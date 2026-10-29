package com.wintp.link

import android.content.Intent
import android.net.Uri
import android.os.Bundle
import android.widget.TextView
import androidx.activity.ComponentActivity
import kotlin.concurrent.thread

/** Share-sheet target: sends shared files (or text) to the PC and shows progress. */
class ShareActivity : ComponentActivity() {
    private lateinit var label: TextView

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val pad = (24 * resources.displayMetrics.density).toInt()
        label = TextView(this).apply { textSize = 16f; setPadding(pad, pad, pad, pad) }
        setContentView(label)

        val prefs = Prefs(this)
        if (!prefs.paired) {
            label.text = "Not paired with a PC. Open wintp link and scan the QR code first."
            return
        }

        val uris = sharedUris(intent)
        val text = if (uris.isEmpty()) intent.getStringExtra(Intent.EXTRA_TEXT) else null
        if (uris.isEmpty() && text.isNullOrEmpty()) {
            label.text = "Nothing to send."
            return
        }

        val client = LinkClient(prefs)
        thread {
            try {
                val link = text?.let(::firstWebUrl)
                if (link != null) {
                    ui("Opening on PC…")
                    client.openUrl(link)
                    ui("Opened on the PC.")
                } else if (text != null) {
                    ui("Sending text…")
                    client.putClipboard(text)
                    ui("Text copied to the PC clipboard.")
                } else {
                    var ok = 0
                    val failures = mutableListOf<String>()
                    uris.forEachIndexed { i, uri ->
                        ui("Sending file ${i + 1} of ${uris.size}…")
                        try {
                            client.uploadFile(contentResolver, uri)
                            ok++
                        } catch (e: Exception) {
                            failures += e.message ?: "unknown error"
                        }
                    }
                    ui(
                        buildString {
                            append("Sent $ok of ${uris.size} file(s) to the PC.")
                            if (failures.isNotEmpty()) append("\n\n").append(failures.distinct().joinToString("\n"))
                        },
                    )
                }
            } catch (e: Exception) {
                ui("Could not reach PC: ${e.message}")
            }
        }
    }

    /** Shared text often looks like "Page title https://…"; pick out the link. */
    private fun firstWebUrl(text: String): String? =
        Regex("""https?://[^\s<>"]+""", RegexOption.IGNORE_CASE).find(text)?.value

    private fun ui(msg: String) = runOnUiThread { label.text = msg }

    @Suppress("DEPRECATION")
    private fun sharedUris(i: Intent): List<Uri> = when (i.action) {
        Intent.ACTION_SEND -> listOfNotNull(i.getParcelableExtra<Uri>(Intent.EXTRA_STREAM))
        Intent.ACTION_SEND_MULTIPLE -> i.getParcelableArrayListExtra<Uri>(Intent.EXTRA_STREAM) ?: emptyList()
        else -> emptyList()
    }
}
