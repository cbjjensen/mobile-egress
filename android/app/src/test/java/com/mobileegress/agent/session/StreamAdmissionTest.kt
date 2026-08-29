package com.mobileegress.agent.session

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class StreamAdmissionTest {
    @Test
    fun `admits at most eight unique agent streams`() {
        val admission = StreamAdmission(8)

        repeat(8) { index -> assertTrue(admission.tryReserve("stream-$index")) }

        assertFalse(admission.tryReserve("stream-8"))
        assertFalse(admission.tryReserve("stream-0"))
        assertEquals(8, admission.size)
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
