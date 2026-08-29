package com.mobileegress.agent.session

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class OutboundMailboxTest {
    @Test
    fun `client close cancels pending normal eof without affecting another stream`() {
        val mailbox = OutboundMailbox(controlCapacity = 2, dataCapacity = 6, perStreamDataCapacity = 3)
        val closingFirst = byteArrayOf(1)
        val closingSecond = byteArrayOf(2)
        val closingTerminal = byteArrayOf(3)
        val activeFirst = byteArrayOf(4)
        val activeSecond = byteArrayOf(5)
        var canceledTerminalEmitted = false

        assertTrue(mailbox.offerData("closing-stream", closingFirst))
        assertTrue(mailbox.offerData("closing-stream", closingSecond))
        assertTrue(
            mailbox.offerRequiredControlAfterData(
                "closing-stream",
                closingTerminal,
                onEmitted = { canceledTerminalEmitted = true },
            ) {},
        )
        assertTrue(mailbox.offerData("active-stream", activeFirst))

        val claimedClosingFrame = requireNotNull(mailbox.poll())
        assertEquals(closingFirst.toList(), claimedClosingFrame.bytes.toList())
        assertTrue(mailbox.cancelGracefulStream("closing-stream"))
        var canceledFrameSent = false
        assertEquals(
            OutboundEmission.Canceled,
            mailbox.emit(claimedClosingFrame) {
                canceledFrameSent = true
                true
            },
        )
        assertFalse(canceledFrameSent)
        assertFalse(canceledTerminalEmitted)
        assertTrue(mailbox.offerData("active-stream", activeSecond))

        assertEquals(activeFirst.toList(), emitNext(mailbox).toList())
        assertEquals(activeSecond.toList(), emitNext(mailbox).toList())
        assertNull(mailbox.poll())
        assertFalse(canceledTerminalEmitted)
    }

    @Test
    fun `normal target eof writes every accepted chunk before close`() {
        val mailbox = OutboundMailbox(controlCapacity = 2, dataCapacity = 4, perStreamDataCapacity = 2)
        val firstChunk = byteArrayOf(1)
        val secondChunk = byteArrayOf(2)
        val terminalClose = byteArrayOf(3)
        var terminalEmitted = false

        assertTrue(mailbox.offerData("eof-stream", firstChunk))
        assertTrue(mailbox.offerData("eof-stream", secondChunk))
        assertTrue(
            mailbox.offerRequiredControlAfterData(
                "eof-stream",
                terminalClose,
                onEmitted = { terminalEmitted = true },
            ) {},
        )

        assertEquals(firstChunk.toList(), emitNext(mailbox).toList())
        assertEquals(secondChunk.toList(), emitNext(mailbox).toList())
        assertFalse(terminalEmitted)
        assertEquals(terminalClose.toList(), emitNext(mailbox).toList())
        assertTrue(terminalEmitted)
        assertFalse(mailbox.offerData("eof-stream", byteArrayOf(4)))
    }

    @Test
    fun `one stream reaching its data bound does not reject an unrelated stream`() {
        val mailbox = OutboundMailbox(controlCapacity = 1, dataCapacity = 4, perStreamDataCapacity = 2)

        assertTrue(mailbox.offerData("busy-stream", byteArrayOf(1)))
        assertTrue(mailbox.offerData("busy-stream", byteArrayOf(2)))
        assertFalse(mailbox.offerData("busy-stream", byteArrayOf(3)))
        assertTrue(mailbox.offerData("other-stream", byteArrayOf(4)))

        assertEquals(listOf(1.toByte()), pollBytes(mailbox).toList())
        assertEquals(listOf(4.toByte()), pollBytes(mailbox).toList())
        assertEquals(listOf(2.toByte()), pollBytes(mailbox).toList())
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

        assertEquals(terminalClose.toList(), pollBytes(mailbox).toList())
        assertEquals(otherStreamData.toList(), pollBytes(mailbox).toList())
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

    private fun emitNext(mailbox: OutboundMailbox): ByteArray {
        val frame = requireNotNull(mailbox.poll())
        assertEquals(OutboundEmission.Emitted, mailbox.emit(frame) { true })
        return frame.bytes
    }

    private fun pollBytes(mailbox: OutboundMailbox): ByteArray = requireNotNull(mailbox.poll()).bytes
}
