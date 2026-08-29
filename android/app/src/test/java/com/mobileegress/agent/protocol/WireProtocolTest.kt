package com.mobileegress.agent.protocol

import java.util.Base64
import org.junit.Assert.assertEquals
import org.junit.Assert.assertThrows
import org.junit.Test

class WireProtocolTest {
    @Test
    fun `parses the relay agent open payload`() {
        val payload = Base64.getUrlEncoder().withoutPadding()
            .encodeToString("""{"ip":"1.1.1.1","port":443}""".encodeToByteArray())
        val raw = """{"version":1,"type":"open","streamId":"opaque_stream-1","payload":"$payload"}"""

        val envelope = WireProtocol.parseAgentInbound(raw.encodeToByteArray())
        val target = WireProtocol.parseOpen(envelope)

        assertEquals("1.1.1.1", target.ip)
        assertEquals(443, target.port)
    }

    @Test
    fun `rejects role incompatible malformed and oversized frames`() {
        listOf(
            """{"version":2,"type":"ping","streamId":"","payload":""}""".encodeToByteArray(),
            """{"version":1,"type":"opened","streamId":"opaque_stream-1","payload":""}""".encodeToByteArray(),
            """{"version":1,"type":"data","streamId":"","payload":""}""".encodeToByteArray(),
            """{"version":1,"type":"data","streamId":"stream","payload":"padded=="}""".encodeToByteArray(),
            ByteArray(WireProtocol.MAX_WEBSOCKET_MESSAGE_BYTES + 1),
        ).forEach { raw ->
            assertThrows(ProtocolException::class.java) { WireProtocol.parseAgentInbound(raw) }
        }
    }

    @Test
    fun `round trips opaque stream data without inspecting it`() {
        val payload = byteArrayOf(0, 1, 2, -1)

        val encoded = WireProtocol.encode("data", "opaque_stream-1", payload)
        val parsed = WireProtocol.parseAgentInbound(encoded)

        assertEquals(payload.toList(), parsed.decodePayload().toList())
    }
}
