package com.mobileegress.agent.session

import org.junit.Assert.assertEquals
import org.junit.Test

class AgentSessionUrlTest {
    @Test
    fun `websocket request uses the HTTPS relay session URL`() {
        val url = agentSessionUrl("https://relay.example:8443")

        assertEquals("https", url.scheme)
        assertEquals("relay.example", url.host)
        assertEquals(8443, url.port)
        assertEquals("/v1/session", url.encodedPath)
    }
}
