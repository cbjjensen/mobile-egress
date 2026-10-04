package com.mobileegress.agent.direct
import com.mobileegress.agent.status.ErrorClass
import org.junit.Assert.*
import org.junit.Test

class PeerRuntimeTest {
    @Test fun staleSessionCallbacksCannotResetReplacementOrAnotherClient() {
        val tracker = PeerRuntimeTracker()
        val old = tracker.begin("a")
        val b = tracker.begin("b")
        tracker.update("b", b) { it.copy(connected = true, streams = 4) }
        val replacement = tracker.begin("a")
        tracker.update("a", replacement) { it.copy(connected = true, streams = 2) }
        assertFalse(tracker.update("a", old) { it.copy(connected = false, streams = 0, error = ErrorClass.RelayUnavailable) })
        assertEquals(6, tracker.snapshot().sumOf { it.streams })
        tracker.remove("a")
        assertFalse(tracker.update("a", replacement) { it.copy(connected = true) })
        assertEquals(4, tracker.snapshot().single().streams)
    }
}
