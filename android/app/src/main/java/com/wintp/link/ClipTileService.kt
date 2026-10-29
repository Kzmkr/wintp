package com.wintp.link

import android.app.PendingIntent
import android.content.Intent
import android.graphics.drawable.Icon
import android.os.Build
import android.service.quicksettings.Tile
import android.service.quicksettings.TileService

/** Quick Settings tile: one tap sends the phone clipboard to the PC. */
class ClipTileService : TileService() {
    override fun onStartListening() {
        qsTile?.apply {
            state = if (Prefs(this@ClipTileService).paired) Tile.STATE_INACTIVE else Tile.STATE_UNAVAILABLE
            icon = Icon.createWithResource(this@ClipTileService, R.drawable.ic_tile_clip)
            updateTile()
        }
    }

    override fun onClick() {
        // The activity must come to the foreground, or Android will not let it read the clipboard.
        val intent = Intent(this, ClipSendActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
        if (Build.VERSION.SDK_INT >= 34) {
            val pi = PendingIntent.getActivity(this, 0, intent, PendingIntent.FLAG_IMMUTABLE)
            startActivityAndCollapse(pi)
        } else {
            @Suppress("DEPRECATION")
            startActivityAndCollapse(intent)
        }
    }
}
