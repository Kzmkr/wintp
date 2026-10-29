package com.wintp.link

import android.content.ContentValues
import android.content.Intent
import android.os.Build
import android.os.Bundle
import android.provider.MediaStore
import android.text.format.Formatter
import android.widget.ArrayAdapter
import android.widget.LinearLayout
import android.widget.ListView
import android.widget.TextView
import android.widget.Toast
import androidx.activity.ComponentActivity
import androidx.activity.OnBackPressedCallback
import androidx.core.content.FileProvider
import androidx.core.view.ViewCompat
import androidx.core.view.WindowInsetsCompat
import java.io.File
import java.text.DateFormat
import java.util.Date
import kotlin.concurrent.thread

/** Browses the folders the PC shares. Tap a file to open it, long-press to save it to Downloads. */
class PcFilesActivity : ComponentActivity() {
    private val prefs by lazy { Prefs(this) }
    private lateinit var pathView: TextView
    private lateinit var list: ListView
    private var path = ""
    private var entries = listOf<LinkClient.PcEntry>()

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val pad = (16 * resources.displayMetrics.density).toInt()
        pathView = TextView(this).apply { textSize = 16f; setPadding(pad, pad, pad, pad / 2) }
        list = ListView(this)
        val root = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            addView(pathView)
            addView(list, LinearLayout.LayoutParams(-1, 0, 1f))
        }
        setContentView(root)
        ViewCompat.setOnApplyWindowInsetsListener(root) { v, insets ->
            val b = insets.getInsets(WindowInsetsCompat.Type.systemBars())
            v.setPadding(b.left, b.top, b.right, b.bottom)
            insets
        }

        list.setOnItemClickListener { _, _, i, _ ->
            val e = entries.getOrNull(i) ?: return@setOnItemClickListener
            if (e.dir) go(join(path, e.name)) else openFile(e)
        }
        list.setOnItemLongClickListener { _, _, i, _ ->
            val e = entries.getOrNull(i)
            if (e != null && !e.dir) {
                saveFile(e)
                true
            } else false
        }
        onBackPressedDispatcher.addCallback(this, object : OnBackPressedCallback(true) {
            override fun handleOnBackPressed() {
                if (path.isEmpty()) finish() else go(path.substringBeforeLast('/', ""))
            }
        })
        go("")
    }

    private fun join(dir: String, name: String) = if (dir.isEmpty()) name else "$dir/$name"

    private fun go(p: String) {
        path = p
        pathView.text = "PC / " + p.replace("/", " / ")
        thread {
            val result = try {
                LinkClient(prefs).pcList(p)
            } catch (e: Exception) {
                runOnUiThread { toast("Could not load: ${e.message}") }
                null
            }
            if (result != null) runOnUiThread { show(result) }
        }
    }

    private fun show(es: List<LinkClient.PcEntry>) {
        entries = es
        val df = DateFormat.getDateInstance(DateFormat.SHORT)
        val rows = es.map {
            if (it.dir) "📁  ${it.name}"
            else "${it.name}\n${Formatter.formatShortFileSize(this, it.size)}  ·  ${df.format(Date(it.mtime))}"
        }
        list.adapter = ArrayAdapter(this, android.R.layout.simple_list_item_1, rows.ifEmpty { listOf("(empty)") })
    }

    private fun safeName(n: String) = n.replace('/', '_').replace('\\', '_')

    private fun openFile(e: LinkClient.PcEntry) {
        toast("Downloading ${e.name}…")
        val remote = join(path, e.name)
        thread {
            try {
                val dir = File(cacheDir, "pc").apply { deleteRecursively(); mkdirs() }
                val f = File(dir, safeName(e.name))
                f.outputStream().use { LinkClient(prefs).pcDownload(remote, it) }
                runOnUiThread {
                    val uri = FileProvider.getUriForFile(this, "$packageName.files", f)
                    val view = Intent(Intent.ACTION_VIEW).setDataAndType(uri, mimeFor(e.name))
                        .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
                    try {
                        startActivity(Intent.createChooser(view, e.name))
                    } catch (ex: Exception) {
                        toast("No app can open this file")
                    }
                }
            } catch (ex: Exception) {
                runOnUiThread { toast("Download failed: ${ex.message}") }
            }
        }
    }

    private fun saveFile(e: LinkClient.PcEntry) {
        toast("Saving ${e.name}…")
        val remote = join(path, e.name)
        thread {
            try {
                val name = safeName(e.name)
                val client = LinkClient(prefs)
                val where = if (Build.VERSION.SDK_INT >= 29) {
                    val values = ContentValues().apply {
                        put(MediaStore.Downloads.DISPLAY_NAME, name)
                        put(MediaStore.Downloads.MIME_TYPE, mimeFor(name))
                        put(MediaStore.Downloads.RELATIVE_PATH, "Download/wintp")
                    }
                    val uri = contentResolver.insert(MediaStore.Downloads.EXTERNAL_CONTENT_URI, values)
                        ?: throw java.io.IOException("cannot create file")
                    try {
                        contentResolver.openOutputStream(uri)!!.use { client.pcDownload(remote, it) }
                    } catch (ex: Exception) {
                        contentResolver.delete(uri, null, null)
                        throw ex
                    }
                    "Downloads/wintp"
                } else {
                    val dir = File(getExternalFilesDir(null), "wintp").apply { mkdirs() }
                    File(dir, name).outputStream().use { client.pcDownload(remote, it) }
                    dir.path
                }
                runOnUiThread { toast("Saved to $where") }
            } catch (ex: Exception) {
                runOnUiThread { toast("Save failed: ${ex.message}") }
            }
        }
    }

    private fun toast(m: String) = Toast.makeText(this, m, Toast.LENGTH_SHORT).show()
}
