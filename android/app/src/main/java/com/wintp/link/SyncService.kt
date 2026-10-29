package com.wintp.link

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.net.Uri
import android.net.wifi.WifiManager
import android.os.Environment
import android.os.Handler
import android.os.IBinder
import android.os.Looper
import androidx.core.app.NotificationCompat
import androidx.core.app.ServiceCompat
import fi.iki.elonen.NanoHTTPD
import java.io.File

/**
 * Background service. It keeps the PC's clipboard flowing to this phone and,
 * when enabled, serves the phone's storage to the PC. Its constant contact
 * with the PC is also how the PC learns this phone's current address.
 *
 * Android 10+ forbids reading the clipboard from the background, so the
 * phone-to-PC clipboard direction is a notification action that opens
 * [ClipSendActivity].
 */
class SyncService : Service() {
    @Volatile private var running = false
    private var worker: Thread? = null
    private val main = Handler(Looper.getMainLooper())

    private val driveLock = Any()
    private var drive: DriveServer? = null
    private var driveSig = ""
    private var wifiLock: WifiManager.WifiLock? = null

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        createChannel()
        ServiceCompat.startForeground(
            this, NOTIF_ID, buildNotification("Connecting…"),
            ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC,
        )
        if (!running) {
            running = true
            worker = Thread(::loop, "wintp-sync").also { it.start() }
        }
        // Settings may have changed; apply them without blocking the main thread.
        Thread({ configureDrive() }, "wintp-drive-config").start()
        return START_STICKY
    }

    override fun onDestroy() {
        running = false
        worker?.interrupt()
        synchronized(driveLock) { stopDrive() }
        super.onDestroy()
    }

    private fun loop() {
        val prefs = Prefs(this)
        val client = LinkClient(prefs)
        var since = -1L
        var backoffMs = 2000L
        while (running) {
            try {
                val clip = client.getClipboard(since.coerceAtLeast(0), 50)
                notify(statusText())
                backoffMs = 2000
                // The first answer only establishes the baseline; don't overwrite
                // whatever is on the phone with stale PC content.
                if (since >= 0 && clip.seq != since && prefs.syncEnabled) applyToPhone(clip.text)
                since = clip.seq
            } catch (e: InterruptedException) {
                return
            } catch (e: Exception) {
                notify("PC not reachable – retrying")
                try {
                    Thread.sleep(backoffMs)
                } catch (ie: InterruptedException) {
                    return
                }
                backoffMs = (backoffMs * 2).coerceAtMost(30_000)
            }
        }
    }

    private fun statusText(): String =
        if (DriveServer.runningPort > 0) "Connected – sharing storage with PC" else "Connected to PC"

    // --- phone drive ---

    private fun stopDrive() {
        drive?.stop()
        drive = null
        driveSig = ""
        wifiLock?.takeIf { it.isHeld }?.release()
        wifiLock = null
    }

    /** Starts, stops or restarts the storage server to match the current settings. */
    private fun configureDrive() = synchronized(driveLock) {
        val prefs = Prefs(this)
        val client = LinkClient(prefs)
        if (!prefs.driveEnabled || !prefs.paired) {
            stopDrive()
            client.ping() // tells the PC we stopped sharing right away
            return@synchronized
        }
        try {
            client.driveCredentials() // refresh; cached copy is used if the PC is unreachable
        } catch (_: Exception) {
        }
        if (prefs.driveCert.isEmpty() || prefs.driveKey.isEmpty() || prefs.driveToken.isEmpty()) {
            stopDrive()
            return@synchronized
        }
        val storage = buildStorage(prefs)
        if (storage == null) {
            stopDrive()
            return@synchronized
        }
        val sig = listOf(
            prefs.driveMode, prefs.driveFolders.joinToString("|"), prefs.driveReadOnly,
            prefs.driveToken, prefs.driveCert.hashCode(),
        ).joinToString(";")
        if (drive != null && sig == driveSig) return@synchronized

        stopDrive()
        try {
            val server = DriveServer(
                DriveServer.PORT, prefs.driveToken, storage, prefs.driveReadOnly,
                serverSocketFactory(prefs.driveCert, prefs.driveKey),
            )
            server.start(NanoHTTPD.SOCKET_READ_TIMEOUT, false)
            drive = server
            driveSig = sig
            wifiLock = (applicationContext.getSystemService(Context.WIFI_SERVICE) as WifiManager)
                .createWifiLock(WifiManager.WIFI_MODE_FULL_HIGH_PERF, "wintp-drive")
                .also { it.acquire() }
        } catch (e: Exception) {
            stopDrive()
        }
        client.ping() // announce the port to the PC right away
    }

    private fun buildStorage(prefs: Prefs): DavStorage? = when (prefs.driveMode) {
        "all" ->
            if (Environment.isExternalStorageManager()) FileStorage(Environment.getExternalStorageDirectory()) else null
        else -> {
            val granted = contentResolver.persistedUriPermissions.filter { it.isReadPermission }.map { it.uri.toString() }
            val uris = prefs.driveFolders.filter { it in granted }.map { Uri.parse(it) }
            if (uris.isEmpty()) null else SafStorage(this, uris)
        }
    }

    private fun applyToPhone(text: String) {
        main.post {
            val cm = getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
            cm.setPrimaryClip(ClipData.newPlainText("wintp", text))
        }
    }

    private fun createChannel() {
        val nm = getSystemService(NotificationManager::class.java)
        nm.createNotificationChannel(
            NotificationChannel(CHANNEL, "Clipboard sync", NotificationManager.IMPORTANCE_LOW),
        )
    }

    private fun notify(text: String) {
        getSystemService(NotificationManager::class.java).notify(NOTIF_ID, buildNotification(text))
    }

    private fun buildNotification(text: String): Notification {
        val send = PendingIntent.getActivity(
            this, 0, Intent(this, ClipSendActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        val open = PendingIntent.getActivity(
            this, 1, Intent(this, MainActivity::class.java),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        return NotificationCompat.Builder(this, CHANNEL)
            .setSmallIcon(android.R.drawable.stat_sys_data_bluetooth)
            .setContentTitle("wintp link")
            .setContentText(text)
            .setContentIntent(open)
            .setOngoing(true)
            .setOnlyAlertOnce(true)
            .addAction(0, "Send clipboard to PC", send)
            .build()
    }

    companion object {
        private const val CHANNEL = "sync"
        private const val NOTIF_ID = 1

        fun start(ctx: Context) {
            ctx.startForegroundService(Intent(ctx, SyncService::class.java))
        }

        fun stop(ctx: Context) {
            ctx.stopService(Intent(ctx, SyncService::class.java))
        }

        /** Starts the service if any feature needs it, stops it otherwise. */
        fun sync(ctx: Context) {
            val p = Prefs(ctx)
            if (p.paired && (p.syncEnabled || p.driveEnabled)) start(ctx) else stop(ctx)
        }
    }
}
