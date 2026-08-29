package com.mobileegress.agent.security

import android.net.Network
import java.io.ByteArrayInputStream
import java.net.Socket
import java.security.KeyStore
import java.security.Principal
import java.security.PrivateKey
import java.security.cert.X509Certificate
import javax.net.ssl.KeyManager
import javax.net.ssl.SSLEngine
import javax.net.ssl.SSLContext
import javax.net.ssl.TrustManagerFactory
import javax.net.ssl.X509ExtendedKeyManager
import javax.net.ssl.X509TrustManager
import okhttp3.ConnectionSpec
import okhttp3.Dns
import okhttp3.OkHttpClient

object PinnedTls {
    fun trustManager(ca: X509Certificate): X509TrustManager {
        val roots = KeyStore.getInstance(KeyStore.getDefaultType()).apply {
            load(null)
            setCertificateEntry("relay-ca", ca)
        }
        val factory = TrustManagerFactory.getInstance(TrustManagerFactory.getDefaultAlgorithm()).apply {
            init(roots)
        }
        return factory.trustManagers.filterIsInstance<X509TrustManager>().single()
    }

    fun clientBuilder(
        network: Network,
        trustManager: X509TrustManager,
        keyManager: KeyManager? = null,
    ): OkHttpClient.Builder {
        val context = SSLContext.getInstance("TLSv1.3").apply {
            init(if (keyManager == null) null else arrayOf(keyManager), arrayOf(trustManager), null)
        }
        return OkHttpClient.Builder()
            .socketFactory(network.socketFactory)
            .dns(object : Dns {
                override fun lookup(hostname: String) = network.getAllByName(hostname).toList()
            })
            .sslSocketFactory(context.socketFactory, trustManager)
            .connectionSpecs(listOf(ConnectionSpec.RESTRICTED_TLS))
            .retryOnConnectionFailure(false)
    }

    fun deviceKeyManager(identity: AgentIdentity, privateKey: PrivateKey): X509ExtendedKeyManager {
        val chain = parseCertificateChain(identity.certificatePem)
        return DeviceKeyManager(identity.keyAlias, privateKey, chain)
    }

    fun parseCertificateChain(pem: String): Array<X509Certificate> {
        val certificates = java.security.cert.CertificateFactory.getInstance("X.509")
            .generateCertificates(ByteArrayInputStream(pem.encodeToByteArray()))
            .filterIsInstance<X509Certificate>()
            .toTypedArray()
        require(certificates.isNotEmpty()) { "Certificate chain is empty" }
        return certificates
    }

    private class DeviceKeyManager(
        private val alias: String,
        private val privateKey: PrivateKey,
        private val chain: Array<X509Certificate>,
    ) : X509ExtendedKeyManager() {
        override fun chooseClientAlias(keyType: Array<out String>?, issuers: Array<out Principal>?, socket: Socket?): String? =
            alias.takeIf { keyType?.any { type -> type.startsWith("EC", ignoreCase = true) } != false }

        override fun chooseEngineClientAlias(keyType: Array<out String>?, issuers: Array<out Principal>?, engine: SSLEngine?): String? =
            chooseClientAlias(keyType, issuers, null)

        override fun getClientAliases(keyType: String?, issuers: Array<out Principal>?): Array<String> =
            if (keyType?.startsWith("EC", ignoreCase = true) == true) arrayOf(alias) else emptyArray()

        override fun getCertificateChain(requestedAlias: String?): Array<X509Certificate>? =
            chain.takeIf { requestedAlias == alias }?.clone()

        override fun getPrivateKey(requestedAlias: String?): PrivateKey? = privateKey.takeIf { requestedAlias == alias }
        override fun chooseServerAlias(keyType: String?, issuers: Array<out Principal>?, socket: Socket?): String? = null
        override fun getServerAliases(keyType: String?, issuers: Array<out Principal>?): Array<String>? = null
    }
}
