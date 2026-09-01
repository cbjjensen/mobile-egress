package com.mobileegress.agent.session

import com.mobileegress.agent.status.ErrorClass
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class TargetReactorPolicyTest {
    @Test
    fun `target eof drains accepted relay data before target closed`() {
        val action = TargetTerminalReason.TargetClosed.protocolAction()

        assertEquals("target_closed", action.code)
        assertEquals(ErrorClass.None, action.errorClass)
        assertTrue(action.drainOutboundData)
    }

    @Test
    fun `connect and io failure retain target failure`() {
        val action = TargetTerminalReason.TargetFailure.protocolAction()

        assertEquals("target_failure", action.code)
        assertEquals(ErrorClass.TargetConnect, action.errorClass)
        assertFalse(action.drainOutboundData)
    }

    @Test
    fun `pre connect setup failure rejects the unopened stream`() {
        val action = TargetTerminalReason.OpenSetupFailure.protocolAction()

        assertEquals("target_failure", action.code)
        assertEquals(ErrorClass.TargetConnect, action.errorClass)
        assertTrue(action.rejectUnopenedStream)
        assertFalse(action.drainOutboundData)
    }

    @Test
    fun `idle and saturation use their existing finite protocol codes`() {
        val idle = TargetTerminalReason.IdleTimeout.protocolAction()
        val saturation = TargetTerminalReason.Backpressure.protocolAction()

        assertEquals("idle_timeout", idle.code)
        assertEquals(ErrorClass.TargetConnect, idle.errorClass)
        assertEquals("agent_unavailable", saturation.code)
        assertEquals(ErrorClass.Backpressure, saturation.errorClass)
    }

    @Test
    fun `relay cancellation and reactor shutdown do not emit terminal frames`() {
        listOf(TargetTerminalReason.Canceled, TargetTerminalReason.Shutdown).forEach { reason ->
            val action = reason.protocolAction()
            assertNull(action.code)
            assertEquals(ErrorClass.None, action.errorClass)
            assertFalse(action.drainOutboundData)
        }
    }
}
