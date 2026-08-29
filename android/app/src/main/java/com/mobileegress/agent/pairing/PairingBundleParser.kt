package com.mobileegress.agent.pairing

import java.io.ByteArrayInputStream
import java.net.URI
import java.nio.ByteBuffer
import java.nio.charset.CodingErrorAction
import java.nio.charset.StandardCharsets
import java.security.cert.CertificateFactory
import java.security.cert.X509Certificate
import java.time.Instant
import java.time.format.DateTimeParseException
import java.util.Base64
import java.util.Date
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.SerializationException
import kotlinx.serialization.json.Json

data class PairingBundle(
    val version: Int,
    val relayOrigin: String,
    val caCertificatePem: String,
    val caCertificate: X509Certificate,
    val capability: String,
    val role: String,
    val expiresAt: Instant,
)

class PairingBundleException(message: String) : IllegalArgumentException(message)

fun interface PairingDecoder {
    fun parse(input: String): PairingBundle
}

class PairingBundleParser(
    private val now: () -> Instant = Instant::now,
) : PairingDecoder {
    private val json = Json {
        ignoreUnknownKeys = false
        isLenient = false
        explicitNulls = true
        coerceInputValues = false
        allowTrailingComma = false
    }

    override fun parse(input: String): PairingBundle {
        val encoded = input.trim()
        if (encoded.isEmpty() || encoded.length > MAX_ENCODED_BYTES || !BASE64URL.matches(encoded)) {
            throw PairingBundleException("Pairing bundle is not unpadded base64url")
        }
        val decoded = try {
            Base64.getUrlDecoder().decode(encoded)
        } catch (_: IllegalArgumentException) {
            throw PairingBundleException("Pairing bundle is not valid base64url")
        }
        val rawJson = decodeUtf8(decoded)
        val wire = try {
            json.decodeFromString<PairingBundleWire>(rawJson)
        } catch (_: SerializationException) {
            throw PairingBundleException("Pairing bundle is not strict version 1 JSON")
        } catch (_: IllegalArgumentException) {
            throw PairingBundleException("Pairing bundle is not strict version 1 JSON")
        }

        if (wire.version != VERSION) {
            throw PairingBundleException("Unsupported pairing bundle version")
        }
        val relayOrigin = parseRelayOrigin(wire.relayUrl)
        if (wire.role != AGENT_ROLE) {
            throw PairingBundleException("Pairing bundle is not for an Agent")
        }
        if (wire.capability.isBlank() || wire.capability.length > MAX_CAPABILITY_BYTES) {
            throw PairingBundleException("Pairing capability is missing or too large")
        }
        val expiresAt = try {
            Instant.parse(wire.expiresAt)
        } catch (_: DateTimeParseException) {
            throw PairingBundleException("Pairing expiry is missing or invalid")
        }
        if (!expiresAt.isAfter(now())) {
            throw PairingBundleException("Pairing bundle has expired")
        }
        val caCertificate = parseCaCertificate(wire.caCertificatePem, now())
        return PairingBundle(
            version = wire.version,
            relayOrigin = relayOrigin,
            caCertificatePem = wire.caCertificatePem,
            caCertificate = caCertificate,
            capability = wire.capability,
            role = wire.role,
            expiresAt = expiresAt,
        )
    }

    private fun decodeUtf8(value: ByteArray): String = try {
        StandardCharsets.UTF_8.newDecoder()
            .onMalformedInput(CodingErrorAction.REPORT)
            .onUnmappableCharacter(CodingErrorAction.REPORT)
            .decode(ByteBuffer.wrap(value))
            .toString()
    } catch (_: Exception) {
        throw PairingBundleException("Pairing bundle JSON is not UTF-8")
    }

    private fun parseRelayOrigin(value: String): String {
        val uri = try {
            URI(value.trim())
        } catch (_: Exception) {
            throw PairingBundleException("Relay URL must be an HTTPS origin")
        }
        val allowedPath = uri.rawPath.isNullOrEmpty() || uri.rawPath == "/"
        if (
            uri.scheme != "https" ||
            uri.host.isNullOrBlank() ||
            uri.rawUserInfo != null ||
            uri.rawQuery != null ||
            uri.rawFragment != null ||
            !allowedPath ||
            uri.port == 0 ||
            uri.port > 65_535
        ) {
            throw PairingBundleException("Relay URL must be an HTTPS origin")
        }
        return URI("https", null, uri.host, uri.port, null, null, null).toASCIIString()
    }

    companion object {
        const val VERSION = 1
        const val AGENT_ROLE = "agent"
        private const val MAX_ENCODED_BYTES = 512 * 1024
        private const val MAX_CAPABILITY_BYTES = 4 * 1024
        private val BASE64URL = Regex("^[A-Za-z0-9_-]+$")

        fun parseCaCertificate(pem: String, at: Instant = Instant.now()): X509Certificate {
            if (pem.isBlank() || !SINGLE_CERTIFICATE_PEM.matches(pem)) {
                throw PairingBundleException("Pairing bundle CA is not one certificate")
            }
            val certificate = try {
                CertificateFactory.getInstance("X.509")
                    .generateCertificate(ByteArrayInputStream(pem.toByteArray(StandardCharsets.US_ASCII))) as X509Certificate
            } catch (_: Exception) {
                throw PairingBundleException("Pairing bundle CA is invalid")
            }
            try {
                certificate.checkValidity(Date.from(at))
            } catch (_: Exception) {
                throw PairingBundleException("Pairing bundle CA is not currently valid")
            }
            val keyUsage = certificate.keyUsage
            if (certificate.basicConstraints < 0 || keyUsage == null || keyUsage.size <= 5 || !keyUsage[5]) {
                throw PairingBundleException("Pairing bundle certificate is not a CA")
            }
            return certificate
        }

        private val SINGLE_CERTIFICATE_PEM = Regex(
            "\\A-----BEGIN CERTIFICATE-----\\r?\\n(?:[A-Za-z0-9+/]{1,64}\\r?\\n)*(?:[A-Za-z0-9+/]{1,62}==|[A-Za-z0-9+/]{1,63}=|[A-Za-z0-9+/]{1,64})\\r?\\n-----END CERTIFICATE-----\\r?\\n?\\z",
        )
    }
}

@Serializable
private data class PairingBundleWire(
    val version: Int,
    @SerialName("relayUrl") val relayUrl: String,
    @SerialName("caCertificatePem") val caCertificatePem: String,
    val capability: String,
    val role: String,
    val expiresAt: String,
)
