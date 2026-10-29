package com.wintp.link

import fi.iki.elonen.NanoHTTPD
import java.io.ByteArrayOutputStream
import java.io.InputStream
import java.net.URLDecoder
import java.net.URLEncoder
import java.security.MessageDigest
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale
import java.util.TimeZone
import javax.net.ssl.SSLServerSocketFactory

/**
 * A small WebDAV server (enough for rclone) over TLS. Only the PC holding the
 * access token may use it; the PC pins this server's certificate in turn.
 */
class DriveServer(
    port: Int,
    private val token: String,
    private val storage: DavStorage,
    private val readOnly: Boolean,
    tls: SSLServerSocketFactory,
) : NanoHTTPD(port) {

    init {
        makeSecure(tls, null)
    }

    companion object {
        const val PORT = 48766

        /** Port currently being served, or 0. Sent to the PC so it knows we are sharing. */
        @Volatile
        var runningPort = 0

        /** Upper bound on bytes read and discarded to keep a connection usable after a rejected request. */
        private const val MAX_DRAIN = 1L shl 30

        /** Splits a decoded request path into segments, rejecting anything that could escape the root. */
        fun splitPath(path: String): List<String>? {
            val segs = path.split('/').filter { it.isNotEmpty() }
            for (s in segs) {
                if (s == "." || s == ".." || s.contains('\\') || s.contains('\u0000')) return null
            }
            return segs
        }

        private val httpDate: SimpleDateFormat
            get() = SimpleDateFormat("EEE, dd MMM yyyy HH:mm:ss 'GMT'", Locale.US).apply {
                timeZone = TimeZone.getTimeZone("GMT")
            }

        private fun xmlEscape(s: String) = s.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")

        private fun hrefOf(segs: List<String>, dir: Boolean): String {
            val enc = segs.joinToString("/") { URLEncoder.encode(it, "UTF-8").replace("+", "%20") }
            return "/" + enc + if (dir && segs.isNotEmpty()) "/" else ""
        }
    }

    override fun start(timeout: Int, daemon: Boolean) {
        super.start(timeout, daemon)
        runningPort = listeningPort
    }

    override fun stop() {
        runningPort = 0
        super.stop()
    }

    override fun serve(session: IHTTPSession): Response {
        return try {
            handle(session)
        } catch (e: Exception) {
            text(Response.Status.INTERNAL_ERROR, "error")
        }
    }

    private fun handle(session: IHTTPSession): Response {
        val body = bodyOf(session)
        val auth = session.headers["authorization"] ?: ""
        if (!MessageDigest.isEqual(auth.toByteArray(), "Bearer $token".toByteArray())) {
            drain(body)
            return text(Response.Status.UNAUTHORIZED, "unauthorized")
        }
        // NanoHTTPD has already percent-decoded session.uri.
        val segs = splitPath(session.uri)
        if (segs == null) {
            drain(body)
            return text(Response.Status.FORBIDDEN, "bad path")
        }
        val method = session.method
        val writes = method == Method.PUT || method == Method.DELETE || method == Method.MOVE ||
            method == Method.MKCOL
        if (writes && readOnly) {
            drain(body)
            return text(Response.Status.FORBIDDEN, "read-only")
        }
        // Everything except PUT has a small, ignorable body; consume it so the
        // connection can be reused for the next request.
        if (method != Method.PUT) drain(body)

        return when (method) {
            Method.OPTIONS -> options()
            Method.PROPFIND -> propfind(session, segs)
            Method.GET, Method.HEAD -> get(session, segs)
            Method.PUT -> put(segs, body)
            Method.DELETE -> delete(segs)
            Method.MKCOL -> mkcol(segs)
            Method.MOVE -> move(session, segs)
            else -> text(Response.Status.NOT_IMPLEMENTED, "not implemented")
        }
    }

    // --- methods ---

    private fun options(): Response = text(Response.Status.OK, "").apply {
        addHeader("DAV", "1")
        addHeader("MS-Author-Via", "DAV")
        addHeader("Allow", "OPTIONS, PROPFIND, GET, HEAD, PUT, DELETE, MKCOL, MOVE")
    }

    private fun propfind(session: IHTTPSession, segs: List<String>): Response {
        val self = storage.stat(segs) ?: return text(Response.Status.NOT_FOUND, "not found")
        val depth = session.headers["depth"] ?: "1"
        val sb = StringBuilder("""<?xml version="1.0" encoding="utf-8"?><D:multistatus xmlns:D="DAV:">""")
        sb.append(propEntry(segs, self))
        if (self.isDir && depth != "0") {
            storage.list(segs)?.sortedBy { it.name.lowercase() }?.forEach { e ->
                sb.append(propEntry(segs + e.name, e))
            }
        }
        sb.append("</D:multistatus>")
        return newFixedLengthResponse(Response.Status.MULTI_STATUS, "application/xml; charset=utf-8", sb.toString())
    }

    private fun propEntry(segs: List<String>, e: DavEntry): String {
        val sb = StringBuilder()
        sb.append("<D:response><D:href>").append(xmlEscape(hrefOf(segs, e.isDir))).append("</D:href>")
        sb.append("<D:propstat><D:prop>")
        sb.append("<D:displayname>").append(xmlEscape(e.name)).append("</D:displayname>")
        if (e.isDir) {
            sb.append("<D:resourcetype><D:collection/></D:resourcetype>")
        } else {
            sb.append("<D:resourcetype/>")
            sb.append("<D:getcontentlength>").append(e.size).append("</D:getcontentlength>")
            sb.append("<D:getcontenttype>").append(mimeFor(e.name)).append("</D:getcontenttype>")
        }
        if (e.modified > 0) {
            sb.append("<D:getlastmodified>").append(httpDate.format(Date(e.modified))).append("</D:getlastmodified>")
        }
        sb.append("</D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat></D:response>")
        return sb.toString()
    }

    private fun get(session: IHTTPSession, segs: List<String>): Response {
        val e = storage.stat(segs) ?: return text(Response.Status.NOT_FOUND, "not found")
        if (e.isDir) return text(Response.Status.METHOD_NOT_ALLOWED, "is a directory")
        val total = e.size
        var start = 0L
        var end = total - 1
        var partial = false
        session.headers["range"]?.let { r ->
            val m = Regex("""bytes=(\d*)-(\d*)""").matchEntire(r.trim())
            if (m != null && (m.groupValues[1].isNotEmpty() || m.groupValues[2].isNotEmpty())) {
                if (m.groupValues[1].isEmpty()) { // suffix: last N bytes
                    start = (total - m.groupValues[2].toLong()).coerceAtLeast(0)
                } else {
                    start = m.groupValues[1].toLong()
                    if (m.groupValues[2].isNotEmpty()) end = minOf(end, m.groupValues[2].toLong())
                }
                if (start > end || start >= total) {
                    return text(Response.Status.RANGE_NOT_SATISFIABLE, "").apply {
                        addHeader("Content-Range", "bytes */$total")
                    }
                }
                partial = true
            }
        }
        val length = if (total == 0L) 0 else end - start + 1
        val stream = if (session.method == Method.HEAD || length == 0L) null else storage.open(segs, start)
        val body: InputStream = stream?.let { LimitedInputStream(it, length) } ?: "".byteInputStream()
        val resp = newFixedLengthResponse(
            if (partial) Response.Status.PARTIAL_CONTENT else Response.Status.OK,
            mimeFor(e.name), body, length,
        )
        resp.addHeader("Accept-Ranges", "bytes")
        if (partial) resp.addHeader("Content-Range", "bytes $start-$end/$total")
        if (e.modified > 0) resp.addHeader("Last-Modified", httpDate.format(Date(e.modified)))
        return resp
    }

    private fun put(segs: List<String>, body: InputStream): Response {
        if (segs.isEmpty()) {
            drain(body)
            return text(Response.Status.METHOD_NOT_ALLOWED, "cannot write root")
        }
        val existing = storage.stat(segs)
        if (existing?.isDir == true) {
            drain(body)
            return text(Response.Status.METHOD_NOT_ALLOWED, "is a directory")
        }
        if (!storage.write(segs, body)) {
            drain(body)
            return text(Response.Status.CONFLICT, "cannot write")
        }
        return text(if (existing == null) Response.Status.CREATED else Response.Status.NO_CONTENT, "")
    }

    private fun delete(segs: List<String>): Response {
        if (segs.isEmpty()) return text(Response.Status.FORBIDDEN, "cannot delete root")
        if (storage.stat(segs) == null) return text(Response.Status.NOT_FOUND, "not found")
        return if (storage.delete(segs)) text(Response.Status.NO_CONTENT, "")
        else text(Response.Status.INTERNAL_ERROR, "delete failed")
    }

    private fun mkcol(segs: List<String>): Response {
        if (segs.isEmpty()) return text(Response.Status.METHOD_NOT_ALLOWED, "exists")
        return when (storage.mkdir(segs)) {
            201 -> text(Response.Status.CREATED, "")
            405 -> text(Response.Status.METHOD_NOT_ALLOWED, "exists")
            409 -> text(Response.Status.CONFLICT, "parent missing")
            else -> text(Response.Status.INTERNAL_ERROR, "mkdir failed")
        }
    }

    private fun move(session: IHTTPSession, segs: List<String>): Response {
        if (segs.isEmpty()) return text(Response.Status.FORBIDDEN, "cannot move root")
        val dest = session.headers["destination"] ?: return text(Response.Status.BAD_REQUEST, "no destination")
        val destPath = try {
            java.net.URI(dest).rawPath ?: dest
        } catch (e: Exception) {
            dest
        }
        val to = splitPath(URLDecoder.decode(destPath.replace("+", "%2B"), "UTF-8"))
            ?: return text(Response.Status.FORBIDDEN, "bad destination")
        if (to.isEmpty()) return text(Response.Status.FORBIDDEN, "bad destination")
        val overwrite = (session.headers["overwrite"] ?: "T").uppercase() != "F"
        return when (val code = storage.move(segs, to, overwrite)) {
            201 -> text(Response.Status.CREATED, "")
            204 -> text(Response.Status.NO_CONTENT, "")
            404 -> text(Response.Status.NOT_FOUND, "not found")
            409 -> text(Response.Status.CONFLICT, "parent missing")
            412 -> text(Response.Status.PRECONDITION_FAILED, "exists")
            403 -> text(Response.Status.FORBIDDEN, "not allowed")
            else -> text(Response.Status.INTERNAL_ERROR, "move failed ($code)")
        }
    }

    // --- helpers ---

    private fun text(status: Response.IStatus, msg: String): Response =
        newFixedLengthResponse(status, "text/plain; charset=utf-8", msg)

    /** The request body as a stream, honouring Content-Length or chunked encoding. */
    private fun bodyOf(session: IHTTPSession): InputStream {
        val h = session.headers
        return when {
            (h["transfer-encoding"] ?: "").contains("chunked", ignoreCase = true) ->
                ChunkedInputStream(session.inputStream)
            else -> LimitedInputStream(session.inputStream, h["content-length"]?.toLongOrNull() ?: 0L)
        }
    }

    /** Reads and discards what is left of a body (up to a sane limit). */
    private fun drain(body: InputStream) {
        val buf = ByteArray(8192)
        var total = 0L
        try {
            while (total < MAX_DRAIN) {
                val n = body.read(buf)
                if (n < 0) break
                total += n
            }
        } catch (_: Exception) {
        }
    }

    private class LimitedInputStream(private val src: InputStream, private var remaining: Long) : InputStream() {
        override fun read(): Int {
            if (remaining <= 0) return -1
            val b = src.read()
            if (b >= 0) remaining--
            return b
        }

        override fun read(b: ByteArray, off: Int, len: Int): Int {
            if (remaining <= 0) return -1
            val n = src.read(b, off, minOf(len.toLong(), remaining).toInt())
            if (n > 0) remaining -= n
            return n
        }

        override fun close() {
            src.close()
        }
    }

    /** Decodes "Transfer-Encoding: chunked" request bodies. */
    private class ChunkedInputStream(private val src: InputStream) : InputStream() {
        private var left = 0L
        private var done = false

        private fun readLine(): String {
            val sb = ByteArrayOutputStream()
            while (true) {
                val c = src.read()
                if (c < 0 || c == '\n'.code) break
                if (c != '\r'.code) sb.write(c)
            }
            return sb.toString("US-ASCII")
        }

        private fun nextChunk() {
            val size = readLine().substringBefore(';').trim()
            left = if (size.isEmpty()) 0 else size.toLong(16)
            if (left == 0L) {
                done = true
                while (readLine().isNotEmpty()) { /* trailers */ }
            }
        }

        override fun read(): Int {
            val one = ByteArray(1)
            return if (read(one, 0, 1) < 0) -1 else one[0].toInt() and 0xff
        }

        override fun read(b: ByteArray, off: Int, len: Int): Int {
            if (done) return -1
            if (left == 0L) {
                nextChunk()
                if (done) return -1
            }
            val n = src.read(b, off, minOf(len.toLong(), left).toInt())
            if (n > 0) {
                left -= n
                if (left == 0L) readLine() // CRLF after chunk data
            }
            return n
        }
    }
}
