package com.mobileegress.agent.session

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class OutboundMailboxTest {
    @Test
    fun `failed data enqueue still schedules terminal close ahead of buffered data`() {
        val mailbox = OutboundMailbox(controlCapacity = 1, dataCapacity = 2)
        val closingStreamData = byteArrayOf(1)
        val otherStreamData = byteArrayOf(2)
        val rejectedData = byteArrayOf(3)
        val terminalClose = byteArrayOf(4)

        assertTrue(mailbox.offerData("closing-stream", closingStreamData))
        assertTrue(mailbox.offerData("other-stream", otherStreamData))
        assertFalse(mailbox.offerData("closing-stream", rejectedData))
        mailbox.blockAndDiscardData("closing-stream")
        assertFalse(mailbox.offerData("closing-stream", byteArrayOf(9)))
        assertTrue(mailbox.offerRequiredControl(terminalClose) {})

        assertEquals(terminalClose.toList(), mailbox.poll()!!.toList())
        assertEquals(otherStreamData.toList(), mailbox.poll()!!.toList())
    }

    @Test
    fun `saturated terminal control path requires the session to close`() {
        val mailbox = OutboundMailbox(controlCapacity = 1, dataCapacity = 1)
        var sessionClosed = false
        assertTrue(mailbox.offerRequiredControl(byteArrayOf(1)) {})

        val queued = mailbox.offerRequiredControl(byteArrayOf(2)) { sessionClosed = true }

        assertFalse(queued)
        assertTrue(sessionClosed)
    }
}
