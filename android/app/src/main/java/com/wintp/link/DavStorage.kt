package com.wintp.link

import android.content.Context
import android.net.Uri
import android.os.ParcelFileDescriptor
import android.provider.DocumentsContract
import android.webkit.MimeTypeMap
import androidx.documentfile.provider.DocumentFile
import java.io.File
import java.io.FileInputStream
import java.io.FileOutputStream
import java.io.InputStream

/** One file or folder as the WebDAV layer sees it. */
data class DavEntry(val name: String, val isDir: Boolean, val size: Long, val modified: Long)

/**
 * What the phone exposes to the PC. Paths are lists of already-validated
 * segments (see [DriveServer.splitPath]); an empty list is the root.
 */
interface DavStorage {
    fun stat(segs: List<String>): DavEntry?
    fun list(segs: List<String>): List<DavEntry>?
    fun open(segs: List<String>, offset: Long): InputStream?
    /** Creates or overwrites a file from [input]. */
    fun write(segs: List<String>, input: InputStream): Boolean
    /** HTTP status: 201 created, 405 exists, 409 parent missing, 500 failure. */
    fun mkdir(segs: List<String>): Int
    fun delete(segs: List<String>): Boolean
    /** HTTP status: 201/204 success, 409 parent missing, 412 exists and not [overwrite], 500 failure. */
    fun move(from: List<String>, to: List<String>, overwrite: Boolean): Int
}

fun mimeFor(name: String): String {
    val ext = name.substringAfterLast('.', "").lowercase()
    return MimeTypeMap.getSingleton()?.getMimeTypeFromExtension(ext) ?: "application/octet-stream"
}

/** Whole shared storage (or any directory), via plain java.io. Needs "All files access". */
class FileStorage(private val root: File) : DavStorage {
    private fun file(segs: List<String>) = File(root, segs.joinToString("/"))

    private fun entry(f: File, name: String = f.name) = DavEntry(name, f.isDirectory, f.length(), f.lastModified())

    override fun stat(segs: List<String>): DavEntry? {
        val f = file(segs)
        return if (f.exists()) entry(f, segs.lastOrNull() ?: "") else null
    }

    override fun list(segs: List<String>): List<DavEntry>? =
        file(segs).takeIf { it.isDirectory }?.listFiles()?.map { entry(it) }

    override fun open(segs: List<String>, offset: Long): InputStream? {
        val f = file(segs)
        if (!f.isFile) return null
        val s = FileInputStream(f)
        if (offset > 0) s.channel.position(offset)
        return s
    }

    override fun write(segs: List<String>, input: InputStream): Boolean {
        val f = file(segs)
        val parent = f.parentFile ?: return false
        if (!parent.isDirectory) return false
        // Write beside the target and rename, so an interrupted upload never
        // leaves a truncated file under the real name.
        val tmp = File(parent, ".${f.name}.wintp-part")
        return try {
            FileOutputStream(tmp).use { input.copyTo(it) }
            if (f.exists() && !f.delete()) {
                tmp.delete(); false
            } else tmp.renameTo(f).also { if (!it) tmp.delete() }
        } catch (e: Exception) {
            tmp.delete()
            false
        }
    }

    override fun mkdir(segs: List<String>): Int {
        val f = file(segs)
        if (f.exists()) return 405
        if (f.parentFile?.isDirectory != true) return 409
        return if (f.mkdir()) 201 else 500
    }

    override fun delete(segs: List<String>): Boolean {
        val f = file(segs)
        return if (f.isDirectory) f.deleteRecursively() else f.delete()
    }

    override fun move(from: List<String>, to: List<String>, overwrite: Boolean): Int {
        val src = file(from)
        val dst = file(to)
        if (!src.exists()) return 404
        if (dst.parentFile?.isDirectory != true) return 409
        val existed = dst.exists()
        if (existed) {
            if (!overwrite) return 412
            if (!(if (dst.isDirectory) dst.deleteRecursively() else dst.delete())) return 500
        }
        return if (src.renameTo(dst)) (if (existed) 204 else 201) else 500
    }
}

/**
 * Folders the user picked with Android's folder picker. Each picked folder
 * appears as a top-level directory named after it.
 */
class SafStorage(private val ctx: Context, treeUris: List<Uri>) : DavStorage {
    private val roots: Map<String, DocumentFile> = buildMap {
        for (u in treeUris) {
            val doc = DocumentFile.fromTreeUri(ctx, u) ?: continue
            var alias = (doc.name ?: "folder").replace('/', '_')
            var n = 2
            while (containsKey(alias)) alias = "${doc.name} (${n++})"
            put(alias, doc)
        }
    }
    private val cr get() = ctx.contentResolver

    private fun resolve(segs: List<String>): DocumentFile? {
        var cur = roots[segs.firstOrNull() ?: return null] ?: return null
        for (s in segs.drop(1)) cur = cur.findFile(s) ?: return null
        return cur
    }

    private fun entry(d: DocumentFile, name: String = d.name ?: "") =
        DavEntry(name, d.isDirectory, d.length(), d.lastModified())

    override fun stat(segs: List<String>): DavEntry? {
        if (segs.isEmpty()) return DavEntry("", true, 0, 0)
        return resolve(segs)?.let { entry(it, segs.last()) }
    }

    override fun list(segs: List<String>): List<DavEntry>? {
        if (segs.isEmpty()) return roots.map { (alias, d) -> entry(d, alias) }
        val d = resolve(segs)?.takeIf { it.isDirectory } ?: return null
        return d.listFiles().map { entry(it) }
    }

    override fun open(segs: List<String>, offset: Long): InputStream? {
        val d = resolve(segs)?.takeIf { it.isFile } ?: return null
        val pfd = cr.openFileDescriptor(d.uri, "r") ?: return null
        val s = ParcelFileDescriptor.AutoCloseInputStream(pfd)
        if (offset > 0) s.channel.position(offset)
        return s
    }

    override fun write(segs: List<String>, input: InputStream): Boolean {
        if (segs.size < 2) return false
        val parent = resolve(segs.dropLast(1))?.takeIf { it.isDirectory } ?: return false
        val name = segs.last()
        val doc = parent.findFile(name) ?: parent.createFile(mimeFor(name), name) ?: return false
        return try {
            (cr.openOutputStream(doc.uri, "wt") ?: return false).use { input.copyTo(it) }
            true
        } catch (e: Exception) {
            false
        }
    }

    override fun mkdir(segs: List<String>): Int {
        if (segs.size < 2) return 405
        val parent = resolve(segs.dropLast(1))?.takeIf { it.isDirectory } ?: return 409
        if (parent.findFile(segs.last()) != null) return 405
        return if (parent.createDirectory(segs.last()) != null) 201 else 500
    }

    override fun delete(segs: List<String>): Boolean {
        if (segs.size < 2) return false // never delete a picked folder's root
        return resolve(segs)?.delete() ?: false
    }

    override fun move(from: List<String>, to: List<String>, overwrite: Boolean): Int {
        if (from.size < 2 || to.size < 2) return 403
        val src = resolve(from) ?: return 404
        val srcParent = resolve(from.dropLast(1)) ?: return 404
        val dstParent = resolve(to.dropLast(1))?.takeIf { it.isDirectory } ?: return 409
        val existing = dstParent.findFile(to.last())
        if (existing != null) {
            if (!overwrite) return 412
            if (!existing.delete()) return 500
        }
        var uri = src.uri
        if (srcParent.uri != dstParent.uri) {
            uri = try {
                DocumentsContract.moveDocument(cr, uri, srcParent.uri, dstParent.uri)
            } catch (e: Exception) {
                null
            } ?: return 500
        }
        if (DocumentFile.fromSingleUri(ctx, uri)?.name != to.last()) {
            DocumentsContract.renameDocument(cr, uri, to.last()) ?: return 500
        }
        return if (existing != null) 204 else 201
    }
}
