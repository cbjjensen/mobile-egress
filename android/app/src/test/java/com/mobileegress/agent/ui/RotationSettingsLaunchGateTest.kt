package com.mobileegress.agent.ui

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class RotationSettingsLaunchGateTest {
    @Test
    fun `each rotation attempt opens settings only once`() {
        val gate = RotationSettingsLaunchGate()

        assertTrue(gate.consume(10))
        assertFalse(gate.consume(10))
        assertTrue(gate.consume(11))
        assertFalse(gate.consume(11))
    }
}
