package com.wintp.link

import android.content.ClipboardManager
import android.content.Context
import android.os.Bundle
import android.widget.Toast
import androidx.activity.ComponentActivity
import kotlin.concurrent.thread

/**
 * Transparent activity that sends the phone clipboard to the PC. Android only
 * lets the focused app read the clipboard, so we wait for window focus.
 */
class ClipSendActivity : ComponentActivity() {
    private var done = false

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
    }

    override fun onWindowFocusChanged(hasFocus: Boolean) {
        super.onWindowFocusChanged(hasFocus)
        if (!hasFocus || done) return
        done = true

        val prefs = Prefs(this)
        if (!prefs.paired) {
            toast("Not paired with a PC")
            finish()
            return
        }
        val cm = getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
        val text = cm.primaryClip?.takeIf { it.itemCount > 0 }?.getItemAt(0)?.coerceToText(this)?.toString()
        if (text.isNullOrEmpty()) {
            toast("Clipboard is empty")
            finish()
            return
        }
        val app = applicationContext
        thread {
            val msg = try {
                LinkClient(prefs).putClipboard(text)
                "Clipboard sent to PC"
            } catch (e: Exception) {
                "Could not reach PC: ${e.message}"
            }
            runOnUiThread {
                Toast.makeText(app, msg, Toast.LENGTH_SHORT).show()
                finish()
            }
        }
    }

    private fun toast(msg: String) = Toast.makeText(this, msg, Toast.LENGTH_SHORT).show()
}
