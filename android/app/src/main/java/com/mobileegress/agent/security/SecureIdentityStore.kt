package com.mobileegress.agent.security

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import com.mobileegress.agent.pairing.AgentIdentityPersistence
import java.security.KeyStore
import java.util.Base64
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
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

class SecureIdentityStore(context: Context) : AgentIdentityPersistence {
    private val preferences = context.getSharedPreferences(PREFERENCES, Context.MODE_PRIVATE)
    private val json = Json { ignoreUnknownKeys = false }

    @Synchronized
    override fun load(): AgentIdentity? {
        val stored = preferences.getString(IDENTITY, null) ?: return null
        return try {
            val pieces = stored.split(':')
            if (pieces.size != 3 || pieces[0] != FORMAT_VERSION) throw IllegalArgumentException()
            val clear = decryptIdentityPayload(
                EncryptedIdentityPayload(
                    Base64.getUrlDecoder().decode(pieces[1]),
                    Base64.getUrlDecoder().decode(pieces[2]),
                ),
                encryptionKey(),
            )
            json.decodeFromString<AgentIdentity>(clear.decodeToString())
                .takeIf { it.role == "agent" && it.serial.isNotBlank() && it.keyAlias.isNotBlank() }
                ?: throw IllegalArgumentException()
        } catch (error: Exception) {
            throw CredentialStoreException("Stored Agent identity is unavailable", error)
        }
    }

    @Synchronized
    override fun save(identity: AgentIdentity) {
        require(identity.role == "agent")
        try {
            val encrypted = encryptIdentityPayload(
                json.encodeToString(identity).encodeToByteArray(),
                encryptionKey(),
            )
            val encoder = Base64.getUrlEncoder().withoutPadding()
            val value = "$FORMAT_VERSION:${encoder.encodeToString(encrypted.iv)}:${encoder.encodeToString(encrypted.ciphertext)}"
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
    }
}
