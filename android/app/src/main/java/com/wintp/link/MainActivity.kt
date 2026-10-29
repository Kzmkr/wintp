package com.wintp.link

import android.Manifest
import android.content.Intent
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.os.Environment
import android.provider.Settings
import android.widget.RadioButton
import android.widget.RadioGroup
import androidx.documentfile.provider.DocumentFile
import android.widget.Button
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.Switch
import android.widget.TextView
import android.widget.Toast
import androidx.activity.ComponentActivity
import androidx.activity.result.contract.ActivityResultContracts
import androidx.core.view.ViewCompat
import androidx.core.view.WindowInsetsCompat
import com.google.mlkit.vision.codescanner.GmsBarcodeScanning
import java.io.IOException
import kotlin.concurrent.thread

class MainActivity : ComponentActivity() {
    private lateinit var prefs: Prefs
    private lateinit var status: TextView
    private lateinit var syncSwitch: Switch
    private lateinit var scanBtn: Button
    private lateinit var sendClipBtn: Button
    private lateinit var sendFilesBtn: Button
    private lateinit var unpairBtn: Button
    private val mediaBtns = mutableListOf<Button>()
    private lateinit var driveSwitch: Switch
    private lateinit var driveStatus: TextView
    private lateinit var modeFolders: RadioButton
    private lateinit var modeAll: RadioButton
    private lateinit var pickFolderBtn: Button
    private lateinit var clearFoldersBtn: Button
    private lateinit var readOnlySwitch: Switch
    private lateinit var btStatus: TextView
    private lateinit var btOffBtn: Button
    private lateinit var btOnBtn: Button

    private val pickFiles = registerForActivityResult(ActivityResultContracts.OpenMultipleDocuments()) { uris ->
        if (uris.isNotEmpty()) startActivity(
            Intent(this, ShareActivity::class.java)
                .setAction(Intent.ACTION_SEND_MULTIPLE)
                .putParcelableArrayListExtra(Intent.EXTRA_STREAM, ArrayList(uris))
                .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION),
        )
    }

    private val notifPermission = registerForActivityResult(ActivityResultContracts.RequestPermission()) {
        // Features still work without the permission; the notification is just hidden.
        SyncService.sync(this)
    }

    private val pickFolder = registerForActivityResult(ActivityResultContracts.OpenDocumentTree()) { uri ->
        if (uri != null) {
            contentResolver.takePersistableUriPermission(
                uri, Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_GRANT_WRITE_URI_PERMISSION,
            )
            prefs.driveFolders = (prefs.driveFolders + uri.toString()).distinct()
            render()
            applyService()
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        prefs = Prefs(this)

        val pad = (16 * resources.displayMetrics.density).toInt()
        val root = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(pad, pad, pad, pad)
        }
        status = TextView(this).apply { textSize = 16f }
        scanBtn = Button(this).apply { text = "Scan pairing QR code"; setOnClickListener { scan() } }
        syncSwitch = Switch(this).apply {
            text = "Receive PC clipboard (background sync)"
            setOnCheckedChangeListener { v, on -> if (v.isPressed) setSync(on) }
        }
        sendClipBtn = Button(this).apply {
            text = "Send clipboard to PC"
            setOnClickListener { startActivity(Intent(this@MainActivity, ClipSendActivity::class.java)) }
        }
        sendFilesBtn = Button(this).apply {
            text = "Send files to PC"
            setOnClickListener { pickFiles.launch(arrayOf("*/*")) }
        }
        unpairBtn = Button(this).apply { text = "Unpair"; setOnClickListener { unpair() } }

        val browseBtn = Button(this).apply {
            text = "Browse PC files"
            setOnClickListener { startActivity(Intent(this@MainActivity, PcFilesActivity::class.java)) }
        }
        listOf(status, scanBtn, syncSwitch, sendClipBtn, sendFilesBtn, browseBtn).forEach { root.addView(it) }
        root.addView(TextView(this).apply { text = "PC media"; textSize = 14f; setPadding(0, pad, 0, 0) })
        root.addView(mediaRow(listOf("⏮" to "prev", "⏯" to "playpause", "⏭" to "next")))
        root.addView(mediaRow(listOf("Vol −" to "voldown", "Mute" to "mute", "Vol +" to "volup")))
        btStatus = TextView(this).apply { textSize = 16f; text = "PC Bluetooth: …" }
        btOffBtn = Button(this).apply { text = "Turn PC Bluetooth off"; setOnClickListener { setPcBluetooth(false) } }
        btOnBtn = Button(this).apply { text = "Turn PC Bluetooth on"; setOnClickListener { setPcBluetooth(true) } }
        root.addView(TextView(this).apply { text = "Headset"; textSize = 14f; setPadding(0, pad, 0, 0) })
        root.addView(btStatus)
        root.addView(btOffBtn)
        root.addView(btOnBtn)

        root.addView(TextView(this).apply { text = "Phone drive"; textSize = 14f; setPadding(0, pad, 0, 0) })
        driveSwitch = Switch(this).apply {
            text = "Share phone storage with PC"
            setOnCheckedChangeListener { v, on -> if (v.isPressed) setDrive(on) }
        }
        modeFolders = RadioButton(this).apply { text = "Only folders I pick" }
        modeAll = RadioButton(this).apply { text = "All shared storage (needs All files access)" }
        val modeGroup = RadioGroup(this).apply {
            addView(modeFolders)
            addView(modeAll)
            setOnCheckedChangeListener { _, id ->
                prefs.driveMode = if (id == modeAll.id) "all" else "folders"
                render()
                if (prefs.driveEnabled) enableDriveAccess()
                applyService()
            }
        }
        pickFolderBtn = Button(this).apply { text = "Choose folders…"; setOnClickListener { pickFolder.launch(null) } }
        clearFoldersBtn = Button(this).apply {
            text = "Clear folders"
            setOnClickListener {
                prefs.driveFolders.forEach { s ->
                    try {
                        contentResolver.releasePersistableUriPermission(
                            Uri.parse(s),
                            Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_GRANT_WRITE_URI_PERMISSION,
                        )
                    } catch (_: Exception) {
                    }
                }
                prefs.driveFolders = emptyList()
                render()
                applyService()
            }
        }
        readOnlySwitch = Switch(this).apply {
            text = "Read-only (PC cannot change files)"
            setOnCheckedChangeListener { v, on ->
                if (v.isPressed) {
                    prefs.driveReadOnly = on
                    applyService()
                }
            }
        }
        driveStatus = TextView(this).apply { textSize = 14f }
        listOf(driveSwitch, modeGroup, pickFolderBtn, clearFoldersBtn, readOnlySwitch, driveStatus).forEach { root.addView(it) }

        root.addView(unpairBtn)
        val scroll = ScrollView(this).apply { addView(root) }
        // Edge-to-edge is enforced on recent Android; keep content clear of the system bars.
        ViewCompat.setOnApplyWindowInsetsListener(scroll) { v, insets ->
            val bars = insets.getInsets(WindowInsetsCompat.Type.systemBars())
            v.setPadding(bars.left, bars.top, bars.right, bars.bottom)
            insets
        }
        setContentView(scroll)
    }

    override fun onResume() {
        super.onResume()
        render()
    }

    private fun render() {
        val paired = prefs.paired
        status.text = if (paired) "Paired with PC (${prefs.lastHost.ifEmpty { prefs.hosts.first() }})"
        else "Not paired. On your PC open wintp → Phone link, enable it, then scan the QR code."
        scanBtn.text = if (paired) "Re-pair (scan new QR code)" else "Scan pairing QR code"
        syncSwitch.isChecked = prefs.syncEnabled
        (listOf(syncSwitch, sendClipBtn, sendFilesBtn, unpairBtn, btOffBtn, btOnBtn) + mediaBtns).forEach { it.isEnabled = paired }
        if (paired) refreshPcBluetooth() else btStatus.text = "PC Bluetooth: not paired"
        renderDrive(paired)
    }

    private fun mediaRow(items: List<Pair<String, String>>) = LinearLayout(this).apply {
        orientation = LinearLayout.HORIZONTAL
        items.forEach { (label, action) ->
            val b = Button(this@MainActivity).apply {
                text = label
                textSize = 20f
                setOnClickListener { sendMedia(action) }
            }
            mediaBtns += b
            addView(b, LinearLayout.LayoutParams(0, LinearLayout.LayoutParams.WRAP_CONTENT, 1f))
        }
    }

    private fun renderDrive(paired: Boolean) {
        val all = prefs.driveMode == "all"
        driveSwitch.isChecked = prefs.driveEnabled
        modeAll.isChecked = all
        modeFolders.isChecked = !all
        readOnlySwitch.isChecked = prefs.driveReadOnly
        val names = prefs.driveFolders.map { DocumentFile.fromTreeUri(this, Uri.parse(it))?.name ?: "(unavailable)" }
        pickFolderBtn.visibility = if (all) android.view.View.GONE else android.view.View.VISIBLE
        clearFoldersBtn.visibility = if (all || names.isEmpty()) android.view.View.GONE else android.view.View.VISIBLE
        driveStatus.text = when {
            !paired -> ""
            !prefs.driveEnabled -> "Off. Turn on, then mount \"Phone\" from wintp on the PC."
            all && !Environment.isExternalStorageManager() -> "Grant \"All files access\" to wintp link, then come back."
            !all && names.isEmpty() -> "Choose at least one folder to share."
            all -> "Sharing all shared storage."
            else -> "Sharing: " + names.joinToString(", ")
        }
        listOf(driveSwitch, modeFolders, modeAll, pickFolderBtn, clearFoldersBtn, readOnlySwitch).forEach { it.isEnabled = paired }
    }

    private fun setDrive(on: Boolean) {
        prefs.driveEnabled = on
        if (on) enableDriveAccess()
        render()
        applyService()
    }

    /** Sends the user to Settings if the chosen mode needs a permission we do not have. */
    private fun enableDriveAccess() {
        if (prefs.driveMode == "all" && !Environment.isExternalStorageManager()) {
            startActivity(
                Intent(Settings.ACTION_MANAGE_APP_ALL_FILES_ACCESS_PERMISSION, Uri.parse("package:$packageName")),
            )
        }
    }

    private fun showPcBluetooth(state: String) {
        btStatus.text = when (state) {
            "on" -> "PC Bluetooth: on"
            "off" -> "PC Bluetooth: off"
            else -> "PC has no Bluetooth adapter"
        }
        val usable = state != "none"
        btOffBtn.isEnabled = usable && state != "off"
        btOnBtn.isEnabled = usable && state != "on"
    }

    private fun refreshPcBluetooth() {
        thread {
            val state = try { LinkClient(prefs).bluetoothState() } catch (e: Exception) { null }
            runOnUiThread {
                if (state != null) showPcBluetooth(state) else btStatus.text = "PC Bluetooth: PC not reachable"
            }
        }
    }

    private fun setPcBluetooth(on: Boolean) {
        btOffBtn.isEnabled = false
        btOnBtn.isEnabled = false
        btStatus.text = "PC Bluetooth: switching…"
        thread {
            try {
                val state = LinkClient(prefs).setBluetooth(on)
                runOnUiThread { showPcBluetooth(state) }
            } catch (e: Exception) {
                runOnUiThread { toast("Could not switch PC Bluetooth: ${e.message}"); refreshPcBluetooth() }
            }
        }
    }

    private fun sendMedia(action: String) {
        thread {
            try {
                LinkClient(prefs).media(action)
            } catch (e: Exception) {
                runOnUiThread { toast("Could not reach PC: ${e.message}") }
            }
        }
    }

    private fun scan() {
        GmsBarcodeScanning.getClient(this).startScan()
            .addOnSuccessListener { code -> code.rawValue?.let(::handlePairingUri) }
            .addOnFailureListener { e -> toast("Scan failed: ${e.message}") }
    }

    private fun handlePairingUri(raw: String) {
        val uri = Uri.parse(raw)
        val hosts = uri.getQueryParameter("h")?.split(",")?.filter { it.isNotBlank() }
        val port = uri.getQueryParameter("p")?.toIntOrNull()
        val fp = uri.getQueryParameter("fp")
        val code = uri.getQueryParameter("c")
        if (uri.scheme != "wintp" || hosts.isNullOrEmpty() || port == null || fp == null || code == null) {
            toast("That is not a wintp pairing code")
            return
        }
        status.text = "Pairing…"
        thread {
            try {
                val token = LinkClient(prefs).pair(hosts, port, fp, code, Build.MODEL)
                prefs.hosts = hosts
                prefs.port = port
                prefs.fingerprint = fp
                prefs.token = token
                runOnUiThread { toast("Paired"); render() }
            } catch (e: IOException) {
                runOnUiThread { toast(e.message ?: "Pairing failed"); render() }
            }
        }
    }

    private fun setSync(on: Boolean) {
        prefs.syncEnabled = on
        applyService()
    }

    /** Runs the background service while any feature that needs it is on. */
    private fun applyService() {
        if (!prefs.syncEnabled && !prefs.driveEnabled) {
            SyncService.stop(this)
        } else if (Build.VERSION.SDK_INT >= 33) {
            notifPermission.launch(Manifest.permission.POST_NOTIFICATIONS)
        } else {
            SyncService.sync(this)
        }
    }

    private fun unpair() {
        SyncService.stop(this)
        prefs.clear()
        render()
    }

    private fun toast(msg: String) = Toast.makeText(this, msg, Toast.LENGTH_LONG).show()
}
