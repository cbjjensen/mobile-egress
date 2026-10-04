package com.mobileegress.agent.session
import org.junit.Assert.*
import org.junit.Test

class PhoneBudgetTest {
    @Test fun requiredControlsAreBoundedAcrossPeersAndRefundAfterAbort() {
        val budget = SharedFrameBudget(1, 32)
        val a = OutboundMailbox(sharedControlBudget = budget)
        val b = OutboundMailbox(sharedControlBudget = budget)
        assertTrue(a.offerRequiredControl(byteArrayOf(1), null) {})
        var saturated = false
        assertFalse(b.offerRequiredControl(byteArrayOf(2), null) { saturated = true })
        assertTrue(saturated)
        a.close()
        assertTrue(b.offerRequiredControl(byteArrayOf(3), null) {})
        b.close()
        assertEquals(0 to 0L, budget.snapshot())
    }
    @Test fun twoMailboxesShareBudgetIncludingTransportDebt() {
        val budget = SharedFrameBudget(2, 10)
        val a = OutboundMailbox(sharedDataBudget = budget)
        val b = OutboundMailbox(sharedDataBudget = budget)
        assertTrue(a.offerData("same", ByteArray(6)))
        val frame = a.poll()!!
        a.emit(frame, retainUntilDrained = true) { true }
        assertFalse(b.offerData("same", ByteArray(5)))
        assertTrue(b.offerData("same", ByteArray(4)))
        assertEquals(2, budget.snapshot().first)
        a.cancelStream("same")
        assertEquals(2, budget.snapshot().first)
        a.drained(frame)
        assertEquals(1, budget.snapshot().first)
        a.close(); b.close()
        assertEquals(0 to 0L, budget.snapshot())
    }
    @Test fun busyPeerCannotReenterAheadOfWaitingPeerAndCancellationUnblocksNext() {
        val gate = PeerReadGate()
        gate.register("a") {}; gate.register("b") {}; gate.register("c") {}
        assertTrue(gate.enter("a"))
        assertFalse(gate.enter("b"))
        assertFalse(gate.enter("c"))
        gate.leave("a")
        assertFalse(gate.enter("a"))
        gate.remove("b")
        assertTrue(gate.enter("c"))
        gate.leave("c")
        assertTrue(gate.enter("a"))
        gate.leave("a")
        gate.remove("a"); gate.remove("c")
    }
}
