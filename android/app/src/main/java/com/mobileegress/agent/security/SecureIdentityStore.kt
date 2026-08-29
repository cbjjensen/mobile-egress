package com.mobileegress.agent.security

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import java.security.KeyStore
import java.security.SecureRandom
import java.util.Base64
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json

@Serializable
data class AgentIdentity(
    val relayOrigin: String,
    val role: String,
    val serial: String,
    val keyAlias: String,
    val certificatePem: String,
    val caCertificatePem: String,
)

class CredentialStoreException(message: String, cause: Throwable? = null) : Exception(message, cause)

class SecureIdentityStore(context: Context) {
    private val preferences = context.getSharedPreferences(PREFERENCES, Context.MODE_PRIVATE)
    private val json = Json { ignoreUnknownKeys = false }

    @Synchronized
    fun load(): AgentIdentity? {
        val stored = preferences.getString(IDENTITY, null) ?: return null
        return try {
            val pieces = stored.split(':')
            if (pieces.size != 3 || pieces[0] != FORMAT_VERSION) throw IllegalArgumentException()
            val cipher = Cipher.getInstance(TRANSFORMATION)
            cipher.init(
                Cipher.DECRYPT_MODE,
                encryptionKey(),
                GCMParameterSpec(128, Base64.getUrlDecoder().decode(pieces[1])),
            )
            cipher.updateAAD(ASSOCIATED_DATA)
            val clear = cipher.doFinal(Base64.getUrlDecoder().decode(pieces[2]))
            json.decodeFromString<AgentIdentity>(clear.decodeToString())
                .takeIf { it.role == "agent" && it.serial.isNotBlank() && it.keyAlias.isNotBlank() }
                ?: throw IllegalArgumentException()
        } catch (error: Exception) {
            throw CredentialStoreException("Stored Agent identity is unavailable", error)
        }
    }

    @Synchronized
    fun save(identity: AgentIdentity) {
        require(identity.role == "agent")
        try {
            val iv = ByteArray(12).also(SecureRandom()::nextBytes)
            val cipher = Cipher.getInstance(TRANSFORMATION)
            cipher.init(Cipher.ENCRYPT_MODE, encryptionKey(), GCMParameterSpec(128, iv))
            cipher.updateAAD(ASSOCIATED_DATA)
            val encrypted = cipher.doFinal(json.encodeToString(identity).encodeToByteArray())
            val encoder = Base64.getUrlEncoder().withoutPadding()
            val value = "$FORMAT_VERSION:${encoder.encodeToString(iv)}:${encoder.encodeToString(encrypted)}"
            if (!preferences.edit().putString(IDENTITY, value).commit()) {
                throw CredentialStoreException("Could not preserve Agent identity")
            }
        } catch (error: CredentialStoreException) {
            throw error
        } catch (error: Exception) {
            throw CredentialStoreException("Could not preserve Agent identity", error)
        }
    }

    @Synchronized
    fun clear() {
        if (!preferences.edit().remove(IDENTITY).commit()) {
            throw CredentialStoreException("Could not clear Agent identity")
        }
    }

    private fun encryptionKey(): SecretKey {
        val keyStore = KeyStore.getInstance(ANDROID_KEY_STORE).apply { load(null) }
        (keyStore.getKey(IDENTITY_KEY_ALIAS, null) as? SecretKey)?.let { return it }
        val generator = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, ANDROID_KEY_STORE)
        generator.init(
            KeyGenParameterSpec.Builder(
                IDENTITY_KEY_ALIAS,
                KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT,
            )
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .setRandomizedEncryptionRequired(true)
                .build(),
        )
        return generator.generateKey()
    }

    companion object {
        private const val ANDROID_KEY_STORE = "AndroidKeyStore"
        private const val IDENTITY_KEY_ALIAS = "mobile_egress_identity_storage_v1"
        private const val PREFERENCES = "mobile_egress_secure_identity"
        private const val IDENTITY = "identity"
        private const val FORMAT_VERSION = "v1"
        private const val TRANSFORMATION = "AES/GCM/NoPadding"
        private val ASSOCIATED_DATA = "mobile-egress-agent-identity-v1".encodeToByteArray()
    }
}
