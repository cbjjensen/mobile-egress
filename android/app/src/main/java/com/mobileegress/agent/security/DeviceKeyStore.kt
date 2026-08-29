package com.mobileegress.agent.security

import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import java.io.StringWriter
import java.security.KeyPairGenerator
import java.security.KeyStore
import java.security.PrivateKey
import java.security.PublicKey
import java.security.spec.ECGenParameterSpec
import java.util.UUID
import org.bouncycastle.openssl.jcajce.JcaPEMWriter
import org.bouncycastle.operator.jcajce.JcaContentSignerBuilder
import org.bouncycastle.pkcs.jcajce.JcaPKCS10CertificationRequestBuilder
import javax.security.auth.x500.X500Principal

data class DeviceKey(
    val alias: String,
    val privateKey: PrivateKey,
    val publicKey: PublicKey,
)

class DeviceKeyStore {
    private fun keyStore(): KeyStore = KeyStore.getInstance(ANDROID_KEY_STORE).apply { load(null) }

    fun create(): DeviceKey {
        val alias = "$DEVICE_KEY_PREFIX${UUID.randomUUID()}"
        val generator = KeyPairGenerator.getInstance(KeyProperties.KEY_ALGORITHM_EC, ANDROID_KEY_STORE)
        generator.initialize(
            KeyGenParameterSpec.Builder(
                alias,
                KeyProperties.PURPOSE_SIGN or KeyProperties.PURPOSE_VERIFY,
            )
                .setAlgorithmParameterSpec(ECGenParameterSpec("secp256r1"))
                .setDigests(KeyProperties.DIGEST_SHA256)
                .setUserAuthenticationRequired(false)
                .build(),
        )
        val pair = generator.generateKeyPair()
        return DeviceKey(alias, pair.private, pair.public)
    }

    fun privateKey(alias: String): PrivateKey? = keyStore().getKey(alias, null) as? PrivateKey

    fun delete(alias: String) {
        if (alias.startsWith(DEVICE_KEY_PREFIX)) {
            keyStore().deleteEntry(alias)
        }
    }

    fun createCsrPem(deviceKey: DeviceKey): String {
        val request = JcaPKCS10CertificationRequestBuilder(
            X500Principal("CN=mobile-egress-android"),
            deviceKey.publicKey,
        ).build(JcaContentSignerBuilder("SHA256withECDSA").build(deviceKey.privateKey))
        return StringWriter().use { output ->
            JcaPEMWriter(output).use { it.writeObject(request) }
            output.toString()
        }
    }

    companion object {
        private const val ANDROID_KEY_STORE = "AndroidKeyStore"
        private const val DEVICE_KEY_PREFIX = "mobile_egress_device_"
    }
}
