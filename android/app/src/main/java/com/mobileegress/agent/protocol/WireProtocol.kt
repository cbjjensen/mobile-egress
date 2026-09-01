package com.mobileegress.agent.protocol

import java.util.Base64
import kotlinx.serialization.Serializable
import kotlinx.serialization.SerializationException
import kotlinx.serialization.json.Json

class ProtocolException(message: String) : Exception(message)

@Serializable
data class WireEnvelope(
    val version: Int,
    val type: String,
    val streamId: String,
    val payload: String,
) {
    fun decodePayload(): ByteArray = WireProtocol.decodePayload(payload)
}

@Serializable
data class AgentOpenTarget(val ip: String, val port: Int)

object WireProtocol {
    const val MAX_WEBSOCKET_MESSAGE_BYTES = 2 * 1024 * 1024
    const val MAX_PAYLOAD_BYTES = 1024 * 1024
    const val MAX_DATA_PAYLOAD_BYTES = 32 * 1024
    private val STREAM_ID = Regex("^[A-Za-z0-9_-]{1,128}$")
    private val BASE64URL = Regex("^[A-Za-z0-9_-]*$")
    private val ALL_TYPES = setOf("open", "opened", "rejected", "data", "close", "ping", "pong")
    private val AGENT_INBOUND_TYPES = setOf("open", "data", "close", "ping", "pong")
    private val json = Json {
        ignoreUnknownKeys = false
        isLenient = false
        explicitNulls = true
        coerceInputValues = false
        allowTrailingComma = false
    }

    fun parseAgentInbound(raw: ByteArray): WireEnvelope {
        if (raw.size > MAX_WEBSOCKET_MESSAGE_BYTES) throw ProtocolException("WebSocket frame is too large")
        val envelope = try {
            json.decodeFromString<WireEnvelope>(raw.decodeToString(throwOnInvalidSequence = true))
        } catch (_: SerializationException) {
            throw ProtocolException("Invalid v1 envelope")
        } catch (_: Exception) {
            throw ProtocolException("Invalid v1 envelope")
        }
        validate(envelope)
        if (envelope.type !in AGENT_INBOUND_TYPES) throw ProtocolException("Role-incompatible envelope")
        return envelope
    }

    fun parseOpen(envelope: WireEnvelope): AgentOpenTarget {
        if (envelope.type != "open") throw ProtocolException("Expected open envelope")
        return try {
            json.decodeFromString<AgentOpenTarget>(envelope.decodePayload().decodeToString(throwOnInvalidSequence = true))
        } catch (_: Exception) {
            throw ProtocolException("Invalid Agent open payload")
        }
    }

    fun encode(type: String, streamId: String = "", payload: ByteArray = byteArrayOf()): ByteArray {
        val envelope = WireEnvelope(
            version = 1,
            type = type,
            streamId = streamId,
            payload = Base64.getUrlEncoder().withoutPadding().encodeToString(payload),
        )
        validate(envelope)
        return json.encodeToString(envelope).encodeToByteArray()
    }

    fun decodePayload(value: String): ByteArray = decodePayload(value, MAX_PAYLOAD_BYTES)

    private fun decodePayload(value: String, maximumBytes: Int): ByteArray {
        if (!BASE64URL.matches(value)) throw ProtocolException("Payload is not unpadded base64url")
        if ((value.length.toLong() * 3L) / 4L > maximumBytes) {
            throw ProtocolException("Payload is too large")
        }
        val decoded = try {
            Base64.getUrlDecoder().decode(value)
        } catch (_: IllegalArgumentException) {
            throw ProtocolException("Payload is not valid base64url")
        }
        if (decoded.size > maximumBytes) throw ProtocolException("Payload is too large")
        return decoded
    }

    fun finiteErrorCode(value: String): ByteArray {
        if (value !in ERROR_CODES) throw ProtocolException("Unknown protocol error code")
        return value.encodeToByteArray()
    }

    private fun validate(envelope: WireEnvelope) {
        if (envelope.version != 1 || envelope.type !in ALL_TYPES) throw ProtocolException("Invalid v1 envelope")
        val keepalive = envelope.type == "ping" || envelope.type == "pong"
        if (keepalive && envelope.streamId.isNotEmpty()) throw ProtocolException("Keepalive stream ID must be empty")
        if (!keepalive && !STREAM_ID.matches(envelope.streamId)) throw ProtocolException("Invalid stream ID")
        val payloadLimit = if (envelope.type == "data") MAX_DATA_PAYLOAD_BYTES else MAX_PAYLOAD_BYTES
        decodePayload(envelope.payload, payloadLimit)
    }

    private val ERROR_CODES = setOf(
        "agent_stream_limit",
        "agent_unavailable",
        "client_closed",
        "client_stream_limit",
        "dns_failure",
        "idle_timeout",
        "invalid_target",
        "opening_timeout",
        "policy_denied",
        "protocol_error",
        "revoked",
        "session_closed",
        "stream_in_use",
        "stream_not_found",
        "target_closed",
        "target_failure",
    )
}
