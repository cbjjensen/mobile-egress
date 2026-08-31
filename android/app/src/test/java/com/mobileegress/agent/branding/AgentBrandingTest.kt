package com.mobileegress.agent.branding

import org.junit.Assert.assertEquals
import org.junit.Test

class AgentBrandingTest {
    @Test
    fun `android user-facing brand includes ZFNF`() {
        assertEquals("ZF", AgentBranding.appMark)
        assertEquals("ZFNF Mobile Egress", AgentBranding.displayName)
        assertEquals("ZFNF MOBILE EGRESS", AgentBranding.headerTitle)
        assertEquals("ZFNF Mobile Egress Agent", AgentBranding.agentName)
        assertEquals("ZFNF Mobile Egress status", AgentBranding.statusClipboardLabel)
    }
}
