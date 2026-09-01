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

    @Test
    fun `relay failure diagnostic includes only failure type and HTTP status`() {
        val diagnostic = relayFailureDiagnostic(
            java.net.UnknownHostException("private relay hostname"),
            responseCode = 503,
        )

        assertEquals("UnknownHostException http=503", diagnostic)
    }
}
