package com.mobileegress.agent.security

import org.junit.Assert.assertEquals
import org.junit.Test

class DeviceKeyStorePolicyTest {
    @Test
    fun `device keys permit raw TLS and SHA-256 signatures`() {
        assertEquals(listOf("NONE", "SHA-256"), deviceKeyDigests().toList())
    }
}
