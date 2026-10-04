package com.mobileegress.agent.security

import android.content.Context
import android.content.SharedPreferences
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import com.mobileegress.agent.pairing.AgentIdentityPersistence
import java.security.KeyStore
import java.util.Base64
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import com.mobileegress.agent.direct.DirectPersistence
import com.mobileegress.agent.direct.DirectRegistryState
import com.mobileegress.agent.direct.DirectRemovalGate
import java.io.File

@Serializable
private data class DirectCredentialArchive(
    val version: Int = 1, val registry: String, val legacyRecovery: String? = null, val legacyRetired: Boolean = false,
)

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

class SecureIdentityStore internal constructor(
    private val preferences: SharedPreferences,
    override val removalGate: DirectRemovalGate,
    private val directFile: DurableCredentialFile,
    private val encryptionKey: () -> SecretKey,
) : AgentIdentityPersistence, DirectPersistence {
    constructor(context: Context) : this(
        context.getSharedPreferences(PREFERENCES, Context.MODE_PRIVATE), sharedGate(context),
        sharedFile(context), ::androidEncryptionKey,
    )
    private val json = Json { ignoreUnknownKeys = false }
    private var cachedGeneration: Long? = null
    private var cachedState: DirectRegistryState? = null

    override fun snapshot(): DirectRegistryState = synchronized(DIRECT_LOCK) { readDirect() }

    override fun change(transform: (DirectRegistryState) -> DirectRegistryState): DirectRegistryState =
        synchronized(DIRECT_LOCK) {
            val changed = transform(readDirect())
            require(changed.version == 2 && changed.records.size <= 10)
            require(changed.records.map { it.clientId }.distinct().size == changed.records.size)
            writeDirect(changed)
            changed
        }

    private fun readDirect(): DirectRegistryState {
        val generation = directFile.committedGeneration
        if (generation != null && generation == cachedGeneration) cachedState?.let { return it }
        try {
            val stored = readArchive().registry
            val pieces = stored.split(':')
            require(pieces.size == 3 && pieces[0] == "v2")
            val clear = decryptIdentityPayload(EncryptedIdentityPayload(
                Base64.getUrlDecoder().decode(pieces[1]), Base64.getUrlDecoder().decode(pieces[2]),
            ), encryptionKey())
            return json.decodeFromString<DirectRegistryState>(clear.decodeToString()).also {
                require(it.version == 2 && it.records.size <= 10 && it.records.map { r -> r.clientId }.distinct().size == it.records.size)
                cachedState = it
                cachedGeneration = directFile.committedGeneration
            }
        } catch (error: Exception) { throw CredentialStoreException("Direct Client storage unavailable", error) }
    }

    private fun readArchive(): DirectCredentialArchive {
        directFile.read()?.let {
            val archive = json.decodeFromString<DirectCredentialArchive>(it.decodeToString()).also { archive -> require(archive.version == 1) }
            return retireLegacy(archive)
        }
        // Import existing ciphertext without exposing it as active state until the new file is
        // durably committed. Recovery ciphertext is archived before retiring the old active keys.
        val legacy = preferences.getString("legacy_recovery", null) ?: preferences.getString(IDENTITY, null)
        val archive = DirectCredentialArchive(registry = preferences.getString("direct_registry", null)
            ?: encodeDirect(DirectRegistryState(migrationRequired = legacy != null)), legacyRecovery = legacy)
        directFile.write(json.encodeToString(archive).encodeToByteArray())
        return retireLegacy(archive)
    }

    private fun retireLegacy(archive: DirectCredentialArchive): DirectCredentialArchive {
        if (archive.legacyRetired) return archive
        // commit(false) may already remove the keys from memory. Never use that absence as proof
        // of retirement: force a new preference generation on every retry, then check commit.
        val retired = preferences.edit().remove(IDENTITY).remove("direct_registry")
            .putString("direct_retirement_generation", java.util.UUID.randomUUID().toString()).commit()
        if (!retired) throw CredentialStoreException("Could not retire legacy active credentials")
        return archive.copy(legacyRetired = true).also {
            directFile.write(json.encodeToString(it).encodeToByteArray())
        }
    }

    private fun encodeDirect(state: DirectRegistryState): String {
        val encrypted = encryptIdentityPayload(json.encodeToString(state).encodeToByteArray(), encryptionKey())
        val encoder = Base64.getUrlEncoder().withoutPadding()
        return "v2:" + encoder.encodeToString(encrypted.iv) + ":" + encoder.encodeToString(encrypted.ciphertext)
    }

    private fun writeDirect(state: DirectRegistryState) {
        try {
            val previous = readArchive()
            directFile.write(json.encodeToString(previous.copy(registry = encodeDirect(state))).encodeToByteArray())
            cachedState = state
            cachedGeneration = directFile.committedGeneration
            com.mobileegress.agent.direct.DirectRegistrySignals.changed()
        } catch (error: Exception) { throw CredentialStoreException("Direct Client storage unavailable", error) }
    }

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

    companion object {
        private fun sharedFile(context: Context) = synchronized(DIRECT_LOCK) {
            sharedDirectFile ?: DurableCredentialFile(File(context.noBackupFilesDir, "direct-credentials-v2.json"))
                .also { sharedDirectFile = it }
        }
        private fun sharedGate(context: Context) = synchronized(REMOVAL_LOCK) {
            sharedRemovalGate ?: run {
                val stops = context.getSharedPreferences("mobile_egress_direct_stops", Context.MODE_PRIVATE)
                DirectRemovalGate(stops.getStringSet("pending_removals", emptySet())!!.toSet()) { ids ->
                    if (!stops.edit().putStringSet("pending_removals", ids).commit()) {
                        throw CredentialStoreException("Could not preserve stopped Clients")
                    }
                }.also { sharedRemovalGate = it }
            }
        }
        private fun androidEncryptionKey(): SecretKey {
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

        private val REMOVAL_LOCK = Any()
        private var sharedRemovalGate: DirectRemovalGate? = null
        private val DIRECT_LOCK = Any()
        private var sharedDirectFile: DurableCredentialFile? = null
        private const val ANDROID_KEY_STORE = "AndroidKeyStore"
        private const val IDENTITY_KEY_ALIAS = "mobile_egress_identity_storage_v1"
        private const val PREFERENCES = "mobile_egress_secure_identity"
        private const val IDENTITY = "identity"
        private const val FORMAT_VERSION = "v1"
    }
}
