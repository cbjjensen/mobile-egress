package com.mobileegress.agent.session

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class OutboundMailboxTest {
    @Test
    fun `normal target eof writes every accepted chunk before close`() {
        val mailbox = OutboundMailbox(controlCapacity = 2, dataCapacity = 4, perStreamDataCapacity = 2)
        val firstChunk = byteArrayOf(1)
        val secondChunk = byteArrayOf(2)
        val terminalClose = byteArrayOf(3)

        assertTrue(mailbox.offerData("eof-stream", firstChunk))
        assertTrue(mailbox.offerData("eof-stream", secondChunk))
        assertTrue(mailbox.offerRequiredControlAfterData("eof-stream", terminalClose) {})

        assertEquals(firstChunk.toList(), mailbox.poll()!!.toList())
        assertEquals(secondChunk.toList(), mailbox.poll()!!.toList())
        assertEquals(terminalClose.toList(), mailbox.poll()!!.toList())
        assertFalse(mailbox.offerData("eof-stream", byteArrayOf(4)))
    }

    @Test
    fun `one stream reaching its data bound does not reject an unrelated stream`() {
        val mailbox = OutboundMailbox(controlCapacity = 1, dataCapacity = 4, perStreamDataCapacity = 2)

        assertTrue(mailbox.offerData("busy-stream", byteArrayOf(1)))
        assertTrue(mailbox.offerData("busy-stream", byteArrayOf(2)))
        assertFalse(mailbox.offerData("busy-stream", byteArrayOf(3)))
        assertTrue(mailbox.offerData("other-stream", byteArrayOf(4)))

        assertEquals(listOf(1.toByte()), mailbox.poll()!!.toList())
        assertEquals(listOf(4.toByte()), mailbox.poll()!!.toList())
        assertEquals(listOf(2.toByte()), mailbox.poll()!!.toList())
    }

    @Test
    fun `failed data enqueue still schedules terminal close ahead of buffered data`() {
        val mailbox = OutboundMailbox(controlCapacity = 1, dataCapacity = 2, perStreamDataCapacity = 2)
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
        val mailbox = OutboundMailbox(controlCapacity = 1, dataCapacity = 1, perStreamDataCapacity = 1)
        var sessionClosed = false
        assertTrue(mailbox.offerRequiredControl(byteArrayOf(1)) {})

        val queued = mailbox.offerRequiredControl(byteArrayOf(2)) { sessionClosed = true }

        assertFalse(queued)
        assertTrue(sessionClosed)
    }
}
