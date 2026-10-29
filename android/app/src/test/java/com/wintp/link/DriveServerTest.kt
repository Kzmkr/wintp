package com.wintp.link

import fi.iki.elonen.NanoHTTPD
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import okio.BufferedSink
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Assume.assumeTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import java.io.File
import java.security.cert.X509Certificate
import javax.net.ssl.SSLContext
import javax.net.ssl.X509TrustManager

class DriveServerTest {
    @get:Rule val tmp = TemporaryFolder()

    private val token = "test-token"
    private lateinit var root: File
    private var server: DriveServer? = null
    private lateinit var base: String

    // The PC pins the certificate; here we only care about the HTTP behaviour.
    private val trustAll = object : X509TrustManager {
        override fun checkClientTrusted(c: Array<X509Certificate>, a: String) {}
        override fun checkServerTrusted(c: Array<X509Certificate>, a: String) {}
        override fun getAcceptedIssuers(): Array<X509Certificate> = emptyArray()
    }
    private val http: OkHttpClient by lazy {
        val ctx = SSLContext.getInstance("TLS").apply { init(null, arrayOf(trustAll), null) }
        OkHttpClient.Builder().sslSocketFactory(ctx.socketFactory, trustAll).hostnameVerifier { _, _ -> true }.build()
    }

    private fun resource(name: String) = javaClass.getResource("/$name")!!.readText()

    private fun startServer(readOnly: Boolean = false) {
        val tls = serverSocketFactory(resource("test-cert.pem"), resource("test-key.pem"))
        val s = DriveServer(0, token, FileStorage(root), readOnly, tls)
        s.start(NanoHTTPD.SOCKET_READ_TIMEOUT, false)
        server = s
        base = "https://127.0.0.1:${s.listeningPort}"
    }

    @Before fun setUp() {
        root = tmp.newFolder("root")
        startServer()
    }

    @After fun tearDown() {
        server?.stop()
    }

    private fun req(
        method: String, path: String, body: RequestBody? = null,
        headers: Map<String, String> = emptyMap(), auth: Boolean = true,
    ): Response {
        val b = Request.Builder().url(base + path)
        if (auth) b.header("Authorization", "Bearer $token")
        headers.forEach { (k, v) -> b.header(k, v) }
        val needsBody = method in setOf("PUT", "POST", "PROPFIND")
        b.method(method, body ?: if (needsBody) ByteArray(0).toRequestBody() else null)
        return http.newCall(b.build()).execute()
    }

    private fun put(path: String, content: String) = req("PUT", path, content.toRequestBody("text/plain".toMediaType()))

    // --- tests ---

    @Test fun requiresToken() {
        assertEquals(401, req("PROPFIND", "/", auth = false).use { it.code })
        val wrong = Request.Builder().url("$base/").header("Authorization", "Bearer nope")
            .method("PROPFIND", ByteArray(0).toRequestBody()).build()
        assertEquals(401, http.newCall(wrong).execute().use { it.code })
    }

    @Test fun propfindListsEntries() {
        File(root, "a b.txt").writeText("12345")
        File(root, "dir").mkdir()
        File(root, "R&D.txt").writeText("x")
        val xml = req("PROPFIND", "/", headers = mapOf("Depth" to "1")).use {
            assertEquals(207, it.code)
            it.body!!.string()
        }
        assertTrue(xml, xml.contains("<D:href>/a%20b.txt</D:href>"))
        assertTrue(xml, xml.contains("<D:getcontentlength>5</D:getcontentlength>"))
        assertTrue(xml, xml.contains("<D:href>/dir/</D:href>"))
        assertTrue(xml, xml.contains("<D:collection/>"))
        assertTrue(xml, xml.contains("R&amp;D.txt"))
        // Depth 0 is just the target itself.
        val self = req("PROPFIND", "/", headers = mapOf("Depth" to "0")).use { it.body!!.string() }
        assertFalse(self, self.contains("a%20b.txt"))
        assertEquals(404, req("PROPFIND", "/missing", headers = mapOf("Depth" to "0")).use { it.code })
    }

    @Test fun putGetOverwriteLeavesNoTempFile() {
        assertEquals(201, put("/f.txt", "hello").use { it.code })
        assertEquals(204, put("/f.txt", "hello world").use { it.code })
        assertEquals("hello world", req("GET", "/f.txt").use { it.body!!.string() })
        assertEquals(listOf("f.txt"), root.list()!!.toList())
        assertEquals(409, put("/nodir/f.txt", "x").use { it.code })
    }

    @Test fun rangeRequests() {
        File(root, "r.bin").writeText("0123456789")
        req("GET", "/r.bin", headers = mapOf("Range" to "bytes=2-4")).use {
            assertEquals(206, it.code)
            assertEquals("234", it.body!!.string())
            assertEquals("bytes 2-4/10", it.header("Content-Range"))
        }
        req("GET", "/r.bin", headers = mapOf("Range" to "bytes=7-")).use { assertEquals("789", it.body!!.string()) }
        req("GET", "/r.bin", headers = mapOf("Range" to "bytes=-3")).use { assertEquals("789", it.body!!.string()) }
        assertEquals(416, req("GET", "/r.bin", headers = mapOf("Range" to "bytes=50-60")).use { it.code })
        req("HEAD", "/r.bin").use {
            assertEquals(200, it.code)
            assertEquals("10", it.header("Content-Length"))
        }
    }

    @Test fun mkcolMoveDelete() {
        assertEquals(201, req("MKCOL", "/d").use { it.code })
        assertEquals(405, req("MKCOL", "/d").use { it.code })
        assertEquals(409, req("MKCOL", "/x/y").use { it.code })

        put("/one.txt", "1").close()
        put("/two.txt", "2").close()
        // Absolute, percent-encoded Destination, as rclone sends it.
        assertEquals(201, req("MOVE", "/one.txt", headers = mapOf("Destination" to "$base/d/new%20name.txt")).use { it.code })
        assertTrue(File(root, "d/new name.txt").isFile)
        assertFalse(File(root, "one.txt").exists())
        assertEquals(
            412,
            req("MOVE", "/two.txt", headers = mapOf("Destination" to "$base/d/new%20name.txt", "Overwrite" to "F")).use { it.code },
        )
        assertEquals(
            204,
            req("MOVE", "/two.txt", headers = mapOf("Destination" to "$base/d/new%20name.txt")).use { it.code },
        )
        assertEquals("2", File(root, "d/new name.txt").readText())

        assertEquals(204, req("DELETE", "/d").use { it.code }) // recursive
        assertFalse(File(root, "d").exists())
        assertEquals(404, req("DELETE", "/d").use { it.code })
        assertEquals(403, req("DELETE", "/").use { it.code })
    }

    /** Sends a request line verbatim (OkHttp would normalise "../" away) and returns the status code. */
    private fun raw(method: String, target: String, body: String = ""): Int {
        val sock = http.sslSocketFactory.createSocket("127.0.0.1", server!!.listeningPort)
        sock.use {
            val out = it.getOutputStream()
            out.write(
                ("$method $target HTTP/1.1\r\nHost: x\r\nAuthorization: Bearer $token\r\n" +
                    "Content-Length: ${body.length}\r\nConnection: close\r\n\r\n$body").toByteArray(),
            )
            out.flush()
            val status = it.getInputStream().bufferedReader().readLine() ?: return -1
            return status.split(" ")[1].toInt()
        }
    }

    @Test fun pathTraversalIsBlocked() {
        val outside = File(root.parentFile, "evil.txt")
        for (p in listOf("/../evil.txt", "/%2e%2e/evil.txt", "/a/%2e%2e/%2e%2e/evil.txt", "/..%5cevil.txt", "/%2e/evil.txt", "/a/..")) {
            assertEquals(p, 403, raw("PUT", p, "pwned"))
        }
        assertFalse(outside.exists())
        assertEquals(403, raw("GET", "/%2e%2e/evil.txt"))
        assertEquals(403, raw("DELETE", "/%2e%2e"))
        assertEquals(400, raw("MOVE", "/x"))  // no Destination header: rejected, not a crash
    }

    @Test fun chunkedUploadIsDecoded() {
        val body = object : RequestBody() { // unknown length => Transfer-Encoding: chunked
            override fun contentType() = "application/octet-stream".toMediaType()
            override fun writeTo(sink: BufferedSink) {
                sink.writeUtf8("chunk-one,")
                sink.flush()
                sink.writeUtf8("chunk-two")
            }
        }
        assertEquals(201, req("PUT", "/c.bin", body).use { it.code })
        assertEquals("chunk-one,chunk-two", File(root, "c.bin").readText())
    }

    @Test fun rejectedUploadDoesNotCorruptNextRequestOnSameConnection() {
        val big = "x".repeat(200_000)
        // 401 with an unread body: the server must drain it or the next request parses garbage.
        assertEquals(401, req("PUT", "/f.txt", big.toRequestBody(), auth = false).use { it.code })
        assertEquals(207, req("PROPFIND", "/", headers = mapOf("Depth" to "0")).use { it.code })
        assertEquals(201, put("/ok.txt", "fine").use { it.code })
    }

    @Test fun largeFileRoundTrip() {
        val data = ByteArray(5_000_000) { (it % 251).toByte() }
        assertEquals(201, req("PUT", "/big.bin", data.toRequestBody()).use { it.code })
        val back = req("GET", "/big.bin").use { it.body!!.bytes() }
        assertTrue(data.contentEquals(back))
    }

    @Test fun readOnlyModeBlocksChanges() {
        server?.stop()
        startServer(readOnly = true)
        File(root, "keep.txt").writeText("keep")
        assertEquals(403, put("/new.txt", "x").use { it.code })
        assertEquals(403, req("DELETE", "/keep.txt").use { it.code })
        assertEquals(403, req("MKCOL", "/d").use { it.code })
        assertEquals(403, req("MOVE", "/keep.txt", headers = mapOf("Destination" to "$base/k2.txt")).use { it.code })
        assertEquals("keep", req("GET", "/keep.txt").use { it.body!!.string() })
        assertTrue(File(root, "keep.txt").exists())
    }

    @Test fun splitPathRejectsEscapes() {
        assertEquals(listOf("a", "b"), DriveServer.splitPath("/a//b/"))
        assertEquals(emptyList<String>(), DriveServer.splitPath("/"))
        assertEquals(null, DriveServer.splitPath("/a/../b"))
        assertEquals(null, DriveServer.splitPath("/./a"))
        assertEquals(null, DriveServer.splitPath("/a\\b"))
    }

    /**
     * Not a real test: keeps the server up so the real rclone can be pointed at
     * it from a shell. Enabled with WINTP_SERVE_SECONDS=<n> and WINTP_SERVE_DIR.
     */
    @Test fun serveForManualRcloneTest() {
        val secs = System.getenv("WINTP_SERVE_SECONDS")?.toLongOrNull()
        assumeTrue(secs != null)
        server?.stop()
        val dir = File(System.getenv("WINTP_SERVE_DIR")!!)
        val tls = serverSocketFactory(resource("test-cert.pem"), resource("test-key.pem"))
        val s = DriveServer(48799, token, FileStorage(dir), false, tls)
        s.start(NanoHTTPD.SOCKET_READ_TIMEOUT, false)
        server = s
        File(dir.parentFile, "serving.flag").writeText("up")
        Thread.sleep(secs!! * 1000)
    }
}
