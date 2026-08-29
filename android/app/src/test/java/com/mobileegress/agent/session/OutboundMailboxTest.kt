package com.mobileegress.agent.session

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class OutboundMailboxTest {
    @Test
    fun `client close cancels claimed opened and pending eof frames while another stream remains usable`() {
        val mailbox = OutboundMailbox(controlCapacity = 4, dataCapacity = 4, perStreamDataCapacity = 2)
        val closingOpened = byteArrayOf(1)
        val closingData = byteArrayOf(2)
        val closingTerminal = byteArrayOf(3)
        val activeOpened = byteArrayOf(4)
        val activeData = byteArrayOf(5)
        var closingTerminalEmitted = false

        assertTrue(mailbox.offerRequiredControl(closingOpened, streamId = "closing-stream") {})
        val claimedOpened = requireNotNull(mailbox.poll())
        assertEquals(closingOpened.toList(), claimedOpened.bytes.toList())
        assertTrue(mailbox.offerData("closing-stream", closingData))
        assertTrue(
            mailbox.offerRequiredControlAfterData(
                "closing-stream",
                closingTerminal,
                onEmitted = { closingTerminalEmitted = true },
            ) {},
        )
        assertTrue(mailbox.offerRequiredControl(activeOpened, streamId = "active-stream") {})

        assertTrue(mailbox.cancelStream("closing-stream"))
        var canceledFrameSent = false
        assertEquals(
            OutboundEmission.Canceled,
            mailbox.emit(claimedOpened) {
                canceledFrameSent = true
                true
            },
        )
        assertFalse(canceledFrameSent)
        assertFalse(closingTerminalEmitted)

        assertEquals(activeOpened.toList(), emitNext(mailbox).toList())
        assertTrue(mailbox.offerData("active-stream", activeData))
        assertEquals(activeData.toList(), emitNext(mailbox).toList())
        assertNull(mailbox.poll())
        assertFalse(closingTerminalEmitted)
    }

    @Test
    fun `client close removes queued opened but leaves session control and another stream`() {
        val mailbox = OutboundMailbox(controlCapacity = 4, dataCapacity = 2, perStreamDataCapacity = 1)
        val closingOpened = byteArrayOf(1)
        val pong = byteArrayOf(2)
        val activeOpened = byteArrayOf(3)

        assertTrue(mailbox.offerRequiredControl(closingOpened, streamId = "closing-stream") {})
        assertTrue(mailbox.offerRequiredControl(pong, streamId = null) {})
        assertTrue(mailbox.offerRequiredControl(activeOpened, streamId = "active-stream") {})

        assertTrue(mailbox.cancelStream("closing-stream"))

        assertEquals(pong.toList(), emitNext(mailbox).toList())
        assertEquals(activeOpened.toList(), emitNext(mailbox).toList())
        assertNull(mailbox.poll())
    }

    @Test
    fun `client close suppresses a claimed rejected control`() {
        val mailbox = OutboundMailbox(controlCapacity = 2, dataCapacity = 1, perStreamDataCapacity = 1)
        val rejected = byteArrayOf(1)
        val activeControl = byteArrayOf(2)

        assertTrue(mailbox.offerRequiredControl(rejected, streamId = "rejected-stream") {})
        val claimedRejected = requireNotNull(mailbox.poll())
        assertTrue(mailbox.offerRequiredControl(activeControl, streamId = "active-stream") {})

        assertTrue(mailbox.cancelStream("rejected-stream"))
        var rejectedSent = false
        assertEquals(
            OutboundEmission.Canceled,
            mailbox.emit(claimedRejected) {
                rejectedSent = true
                true
            },
        )

        assertFalse(rejectedSent)
        assertEquals(activeControl.toList(), emitNext(mailbox).toList())
    }

    @Test
    fun `cancellation during sender does not relabel a genuinely emitted stream control`() {
        val mailbox = OutboundMailbox(controlCapacity = 1, dataCapacity = 1, perStreamDataCapacity = 1)
        val opened = byteArrayOf(1)
        var emitted = 0

        assertTrue(mailbox.offerRequiredControl(opened, streamId = "opened-stream") {})
        val claimedOpened = requireNotNull(mailbox.poll())
        assertEquals(
            OutboundEmission.Emitted,
            mailbox.emit(claimedOpened) {
                emitted += 1
                assertTrue(mailbox.cancelStream("opened-stream"))
                true
            },
        )

        assertEquals(1, emitted)
    }

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
        assertTrue(mailbox.cancelStream("closing-stream"))
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
        assertTrue(mailbox.offerRequiredControl(terminalClose, streamId = "closing-stream") {})

        assertEquals(terminalClose.toList(), pollBytes(mailbox).toList())
        assertEquals(otherStreamData.toList(), pollBytes(mailbox).toList())
    }

    @Test
    fun `saturated terminal control path requires the session to close`() {
        val mailbox = OutboundMailbox(controlCapacity = 1, dataCapacity = 1, perStreamDataCapacity = 1)
        var sessionClosed = false
        assertTrue(mailbox.offerRequiredControl(byteArrayOf(1), streamId = null) {})

        val queued = mailbox.offerRequiredControl(byteArrayOf(2), streamId = null) { sessionClosed = true }

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
