package com.mobileegress.agent.migration

import com.mobileegress.agent.pairing.AgentIdentityPersistence
import com.mobileegress.agent.pairing.PairingBundleException
import com.mobileegress.agent.pairing.PairingBundleParser
import com.mobileegress.agent.security.AgentIdentity
import java.net.URI
import java.nio.ByteBuffer
import java.nio.charset.CodingErrorAction
import java.nio.charset.StandardCharsets
import java.time.Instant
import java.time.format.DateTimeParseException
import java.util.Base64
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.SerializationException
import kotlinx.serialization.json.Json

data class EndpointMigration(
    val version: Int,
    val type: String,
    val relayOrigin: String,
    val caCertificatePem: String,
    val capability: String,
    val expiresAt: Instant,
)

class EndpointMigrationException(message: String, cause: Throwable? = null) :
    IllegalArgumentException(message, cause)

fun interface EndpointMigrationDecoder {
    fun parse(input: String): EndpointMigration
}

class EndpointMigrationParser(private val now: () -> Instant = Instant::now) : EndpointMigrationDecoder {
    private val json = Json {
        ignoreUnknownKeys = false
        isLenient = false
        explicitNulls = true
        coerceInputValues = false
        allowTrailingComma = false
    }

    override fun parse(input: String): EndpointMigration {
        val encoded = input.trim()
        if (encoded.isEmpty() || encoded.length > MAX_ENCODED_BYTES || !BASE64URL.matches(encoded)) {
            throw EndpointMigrationException("Migration QR is not unpadded base64url")
        }
        val decoded = try {
            Base64.getUrlDecoder().decode(encoded)
        } catch (error: IllegalArgumentException) {
            throw EndpointMigrationException("Migration QR is not valid base64url", error)
        }
        val raw = try {
            StandardCharsets.UTF_8.newDecoder()
                .onMalformedInput(CodingErrorAction.REPORT)
                .onUnmappableCharacter(CodingErrorAction.REPORT)
                .decode(ByteBuffer.wrap(decoded)).toString()
        } catch (error: Exception) {
            throw EndpointMigrationException("Migration QR is not UTF-8", error)
        }
        val wire = try {
            json.decodeFromString<EndpointMigrationWire>(raw)
        } catch (error: SerializationException) {
            throw EndpointMigrationException("Migration QR is not strict version 1 JSON", error)
        } catch (error: IllegalArgumentException) {
            throw EndpointMigrationException("Migration QR is not strict version 1 JSON", error)
        }
        if (wire.version != VERSION || wire.type != TYPE) {
            throw EndpointMigrationException("Migration QR has the wrong type or version")
        }
        val expiresAt = try {
            Instant.parse(wire.expiresAt)
        } catch (error: DateTimeParseException) {
            throw EndpointMigrationException("Migration expiry is invalid", error)
        }
        if (!expiresAt.isAfter(now())) throw EndpointMigrationException("Migration QR has expired")
        if (wire.capability.isBlank() || wire.capability.length > MAX_CAPABILITY_BYTES) {
            throw EndpointMigrationException("Migration capability is missing or too large")
        }
        val relayOrigin = relayOrigin(wire.relayUrl)
        try {
            PairingBundleParser.parseCaCertificate(wire.caCertificatePem, now())
        } catch (error: PairingBundleException) {
            throw EndpointMigrationException("Migration CA is invalid", error)
        }
        return EndpointMigration(
            wire.version, wire.type, relayOrigin, wire.caCertificatePem, wire.capability, expiresAt,
        )
    }

    fun recognizes(input: String): Boolean = runCatching { parse(input) }.isSuccess

    private fun relayOrigin(value: String): String {
        val uri = try {
            URI(value.trim())
        } catch (error: Exception) {
            throw EndpointMigrationException("Migration relay URL must be an HTTPS origin", error)
        }
        val allowedPath = uri.rawPath.isNullOrEmpty() || uri.rawPath == "/"
        if (
            uri.scheme != "https" || uri.host.isNullOrBlank() || uri.rawUserInfo != null ||
            uri.rawQuery != null || uri.rawFragment != null || !allowedPath || uri.port == 0 || uri.port > 65_535
        ) {
            throw EndpointMigrationException("Migration relay URL must be an HTTPS origin")
        }
        return URI("https", null, uri.host, uri.port, null, null, null).toASCIIString()
    }

    companion object {
        const val VERSION = 1
        const val TYPE = "agent-endpoint-migration"
        private const val MAX_ENCODED_BYTES = 512 * 1024
        private const val MAX_CAPABILITY_BYTES = 4 * 1024
        private val BASE64URL = Regex("^[A-Za-z0-9_-]+$")
    }
}

fun interface EndpointMigrationPerformer {
    suspend fun consume(migration: EndpointMigration, identity: AgentIdentity): String
}

class EndpointMigrationRepository(
    private val decoder: EndpointMigrationDecoder,
    private val identityPersistence: AgentIdentityPersistence,
    private val performer: EndpointMigrationPerformer,
) {
    suspend fun migrate(encoded: String): AgentIdentity = withContext(Dispatchers.IO) {
        val migration = decoder.parse(encoded)
        val identity = identityPersistence.load()
            ?: throw EndpointMigrationException("An existing Agent identity is required")
        val migrationCA = try {
            PairingBundleParser.parseCaCertificate(migration.caCertificatePem)
        } catch (error: PairingBundleException) {
            throw EndpointMigrationException("Migration CA is invalid", error)
        }
        val identityCA = try {
            PairingBundleParser.parseCaCertificate(identity.caCertificatePem)
        } catch (error: PairingBundleException) {
            throw EndpointMigrationException("Stored Agent CA is invalid", error)
        }
        if (!migrationCA.encoded.contentEquals(identityCA.encoded)) {
            throw EndpointMigrationException("Migration QR belongs to a different relay")
        }
        val consumedOrigin = performer.consume(migration, identity)
        if (consumedOrigin != migration.relayOrigin) {
            throw EndpointMigrationException("Relay confirmed a different endpoint")
        }
        identity.copy(relayOrigin = migration.relayOrigin).also(identityPersistence::save)
    }
}

@Serializable
private data class EndpointMigrationWire(
    val version: Int,
    val type: String,
    @SerialName("relayUrl") val relayUrl: String,
    @SerialName("caCertificatePem") val caCertificatePem: String,
    val capability: String,
    val expiresAt: String,
)
