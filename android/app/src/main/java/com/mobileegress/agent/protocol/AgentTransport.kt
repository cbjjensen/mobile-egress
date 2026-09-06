package com.mobileegress.agent.protocol

/** Per-WebSocket capability state. Outbound data may also be produced by the target reactor. */
class AgentTransport {
    @Volatile var negotiated: Boolean = false
        private set

    fun parseInbound(raw: ByteArray): WireEnvelope {
        if (raw.firstOrNull() == 2.toByte()) {
            if (!negotiated) throw ProtocolException("Binary data before negotiation")
            if (raw.size < 4 || raw[1] != 4.toByte()) throw ProtocolException("Invalid binary header")
            val idLength = ((raw[2].toInt() and 255) shl 8) or (raw[3].toInt() and 255)
            if (idLength !in 1..128 || raw.size < 4 + idLength ||
                raw.size - 4 - idLength > WireProtocol.MAX_DATA_PAYLOAD_BYTES
            ) throw ProtocolException("Invalid binary frame length")
            val id = raw.copyOfRange(4, 4 + idLength).decodeToString()
            if (!STREAM_ID.matches(id)) throw ProtocolException("Invalid stream ID")
            return WireEnvelope(1, "data", id, "", raw.copyOfRange(4 + idLength, raw.size))
        }
        return WireProtocol.parseAgentInbound(raw).also {
            if (it.type == "ping" && it.decodePayload().contentEquals(CAPABILITY)) negotiated = true
        }
    }

    fun encodeData(streamId: String, payload: ByteArray): ByteArray {
        if (!negotiated) return WireProtocol.encode("data", streamId, payload)
        if (!STREAM_ID.matches(streamId) || payload.size > WireProtocol.MAX_DATA_PAYLOAD_BYTES) {
            throw ProtocolException("Invalid binary data")
        }
        val id = streamId.encodeToByteArray()
        return ByteArray(4 + id.size + payload.size).also {
            it[0] = 2
            it[1] = 4
            it[2] = (id.size ushr 8).toByte()
            it[3] = id.size.toByte()
            id.copyInto(it, 4)
            payload.copyInto(it, 4 + id.size)
        }
    }

    private companion object {
        val STREAM_ID = Regex("^[A-Za-z0-9_-]{1,128}$")
        val CAPABILITY = "mobile-egress.transport.v2".encodeToByteArray()
    }
}
