package com.mobileegress.agent.pairing

import android.net.Network
import com.mobileegress.agent.security.AgentIdentity
import com.mobileegress.agent.security.DeviceKey
import com.mobileegress.agent.security.PinnedTls
import java.time.Instant
import java.util.Date
import kotlinx.serialization.Serializable
import kotlinx.serialization.SerializationException
import kotlinx.serialization.json.Json
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody

class EnrollmentException(message: String, cause: Throwable? = null) : Exception(message, cause)

class EnrollmentClient {
    private val json = Json {
        ignoreUnknownKeys = false
        explicitNulls = true
        isLenient = false
        coerceInputValues = false
    }

    fun enroll(
        bundle: PairingBundle,
        network: Network,
        deviceKey: DeviceKey,
        csrPem: String,
    ): AgentIdentity {
        val trustManager = PinnedTls.trustManager(bundle.caCertificate)
        val client = PinnedTls.clientBuilder(network, trustManager)
            .callTimeout(30, java.util.concurrent.TimeUnit.SECONDS)
            .build()
        try {
            val payload = json.encodeToString(
                EnrollmentRequest(bundle.capability, bundle.role, csrPem),
            ).toRequestBody(JSON_MEDIA_TYPE)
            val request = Request.Builder()
                .url(bundle.relayOrigin + ENROLLMENT_PATH)
                .post(payload)
                .build()
            val response = try {
                client.newCall(request).execute()
            } catch (error: Exception) {
                throw EnrollmentException("Pinned relay enrollment connection failed", error)
            }
            response.use {
                if (it.code != 201) {
                    throw EnrollmentException("Relay rejected Agent enrollment")
                }
                val raw = readLimitedBody(it.body?.source())
                val result = try {
                    json.decodeFromString<EnrollmentResponse>(raw.decodeToString())
                } catch (error: SerializationException) {
                    throw EnrollmentException("Relay returned an invalid enrollment response", error)
                }
                return verifyResult(bundle, deviceKey, result)
            }
        } finally {
            client.connectionPool.evictAll()
            client.dispatcher.executorService.shutdown()
        }
    }

    private fun verifyResult(
        bundle: PairingBundle,
        deviceKey: DeviceKey,
        response: EnrollmentResponse,
    ): AgentIdentity {
        if (response.role != "agent" || !SERIAL.matches(response.serial)) {
            throw EnrollmentException("Relay returned the wrong Agent identity")
        }
        val returnedCa = try {
            PairingBundleParser.parseCaCertificate(response.caCertificatePem)
        } catch (error: PairingBundleException) {
            throw EnrollmentException("Relay returned an invalid CA", error)
        }
        if (!returnedCa.encoded.contentEquals(bundle.caCertificate.encoded)) {
            throw EnrollmentException("Relay returned a CA different from the pairing bundle")
        }
        val chain = try {
            PinnedTls.parseCertificateChain(response.certificatePem)
        } catch (error: Exception) {
            throw EnrollmentException("Relay returned an invalid Agent certificate chain", error)
        }
        val leaf = chain.first()
        try {
            leaf.checkValidity(Date.from(Instant.now()))
            leaf.verify(bundle.caCertificate.publicKey)
        } catch (error: Exception) {
            throw EnrollmentException("Agent certificate does not verify against the pairing CA", error)
        }
        if (!leaf.publicKey.encoded.contentEquals(deviceKey.publicKey.encoded)) {
            throw EnrollmentException("Agent certificate does not match the AndroidKeyStore key")
        }
        if (!chain.any { it.encoded.contentEquals(bundle.caCertificate.encoded) }) {
            throw EnrollmentException("Agent certificate chain omits the pairing CA")
        }
        if (!leaf.extendedKeyUsage.orEmpty().contains(CLIENT_AUTH_OID)) {
            throw EnrollmentException("Agent certificate is not valid for client authentication")
        }
        val certificateSerial = leaf.serialNumber.toString(16).uppercase()
        if (response.serial.uppercase() != certificateSerial) {
            throw EnrollmentException("Agent certificate serial does not match the response")
        }
        return AgentIdentity(
            relayOrigin = bundle.relayOrigin,
            role = "agent",
            serial = certificateSerial,
            keyAlias = deviceKey.alias,
            certificatePem = response.certificatePem,
            caCertificatePem = response.caCertificatePem,
        )
    }

    private fun readLimitedBody(source: okio.BufferedSource?): ByteArray {
        if (source == null) throw EnrollmentException("Relay returned an empty enrollment response")
        val bytes = source.readByteArray(MAX_CONTROL_BYTES + 1L)
        if (bytes.size > MAX_CONTROL_BYTES) {
            throw EnrollmentException("Relay enrollment response is too large")
        }
        return bytes
    }

    companion object {
        private const val ENROLLMENT_PATH = "/v1/enroll"
        private const val MAX_CONTROL_BYTES = 256 * 1024
        private const val CLIENT_AUTH_OID = "1.3.6.1.5.5.7.3.2"
        private val JSON_MEDIA_TYPE = "application/json".toMediaType()
        private val SERIAL = Regex("^[0-9A-Fa-f]{1,64}$")
    }
}

@Serializable
private data class EnrollmentRequest(
    val code: String,
    val role: String,
    val csrPem: String,
)

@Serializable
private data class EnrollmentResponse(
    val certificatePem: String,
    val caCertificatePem: String,
    val serial: String,
    val role: String,
)
