package com.wintp.link

import java.security.KeyFactory
import java.security.KeyStore
import java.security.cert.CertificateFactory
import java.security.spec.PKCS8EncodedKeySpec
import javax.net.ssl.KeyManagerFactory
import javax.net.ssl.SSLContext
import javax.net.ssl.SSLServerSocketFactory

/** Builds a TLS server socket factory from the PEM certificate and PKCS#8 key issued by the PC. */
fun serverSocketFactory(certPem: String, keyPem: String): SSLServerSocketFactory {
    val cert = CertificateFactory.getInstance("X.509").generateCertificate(certPem.byteInputStream())
    val keyDer = java.util.Base64.getMimeDecoder().decode(
        keyPem.lines().filter { it.isNotBlank() && !it.startsWith("-----") }.joinToString(""),
    )
    val key = KeyFactory.getInstance("EC").generatePrivate(PKCS8EncodedKeySpec(keyDer))
    val password = "wintp".toCharArray() // in-memory only; PKCS12 dislikes empty passwords
    val ks = KeyStore.getInstance("PKCS12").apply {
        load(null, null)
        setKeyEntry("drive", key, password, arrayOf(cert))
    }
    val kmf = KeyManagerFactory.getInstance(KeyManagerFactory.getDefaultAlgorithm()).apply { init(ks, password) }
    return SSLContext.getInstance("TLS").apply { init(kmf.keyManagers, null, null) }.serverSocketFactory
}
