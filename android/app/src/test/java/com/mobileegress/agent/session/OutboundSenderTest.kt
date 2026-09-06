package com.mobileegress.agent.session

import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.Job
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Assert.*
import org.junit.Test

@OptIn(ExperimentalCoroutinesApi::class)
class OutboundSenderTest {
    @Test
    fun `reusing stream ID retains old SDK debt against per stream bound`() {
        val mailbox = OutboundMailbox(perStreamDataCapacity = 2)
        val sdk = StalledSdk()
        val sender = OutboundSender(mailbox, sdk::send, sdk::queueSize, 8, 4)
        assertTrue(mailbox.offerData("reused", ByteArray(4) { 1 }))
        sender.pump()
        mailbox.cancelStream("reused")
        mailbox.allowData("reused")
        assertTrue(mailbox.offerData("reused", ByteArray(4) { 2 }))
        assertFalse(mailbox.offerData("reused", ByteArray(4) { 3 }))
        assertTrue(mailbox.offerData("peer", ByteArray(4) { 4 }))
        sdk.queued = 0
        sender.pump()
        assertTrue(mailbox.offerData("reused", ByteArray(4) { 3 }))
        sender.close()
        assertEquals(OutboundMailboxSnapshot(0, 0), mailbox.snapshot())
    }
    @Test
    fun `SDK buffered controls retain mailbox control slots until drain`() {
        val mailbox = OutboundMailbox(controlCapacity = 2)
        val sdk = StalledSdk()
        val sender = OutboundSender(mailbox, sdk::send, sdk::queueSize)
        assertTrue(mailbox.offerRequiredControl(byteArrayOf(1), null) {})
        assertTrue(mailbox.offerRequiredControl(byteArrayOf(2), null) {})
        sender.pump()
        assertFalse(mailbox.offerRequiredControl(byteArrayOf(3), null) {})
        assertFalse(mailbox.offerRequiredControlAfterData("stream", byteArrayOf(4)) {})
        sdk.queued = 1
        sender.pump()
        assertTrue(mailbox.offerRequiredControl(byteArrayOf(3), null) {})
        sender.pump()
        assertFalse(mailbox.offerRequiredControl(byteArrayOf(4), null) {})
        sender.close()
    }
    @Test
    fun `continuous SDK drain does not postpone coroutine cancellation`() = runTest {
        val mailbox = OutboundMailbox()
        var writes = 0
        lateinit var job: Job
        val sender = OutboundSender(mailbox, {
            writes++
            if (writes == 1) job.cancel()
            true
        }, { 0L })
        mailbox.offerData("stream", byteArrayOf(1))
        mailbox.offerData("stream", byteArrayOf(2))
        job = launch { sender.run() }
        runCurrent()
        job.join()
        assertEquals(1, writes)
        assertEquals(OutboundMailboxSnapshot(0, 0), mailbox.snapshot())
    }
    @Test
    fun `default sdk window bounds data and reserves room for required controls`() {
        val mailbox = OutboundMailbox()
        val sdk = StalledSdk()
        val sender = OutboundSender(mailbox, sdk::send, sdk::queueSize)
        repeat(40) { mailbox.offerData("stream-$it", ByteArray(16 * 1024) { 1 }) }
        sender.pump()
        assertEquals(448L * 1024, sdk.queued)
        assertEquals(OutboundMailboxSnapshot(40, 640L * 1024), mailbox.snapshot())
        repeat(4) { mailbox.offerRequiredControl(ByteArray(16 * 1024) { 9 }, null) {} }
        sender.pump()
        assertEquals(512L * 1024, sdk.queued)
        assertEquals(32, sdk.sent.size)
        sender.close()
        assertEquals(OutboundMailboxSnapshot(0, 0), mailbox.snapshot())
    }

    @Test
    fun `enqueue callback cancellation cannot refund SDK accepted bytes`() {
        val mailbox = OutboundMailbox()
        val sdk = StalledSdk()
        val sender = OutboundSender(mailbox, { bytes ->
            sdk.send(bytes).also { mailbox.cancelStream("stream") }
        }, sdk::queueSize, 8, 4)
        mailbox.offerData("stream", ByteArray(4) { 1 })
        sender.pump()
        assertEquals(OutboundMailboxSnapshot(1, 4), mailbox.snapshot())
        sdk.queued = 0
        sender.pump()
        assertEquals(OutboundMailboxSnapshot(0, 0), mailbox.snapshot())
        sender.close()
    }

    @Test
    fun `before emission veto never reaches SDK or fires emitted callback`() {
        val mailbox = OutboundMailbox()
        val sdk = StalledSdk()
        val sender = OutboundSender(mailbox, sdk::send, sdk::queueSize, 8, 4)
        var emitted = false
        mailbox.offerRequiredControlAfterData("stream", byteArrayOf(1), { false }, { emitted = true }) {}
        sender.pump()
        assertEquals(emptyList<Int>(), sdk.sent)
        assertFalse(emitted)
        sender.close()
    }

    @Test
    fun `sdk accepted data stays charged through partial and full FIFO drain`() {
        val mailbox = OutboundMailbox(dataCapacity = 3, perStreamDataCapacity = 2, dataByteCapacity = 12)
        val sdk = StalledSdk()
        val sender = OutboundSender(mailbox, sdk::send, sdk::queueSize, 12, 4)
        assertTrue(mailbox.offerData("first", ByteArray(4) { 1 }))
        assertTrue(mailbox.offerData("peer", ByteArray(4) { 2 }))
        assertTrue(mailbox.offerData("first", ByteArray(4) { 3 }))
        assertTrue(sender.pump())
        assertEquals(listOf(1, 2), sdk.sent)
        assertEquals(OutboundMailboxSnapshot(3, 12), mailbox.snapshot())
        assertFalse(mailbox.offerData("peer", byteArrayOf(9)))

        sdk.queued = 6 // No complete frame yet: do not refund a partial frame.
        assertTrue(sender.pump())
        assertEquals(OutboundMailboxSnapshot(3, 12), mailbox.snapshot())
        sdk.queued = 4
        assertTrue(sender.pump())
        assertEquals(listOf(1, 2, 3), sdk.sent)
        assertEquals(OutboundMailboxSnapshot(2, 8), mailbox.snapshot())
        sdk.queued = 0
        assertTrue(sender.pump())
        assertEquals(OutboundMailboxSnapshot(0, 0), mailbox.snapshot())
        sender.close()
        assertEquals(OutboundMailboxSnapshot(0, 0), mailbox.snapshot())
    }

    @Test
    fun `saturated data leaves a control reserve and keeps queued streams fair`() {
        val mailbox = OutboundMailbox()
        val sdk = StalledSdk()
        val sender = OutboundSender(mailbox, sdk::send, sdk::queueSize, 12, 4)
        mailbox.offerData("busy", ByteArray(4) { 1 })
        mailbox.offerData("busy", ByteArray(4) { 2 })
        mailbox.offerData("peer", ByteArray(4) { 3 })
        sender.pump()
        assertEquals(listOf(1, 3), sdk.sent)
        mailbox.offerRequiredControl(ByteArray(4) { 9 }, null) {}
        sender.pump()
        assertEquals(listOf(1, 3, 9), sdk.sent)
        assertEquals(12L, sdk.queued)
        repeat(10) { sender.pump() }
        assertEquals(12L, sdk.queued)
        sdk.queued = 0
        sender.pump()
        assertEquals(listOf(1, 3, 9, 2), sdk.sent)
        sender.close()
    }

    @Test
    fun `normal close waits for data enqueue and invokes callbacks only once`() {
        val mailbox = OutboundMailbox()
        val sdk = StalledSdk()
        val sender = OutboundSender(mailbox, sdk::send, sdk::queueSize, 8, 4)
        var before = 0
        var emitted = 0
        mailbox.offerData("stream", ByteArray(4) { 1 })
        mailbox.offerData("stream", ByteArray(4) { 2 })
        mailbox.offerRequiredControlAfterData("stream", ByteArray(4) { 3 }, { before++; true }, { emitted++ }) {}
        sender.pump()
        assertEquals(listOf(1), sdk.sent)
        assertEquals(0, before)
        sdk.queued = 0
        sender.pump()
        assertEquals(listOf(1, 2, 3), sdk.sent)
        assertEquals(1, before)
        assertEquals(1, emitted)
        sender.close()
    }

    @Test
    fun `stream cancellation retains sdk debt while discarding unsent frames`() {
        val mailbox = OutboundMailbox()
        val sdk = StalledSdk()
        val sender = OutboundSender(mailbox, sdk::send, sdk::queueSize, 8, 4)
        mailbox.offerData("canceled", ByteArray(4) { 1 })
        mailbox.offerData("canceled", ByteArray(4) { 2 })
        sender.pump()
        mailbox.cancelStream("canceled")
        assertEquals(OutboundMailboxSnapshot(1, 4), mailbox.snapshot())
        mailbox.offerData("peer", ByteArray(4) { 3 })
        sender.pump()
        assertEquals(listOf(1), sdk.sent)
        sdk.queued = 0
        sender.pump()
        assertEquals(listOf(1, 3), sdk.sent)
        assertEquals(OutboundMailboxSnapshot(1, 4), mailbox.snapshot())
        sender.close()
        assertEquals(OutboundMailboxSnapshot(0, 0), mailbox.snapshot())
    }

    @Test
    fun `send rejection releases its reservation and shutdown refunds all work`() {
        val mailbox = OutboundMailbox()
        val sdk = StalledSdk()
        val sender = OutboundSender(mailbox, sdk::send, sdk::queueSize, 12, 4)
        mailbox.offerData("first", ByteArray(4) { 1 })
        sender.pump()
        mailbox.offerData("second", ByteArray(4) { 2 })
        sdk.accept = false
        assertFalse(sender.pump())
        assertEquals(OutboundMailboxSnapshot(1, 4), mailbox.snapshot())
        sender.close()
        sender.close()
        assertEquals(OutboundMailboxSnapshot(0, 0), mailbox.snapshot())
    }

    @Test
    fun `idle sender waits for mailbox signal and stalled sdk checks are cancellable`() = runTest {
        val mailbox = OutboundMailbox()
        val sdk = StalledSdk()
        val sender = OutboundSender(mailbox, sdk::send, sdk::queueSize, 8, 4)
        val job = launch { sender.run() }
        runCurrent()
        val idleReads = sdk.reads
        advanceTimeBy(100)
        runCurrent()
        assertEquals(idleReads, sdk.reads)
        mailbox.offerData("stream", ByteArray(4) { 1 })
        runCurrent()
        assertEquals(listOf(1), sdk.sent)
        sdk.queued = 0
        advanceTimeBy(2)
        runCurrent()
        assertEquals(OutboundMailboxSnapshot(0, 0), mailbox.snapshot())
        mailbox.offerData("stream", ByteArray(4) { 2 })
        runCurrent()
        job.cancelAndJoin()
        assertEquals(OutboundMailboxSnapshot(0, 0), mailbox.snapshot())
    }

    private class StalledSdk {
        var queued = 0L
        var accept = true
        var reads = 0
        val sent = mutableListOf<Int>()
        fun queueSize(): Long { reads++; return queued }
        fun send(bytes: ByteArray): Boolean {
            if (!accept) return false
            queued += bytes.size
            sent += bytes.first().toInt()
            return true
        }
    }
}
