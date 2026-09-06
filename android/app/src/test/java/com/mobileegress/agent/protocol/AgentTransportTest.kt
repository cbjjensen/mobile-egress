package com.mobileegress.agent.protocol

import org.junit.Assert.*
import org.junit.Test

class AgentTransportTest {
    private val fixture = byteArrayOf(2, 4, 0, 3, 65, 95, 45, 0, -1)

    @Test fun `old relay keeps legacy encoding and rejects unsolicited binary`() {
        val transport = AgentTransport()
        assertEquals('{'.code.toByte(), transport.encodeData("A_-", byteArrayOf(0, -1))[0])
        assertThrows(ProtocolException::class.java) { transport.parseInbound(fixture) }
        transport.parseInbound(WireProtocol.encode("ping"))
        assertFalse(transport.negotiated)
    }

    @Test fun `capability ping enables exact raw fixture and still accepts legacy data`() {
        val transport = negotiated()
        assertArrayEquals(fixture, transport.encodeData("A_-", byteArrayOf(0, -1)))
        val raw = transport.parseInbound(fixture)
        assertEquals("A_-", raw.streamId)
        assertEquals("", raw.payload)
        assertArrayEquals(byteArrayOf(0, -1), raw.decodePayload())
        assertArrayEquals(byteArrayOf(9), transport.parseInbound(WireProtocol.encode("data", "s", byteArrayOf(9))).decodePayload())
    }

    @Test fun `rejects malformed binary headers ids and payload bounds`() {
        val transport = negotiated()
        listOf(byteArrayOf(2), byteArrayOf(2, 3, 0, 1, 65), byteArrayOf(2, 4, 0, 0),
            byteArrayOf(2, 4, 0, 2, 65), byteArrayOf(2, 4, 0, 1, -1),
            byteArrayOf(2, 4, 0, 1, 46), byteArrayOf(2, 4, 0, -127) + ByteArray(129) { 65 },
            byteArrayOf(2, 4, 0, 1, 65) + ByteArray(32769)).forEach {
            assertThrows(ProtocolException::class.java) { transport.parseInbound(it) }
        }
        assertEquals(32768, transport.parseInbound(byteArrayOf(2, 4, 0, 1, 65) + ByteArray(32768)).decodePayload().size)
        assertEquals(0, transport.parseInbound(byteArrayOf(2, 4, 0, 1, 65)).decodePayload().size)
        assertThrows(ProtocolException::class.java) { transport.encodeData("bad.id", byteArrayOf()) }
        assertThrows(ProtocolException::class.java) { transport.encodeData("s", ByteArray(32769)) }
    }

    @Test fun `enhanced targets require negotiation and a bounded nonempty list`() {
        val envelope = open("""{"ip":"1.1.1.1","port":443,"ips":["1.1.1.1","2606:4700:4700::1111"]}""")
        assertThrows(ProtocolException::class.java) { WireProtocol.parseOpen(envelope) }
        assertEquals(listOf("1.1.1.1", "2606:4700:4700::1111"), WireProtocol.parseOpen(envelope, true).ips)
        listOf("[]", "null",
            "[\"1.1.1.1\",\"1.1.1.2\",\"1.1.1.3\",\"1.1.1.4\",\"1.1.1.5\",\"1.1.1.6\",\"1.1.1.7\",\"1.1.1.8\",\"1.1.1.9\"]").forEach {
            assertThrows(ProtocolException::class.java) { WireProtocol.parseOpen(open("""{"ip":"1.1.1.1","port":443,"ips":$it}"""), true) }
        }
    }

    private fun open(payload: String) = WireProtocol.parseAgentInbound(WireProtocol.encode("open", "s", payload.encodeToByteArray()))
    private fun negotiated() = AgentTransport().also {
        it.parseInbound(WireProtocol.encode("ping", payload = "mobile-egress.transport.v2".encodeToByteArray()))
        assertTrue(it.negotiated)
    }
}
