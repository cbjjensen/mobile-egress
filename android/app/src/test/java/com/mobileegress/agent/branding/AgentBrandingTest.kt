package com.mobileegress.agent.branding

import org.junit.Assert.assertEquals
import org.junit.Test

class AgentBrandingTest {
    @Test
    fun `android user-facing brand includes ZFNF`() {
        assertEquals("ZFNF Mobile Egress", AgentBranding.displayName)
        assertEquals("ZFNF Mobile Egress Agent", AgentBranding.agentName)
        assertEquals("ZFNF Mobile Egress status", AgentBranding.statusClipboardLabel)
    }
}
