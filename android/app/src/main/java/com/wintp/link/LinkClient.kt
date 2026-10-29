package com.wintp.link

import android.content.ContentResolver
import android.net.Uri
import android.provider.OpenableColumns
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody
import okhttp3.RequestBody.Companion.toRequestBody
import okio.BufferedSink
import okio.source
import org.json.JSONObject
import java.io.IOException
import java.net.URLEncoder
import java.security.MessageDigest
import java.security.cert.CertificateException
import java.security.cert.X509Certificate
import java.util.concurrent.TimeUnit
import javax.net.ssl.SSLContext
import javax.net.ssl.X509TrustManager

/**
 * Talks to the wintp PC server. The server certificate is self-signed, so
 * instead of the system trust store we accept exactly the certificate whose
 * SHA-256 fingerprint was in the pairing QR code.
 */
class LinkClient(private val prefs: Prefs) {

    private class PinnedTrust(private val fp: String) : X509TrustManager {
        override fun checkServerTrusted(chain: Array<X509Certificate>, authType: String) {
            val got = MessageDigest.getInstance("SHA-256").digest(chain[0].encoded)
                .joinToString("") { "%02x".format(it) }
            if (!got.equals(fp, ignoreCase = true)) {
                throw CertificateException("Server certificate does not match the paired PC")
            }
        }

        override fun checkClientTrusted(chain: Array<X509Certificate>, authType: String) {
            throw CertificateException("client certs not supported")
        }

        override fun getAcceptedIssuers(): Array<X509Certificate> = emptyArray()
    }

    private fun client(fp: String, readTimeoutSec: Long = 15, connectTimeoutSec: Long = 3): OkHttpClient {
        val tm = PinnedTrust(fp)
        val ctx = SSLContext.getInstance("TLS").apply { init(null, arrayOf(tm), null) }
        return OkHttpClient.Builder()
            .sslSocketFactory(ctx.socketFactory, tm)
            // Identity is established by the pinned fingerprint, not the hostname.
            .hostnameVerifier { _, _ -> true }
            .connectTimeout(connectTimeoutSec, TimeUnit.SECONDS)
            .readTimeout(readTimeoutSec, TimeUnit.SECONDS)
            .writeTimeout(60, TimeUnit.SECONDS)
            .build()
    }

    /** Hosts to try, most recently successful first. */
    private fun hostOrder(): List<String> {
        val last = prefs.lastHost
        return prefs.hosts.sortedBy { if (it == last) 0 else 1 }
    }

    /** Runs [block] against each known host until one connects. */
    private fun <T> withHost(http: OkHttpClient, block: (base: String, http: OkHttpClient) -> T): T {
        var last: IOException? = null
        for (h in hostOrder()) {
            try {
                val r = block("https://$h:${prefs.port}", http)
                prefs.lastHost = h
                return r
            } catch (e: java.net.ConnectException) {
                last = e
            } catch (e: java.net.SocketTimeoutException) {
                last = e
            } catch (e: java.net.NoRouteToHostException) {
                last = e
            }
        }
        throw last ?: IOException("No PC address known")
    }

    private fun authed(url: String) = Request.Builder().url(url)
        .header("Authorization", "Bearer ${prefs.token}")
        .apply {
            // Tells the PC we are sharing storage and on which port.
            val port = DriveServer.runningPort
            if (port > 0) header("X-Drive-Port", port.toString())
        }

    /** Exchanges the one-time pairing code for a long-lived token. */
    fun pair(hosts: List<String>, port: Int, fp: String, code: String, deviceName: String): String {
        val http = client(fp)
        val body = JSONObject().put("code", code).put("name", deviceName).toString()
            .toRequestBody("application/json".toMediaType())
        var last: IOException? = null
        for (h in hosts) {
            try {
                val req = Request.Builder().url("https://$h:$port/v1/pair").post(body).build()
                http.newCall(req).execute().use { resp ->
                    if (!resp.isSuccessful) throw IOException("Pairing refused (${resp.code}). Show a new QR code and try again.")
                    prefs.lastHost = h
                    return JSONObject(resp.body!!.string()).getString("token")
                }
            } catch (e: java.net.ConnectException) {
                last = e
            } catch (e: java.net.SocketTimeoutException) {
                last = e
            }
        }
        throw last ?: IOException("No PC address in QR code")
    }

    fun ping(): Boolean = try {
        withHost(client(prefs.fingerprint)) { base, http ->
            http.newCall(authed("$base/v1/ping").build()).execute().use { it.isSuccessful }
        }
    } catch (e: IOException) {
        false
    }

    data class Clip(val seq: Long, val text: String)

    /** Long-polls for a clipboard change newer than [since]. */
    fun getClipboard(since: Long, waitSec: Int): Clip {
        val http = client(prefs.fingerprint, readTimeoutSec = waitSec + 15L)
        return withHost(http) { base, h ->
            h.newCall(authed("$base/v1/clipboard?since=$since&wait=$waitSec").build()).execute().use { resp ->
                if (!resp.isSuccessful) throw IOException("HTTP ${resp.code}")
                val j = JSONObject(resp.body!!.string())
                Clip(j.getLong("seq"), j.getString("text"))
            }
        }
    }

    fun putClipboard(text: String) {
        val body = JSONObject().put("text", text).toString().toRequestBody("application/json".toMediaType())
        withHost(client(prefs.fingerprint)) { base, http ->
            http.newCall(authed("$base/v1/clipboard").put(body).build()).execute().use { resp ->
                if (!resp.isSuccessful) throw IOException("HTTP ${resp.code}")
            }
        }
    }

    /** Fetches the TLS certificate/key and access token the phone drive server needs, and caches them. */
    fun driveCredentials() {
        withHost(client(prefs.fingerprint)) { base, http ->
            http.newCall(authed("$base/v1/drive/credentials").build()).execute().use { resp ->
                if (!resp.isSuccessful) throw IOException("HTTP ${resp.code}")
                val j = JSONObject(resp.body!!.string())
                prefs.driveCert = j.getString("cert")
                prefs.driveKey = j.getString("key")
                prefs.driveToken = j.getString("token")
            }
        }
    }

    data class PcEntry(val name: String, val dir: Boolean, val size: Long, val mtime: Long)

    private fun pcUrl(base: String, endpoint: String, path: String) =
        base.toHttpUrl().newBuilder().addPathSegments("v1/pc/$endpoint").addQueryParameter("path", path).build().toString()

    /** Lists a folder the PC shares; "" lists the shared folders themselves. */
    fun pcList(path: String): List<PcEntry> =
        withHost(client(prefs.fingerprint)) { base, http ->
            http.newCall(authed(pcUrl(base, "list", path)).build()).execute().use { resp ->
                if (!resp.isSuccessful) throw IOException("HTTP ${resp.code}")
                val arr = JSONObject(resp.body!!.string()).getJSONArray("entries")
                (0 until arr.length()).map {
                    val o = arr.getJSONObject(it)
                    PcEntry(o.getString("name"), o.getBoolean("dir"), o.getLong("size"), o.getLong("mtime"))
                }
            }
        }

    /** Streams a shared file into [out]. */
    fun pcDownload(path: String, out: java.io.OutputStream) {
        withHost(client(prefs.fingerprint, readTimeoutSec = 120)) { base, http ->
            http.newCall(authed(pcUrl(base, "file", path)).build()).execute().use { resp ->
                if (!resp.isSuccessful) throw IOException("HTTP ${resp.code}")
                resp.body!!.byteStream().copyTo(out)
            }
        }
    }

    /** PC Bluetooth radio state: "on", "off" or "none" (no adapter). */
    fun bluetoothState(): String = bluetooth(null)

    fun setBluetooth(on: Boolean): String = bluetooth(on)

    private fun bluetooth(set: Boolean?): String {
        // Switching the radio runs PowerShell on the PC and can take a few seconds.
        val http = client(prefs.fingerprint, readTimeoutSec = 40)
        return withHost(http) { base, h ->
            val b = authed("$base/v1/bluetooth")
            if (set != null) {
                b.put(JSONObject().put("on", set).toString().toRequestBody("application/json".toMediaType()))
            }
            h.newCall(b.build()).execute().use { resp ->
                if (!resp.isSuccessful) throw IOException("PC Bluetooth failed (HTTP ${resp.code})")
                JSONObject(resp.body!!.string()).getString("state")
            }
        }
    }

    /** Presses a media key on the PC: playpause, next, prev, stop, volup, voldown, mute. */
    fun media(action: String) {
        val body = JSONObject().put("action", action).toString().toRequestBody("application/json".toMediaType())
        withHost(client(prefs.fingerprint)) { base, http ->
            http.newCall(authed("$base/v1/media").post(body).build()).execute().use { resp ->
                if (!resp.isSuccessful) throw IOException("PC refused (HTTP ${resp.code})")
            }
        }
    }

    /** Asks the PC to open [url] in its default browser. */
    fun openUrl(url: String) {
        val body = JSONObject().put("url", url).toString().toRequestBody("application/json".toMediaType())
        withHost(client(prefs.fingerprint)) { base, http ->
            http.newCall(authed("$base/v1/open").post(body).build()).execute().use { resp ->
                if (!resp.isSuccessful) throw IOException("PC refused the link (HTTP ${resp.code})")
            }
        }
    }

    /** Streams [uri] to the PC. Returns the name the PC saved it under. */
    fun uploadFile(cr: ContentResolver, uri: Uri): String {
        val name = displayName(cr, uri)
        val http = client(prefs.fingerprint, readTimeoutSec = 120)
        return withHost(http) { base, h ->
            val body = object : RequestBody() {
                override fun contentType() = "application/octet-stream".toMediaType()
                override fun writeTo(sink: BufferedSink) {
                    val input = cr.openInputStream(uri) ?: throw IOException("Cannot open $name")
                    input.source().use { sink.writeAll(it) }
                }
            }
            val req = authed("$base/v1/files")
                .header("X-Filename", URLEncoder.encode(name, "UTF-8").replace("+", "%20"))
                .post(body).build()
            h.newCall(req).execute().use { resp ->
                if (!resp.isSuccessful) throw IOException("Upload failed (HTTP ${resp.code})")
                JSONObject(resp.body!!.string()).getString("name")
            }
        }
    }

    private fun displayName(cr: ContentResolver, uri: Uri): String {
        cr.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME), null, null, null)?.use { c ->
            if (c.moveToFirst()) {
                val n = c.getString(0)
                if (!n.isNullOrBlank()) return n
            }
        }
        return uri.lastPathSegment?.substringAfterLast('/') ?: "file"
    }
}
