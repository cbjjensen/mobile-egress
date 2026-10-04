package com.mobileegress.agent.direct

import com.mobileegress.agent.pairing.PairingBundleParser
import java.net.URI
import java.nio.ByteBuffer
import java.nio.charset.CodingErrorAction
import java.nio.charset.StandardCharsets
import java.security.Signature
import java.security.interfaces.ECPublicKey
import java.time.Instant
import java.util.Base64
import java.util.UUID
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull

@Serializable private data class SignedEndpoint(val version: Int, val type: String, val payload: String, val signature: String)
object DirectBundles {
    val json = Json { ignoreUnknownKeys = false; isLenient = false; coerceInputValues = false }
    fun decode(encoded: String): ByteArray {
        if (encoded.length > 87_384 || !Regex("^[A-Za-z0-9_-]+$").matches(encoded)) throw DirectException("invalid_bundle")
        val bytes = runCatching { Base64.getUrlDecoder().decode(encoded) }.getOrElse { throw DirectException("invalid_bundle") }
        if (bytes.size > 65_536 || Base64.getUrlEncoder().withoutPadding().encodeToString(bytes) != encoded) throw DirectException("invalid_bundle")
        return bytes
    }
    fun text(bytes: ByteArray): String = try {
        StandardCharsets.UTF_8.newDecoder().onMalformedInput(CodingErrorAction.REPORT)
            .onUnmappableCharacter(CodingErrorAction.REPORT).decode(ByteBuffer.wrap(bytes)).toString()
    } catch (_: Exception) { throw DirectException("invalid_utf8") }
    fun strict(raw: String): String {
        try { json.parseToJsonElement(raw); DuplicateKeyGuard(raw).check() } catch (_: Exception) { throw DirectException("invalid_json") }
        return raw
    }
    fun type(encoded: String): String = try {
        json.parseToJsonElement(strict(text(decode(encoded)))).jsonObject["type"]?.jsonPrimitive?.content
            ?: throw DirectException("legacy_pairing_rejected")
    } catch (e: DirectException) { throw e } catch (_: Exception) { throw DirectException("invalid_bundle") }
    fun invitation(encoded: String, now: Instant = Instant.now()): DirectInvitation {
        if (type(encoded) != "mobile-egress-direct-invitation") throw DirectException("legacy_pairing_rejected")
        val value = try { json.decodeFromString<DirectInvitation>(strict(text(decode(encoded)))) }
        catch (_: Exception) { throw DirectException("invalid_invitation") }
        if (value.version != 2 || value.role != "agent" || !uuid(value.clientId) || !uuid(value.invitationId) ||
            value.displayName.isBlank() || value.displayName.length > 128 || value.displayName.any { it.isISOControl() } ||
            runCatching { decode(value.capability).size }.getOrDefault(0) != 32 ||
            !runCatching { Instant.parse(value.expiresAt).isAfter(now) && value.expiresAt.endsWith("Z") }.getOrDefault(false)) {
            throw DirectException("invalid_or_expired_invitation")
        }
        val ca = try { PairingBundleParser.parseCaCertificate(value.caCertificatePem, now) }
        catch (_: Exception) { throw DirectException("invalid_trust") }
        if ((ca.publicKey as? ECPublicKey)?.params?.curve?.field?.fieldSize != 256) throw DirectException("invalid_trust")
        return value.copy(endpoint = origin(value.endpoint))
    }
    fun endpoint(encoded: String, record: DirectRecord, now: Instant = Instant.now()): DirectEndpointPayload {
        val wrapper = try { json.decodeFromString<SignedEndpoint>(strict(text(decode(encoded)))) }
        catch (_: Exception) { throw DirectException("invalid_endpoint_update") }
        if (wrapper.version != 2 || wrapper.type != "mobile-egress-direct-endpoint-update") throw DirectException("invalid_endpoint_update")
        val payload = decode(wrapper.payload)
        val ca = try { PairingBundleParser.parseCaCertificate(record.caCertificatePem, now) }
        catch (_: Exception) { throw DirectException("invalid_trust") }
        val verified = runCatching {
            Signature.getInstance("SHA256withECDSA").run {
                initVerify(ca.publicKey)
                update("MobileEgress-Direct-Endpoint-v2\n".toByteArray(StandardCharsets.UTF_8))
                update(payload)
                verify(decode(wrapper.signature))
            }
        }.getOrDefault(false)
        if (!verified) throw DirectException("invalid_update_signature")
        val result = try { json.decodeFromString<DirectEndpointPayload>(strict(text(payload))) }
        catch (_: Exception) { throw DirectException("invalid_endpoint_update") }
        // Verify the signed bytes above before normalizing a retained Client spelling.
        val endpoint = origin(result.endpoint)
        if (result.clientId != record.clientId || result.pairingId != record.pairingId || result.generation <= 0 ||
            result.generation < record.generation ||
            (result.generation == record.generation && endpoint != origin(record.endpoint))) throw DirectException("stale_endpoint_update")
        return result.copy(endpoint = endpoint)
    }
    fun origin(value: String): String {
        val uri = runCatching { URI(value) }.getOrElse { throw DirectException("invalid_endpoint") }
        if (uri.scheme != "https" || uri.host.isNullOrBlank() || uri.rawUserInfo != null || uri.rawQuery != null ||
            uri.rawFragment != null || (uri.rawPath ?: "") !in listOf("", "/") || uri.port == 0 || uri.port > 65535) {
            throw DirectException("invalid_endpoint")
        }
        // HttpUrl canonicalizes DNS case, numeric IPv6 and default/padded ports without DNS.
        return URI("https", null, uri.host, uri.port, null, null, null).toASCIIString()
            .toHttpUrlOrNull()?.toString()?.removeSuffix("/") ?: throw DirectException("invalid_endpoint")
    }
    fun uuid(value: String) = runCatching { UUID.fromString(value).toString() == value.lowercase() && value.length == 36 }.getOrDefault(false)
}

/** JSON validation precedes this scanner; it adds duplicate-key rejection at every depth. */
private class DuplicateKeyGuard(private val raw: String) {
    private var at = 0
    fun check() { value(); whitespace(); require(at == raw.length) }
    private fun whitespace() { while (at < raw.length && raw[at].isWhitespace()) at++ }
    private fun string(): String {
        val start = at++
        while (at < raw.length) {
            val c = raw[at++]
            if (c == '\\') at++ else if (c == '"') return DirectBundles.json.decodeFromString(raw.substring(start, at))
        }
        error("invalid")
    }
    private fun value(depth: Int = 0) {
        require(depth < 32)
        whitespace()
        when (raw[at]) {
            '{' -> {
                at++; whitespace(); val keys = HashSet<String>()
                if (raw[at] == '}') { at++; return }
                while (true) {
                    whitespace(); require(keys.add(string())); whitespace(); require(raw[at++] == ':')
                    value(depth + 1); whitespace()
                    if (raw[at++] == '}') break
                }
            }
            '[' -> {
                at++; whitespace()
                if (raw[at] == ']') { at++; return }
                while (true) { value(depth + 1); whitespace(); if (raw[at++] == ']') break }
            }
            '"' -> string()
            else -> while (at < raw.length && raw[at] !in ",]}" && !raw[at].isWhitespace()) at++
        }
    }
}
