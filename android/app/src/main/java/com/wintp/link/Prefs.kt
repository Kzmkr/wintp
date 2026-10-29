package com.wintp.link

import android.content.Context

/** Pairing info and settings, kept in app-private storage. */
class Prefs(ctx: Context) {
    private val sp = ctx.applicationContext.getSharedPreferences("wintp", Context.MODE_PRIVATE)

    var hosts: List<String>
        get() = (sp.getString("hosts", "") ?: "").split(",").filter { it.isNotBlank() }
        set(v) = sp.edit().putString("hosts", v.joinToString(",")).apply()

    var port: Int
        get() = sp.getInt("port", 48765)
        set(v) = sp.edit().putInt("port", v).apply()

    /** SHA-256 hex of the PC's certificate, delivered in the pairing QR code. */
    var fingerprint: String
        get() = sp.getString("fp", "") ?: ""
        set(v) = sp.edit().putString("fp", v).apply()

    var token: String
        get() = sp.getString("token", "") ?: ""
        set(v) = sp.edit().putString("token", v).apply()

    /** Last host that answered, tried first next time. */
    var lastHost: String
        get() = sp.getString("lastHost", "") ?: ""
        set(v) = sp.edit().putString("lastHost", v).apply()

    var syncEnabled: Boolean
        get() = sp.getBoolean("sync", false)
        set(v) = sp.edit().putBoolean("sync", v).apply()

    // --- phone drive (the PC mounts the phone's storage) ---

    var driveEnabled: Boolean
        get() = sp.getBoolean("drive", false)
        set(v) = sp.edit().putBoolean("drive", v).apply()

    /** "folders" = only folders the user picked; "all" = all shared storage. */
    var driveMode: String
        get() = sp.getString("driveMode", "folders") ?: "folders"
        set(v) = sp.edit().putString("driveMode", v).apply()

    var driveReadOnly: Boolean
        get() = sp.getBoolean("driveReadOnly", false)
        set(v) = sp.edit().putBoolean("driveReadOnly", v).apply()

    var driveFolders: List<String>
        get() = (sp.getStringSet("driveFolders", emptySet()) ?: emptySet()).sorted()
        set(v) = sp.edit().putStringSet("driveFolders", v.toSet()).apply()

    /** TLS cert, key and PC access token issued by the PC (cached for offline starts). */
    var driveCert: String
        get() = sp.getString("driveCert", "") ?: ""
        set(v) = sp.edit().putString("driveCert", v).apply()

    var driveKey: String
        get() = sp.getString("driveKey", "") ?: ""
        set(v) = sp.edit().putString("driveKey", v).apply()

    var driveToken: String
        get() = sp.getString("driveToken", "") ?: ""
        set(v) = sp.edit().putString("driveToken", v).apply()

    val paired: Boolean get() = token.isNotEmpty() && fingerprint.isNotEmpty() && hosts.isNotEmpty()

    fun clear() = sp.edit().clear().apply()
}
