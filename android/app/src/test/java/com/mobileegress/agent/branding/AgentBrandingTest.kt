package com.mobileegress.agent.branding

import org.junit.Assert.assertEquals
import org.junit.Test

class AgentBrandingTest {
    @Test
    fun `android user-facing brand uses Inevitable Mobile Relay`() {
        assertEquals("IMR", AgentBranding.appMark)
        assertEquals("Inevitable Mobile Relay", AgentBranding.displayName)
        assertEquals("INEVITABLE MOBILE RELAY", AgentBranding.headerTitle)
        assertEquals("Inevitable Mobile Relay Agent", AgentBranding.agentName)
        assertEquals("Inevitable Mobile Relay status", AgentBranding.statusClipboardLabel)
    }
}
