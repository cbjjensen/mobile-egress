package com.mobileegress.agent.session

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class StreamAdmissionTest {
    @Test
    fun `production limits expose expanded bounded Android lanes`() {
        assertEquals(256, AgentCapacity.MAX_STREAMS)
        assertEquals(512, AgentCapacity.OUTBOUND_CONTROL_CAPACITY)
        assertEquals(32, AgentCapacity.OUTBOUND_PER_STREAM_DATA_CAPACITY)
        assertEquals(8_192, AgentCapacity.OUTBOUND_DATA_CAPACITY)
        assertEquals(64 * 1024 * 1024, AgentCapacity.OUTBOUND_DATA_BYTE_CAPACITY)
        assertEquals(32, AgentCapacity.TARGET_INBOUND_PER_STREAM_CAPACITY)
        assertEquals(8_192, AgentCapacity.TARGET_INBOUND_SESSION_FRAME_CAPACITY)
        assertEquals(64 * 1024 * 1024, AgentCapacity.TARGET_INBOUND_SESSION_BYTE_CAPACITY)
        assertEquals(8_192, AgentCapacity.REACTOR_DATA_COMMAND_CAPACITY)
        assertEquals(1_024, AgentCapacity.REACTOR_CONTROL_RESERVE_CAPACITY)
        assertEquals(9_216, AgentCapacity.REACTOR_COMMAND_CAPACITY)
        assertEquals(512, AgentCapacity.REACTOR_COMMANDS_PER_CYCLE)
        assertEquals(1_024, AgentCapacity.RETAINED_STREAM_CAPACITY)
    }

    @Test
    fun `admits at most two hundred fifty six unique agent streams`() {
        val admission = StreamAdmission(AgentCapacity.MAX_STREAMS)

        repeat(256) { index -> assertTrue(admission.tryReserve("stream-$index")) }

        assertFalse(admission.tryReserve("stream-256"))
        assertFalse(admission.tryReserve("stream-0"))
        assertEquals(256, admission.size)
    }

    @Test
    fun `released slots can be reused and clear closes the session view`() {
        val admission = StreamAdmission(2)
        admission.tryReserve("stream-1")
        admission.tryReserve("stream-2")

        admission.release("stream-1")
        assertTrue(admission.tryReserve("stream-3"))
        assertEquals(setOf("stream-2", "stream-3"), admission.clear())
        assertEquals(0, admission.size)
    }
}
