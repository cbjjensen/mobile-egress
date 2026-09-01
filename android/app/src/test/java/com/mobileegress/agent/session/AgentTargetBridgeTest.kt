package com.mobileegress.agent.session

import com.mobileegress.agent.protocol.WireProtocol
import com.mobileegress.agent.status.ErrorClass
import java.net.InetAddress
import java.net.InetSocketAddress
import java.net.ServerSocket
import java.util.Collections
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicInteger
import kotlin.concurrent.thread
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class AgentTargetBridgeTest {
    @Test
    fun `reactor construction is deferred and factory failure is session internal`() {
        val factoryCalls = AtomicInteger()
        val failures = Collections.synchronizedList(mutableListOf<ErrorClass>())
        val bridge = AgentTargetBridge(
            outbound = OutboundMailbox(),
            reactorFactory = {
                factoryCalls.incrementAndGet()
                throw IllegalStateException("selector unavailable")
            },
            onSessionFailure = failures::add,
        )

        assertEquals(0, factoryCalls.get())
        assertFalse(bridge.start())
        assertEquals(1, factoryCalls.get())
        assertEquals(listOf(ErrorClass.Internal), failures)
        assertTrue(bridge.shutdownAndAwait(1, TimeUnit.SECONDS))
    }

    @Test
    fun `required reactor and mailbox command overflow are session fatal`() {
        val reactorOverflow = Fixture(openResult = ReactorSubmitResult.SessionSaturated)
        reactorOverflow.bridge.open("reactor-full", targetAddress())
        assertEquals(listOf(ErrorClass.Backpressure), reactorOverflow.failures)

        val mailboxOverflow = Fixture(outbound = OutboundMailbox(controlCapacity = 1))
        mailboxOverflow.bridge.open("one", targetAddress())
        mailboxOverflow.bridge.open("two", targetAddress())
        val one = mailboxOverflow.reactor.opened.getValue("one")
        val two = mailboxOverflow.reactor.opened.getValue("two")
        mailboxOverflow.listener.onOpened("one", one)
        mailboxOverflow.listener.onOpened("two", two)

        assertEquals(listOf(ErrorClass.Backpressure), mailboxOverflow.failures)
    }

    @Test
    fun `target write saturation emits exactly one required agent unavailable close`() {
        val fixture = Fixture(maxStreams = 1)
        fixture.bridge.open("saturated", targetAddress())
        fixture.reactor.writeResults["saturated"] = ReactorSubmitResult.StreamSaturated

        fixture.bridge.routeData("saturated", byteArrayOf(1))

        assertEquals(
            listOf(
                "{\"version\":1,\"type\":\"close\",\"streamId\":\"saturated\"," +
                    "\"payload\":\"YWdlbnRfdW5hdmFpbGFibGU\"}",
            ),
            fixture.emittedFrames().map(ByteArray::decodeToString),
        )
        assertEquals(emptyList<ErrorClass>(), fixture.failures)
    }

    @Test
    fun `saturated stream release is exact once and its slot is reusable`() {
        val fixture = Fixture(maxStreams = 1)
        fixture.bridge.open("saturated", targetAddress())
        val firstToken = fixture.reactor.opened.getValue("saturated")
        fixture.reactor.writeResults["saturated"] = ReactorSubmitResult.StreamSaturated
        fixture.bridge.routeData("saturated", byteArrayOf(1))

        fixture.listener.onTerminal("saturated", firstToken, TargetTerminalReason.Backpressure)
        fixture.listener.onReleased("saturated", firstToken)
        fixture.listener.onReleased("saturated", firstToken)

        assertEquals(0, fixture.bridge.activeStreamCount)
        assertEquals(listOf(1, 0), fixture.status.activeCounts)
        fixture.bridge.open("replacement", targetAddress())
        val replacementToken = fixture.reactor.opened.getValue("replacement")
        assertNotEquals(firstToken, replacementToken)
        assertEquals(1, fixture.bridge.activeStreamCount)
        assertEquals(listOf(1, 0, 1), fixture.status.activeCounts)
        assertEquals(
            1,
            fixture.emittedFrames().count {
                it.decodeToString().contains("YWdlbnRfdW5hdmFpbGFibGU")
            },
        )
    }

    @Test
    fun `late saturated stream data is absorbed while an unrelated stream continues`() {
        val fixture = Fixture(maxStreams = 2)
        fixture.bridge.open("saturated", targetAddress())
        fixture.bridge.open("peer", targetAddress())
        val saturatedToken = fixture.reactor.opened.getValue("saturated")
        fixture.reactor.writeResults["saturated"] = ReactorSubmitResult.StreamSaturated
        fixture.bridge.routeData("saturated", byteArrayOf(1))

        fixture.bridge.routeData("saturated", byteArrayOf(2))
        fixture.bridge.routeData("saturated", byteArrayOf(3))
        fixture.bridge.routeData("peer", "first".encodeToByteArray())
        fixture.listener.onReleased("saturated", saturatedToken)
        fixture.bridge.routeData("saturated", "after-release".encodeToByteArray())
        fixture.bridge.routeData("peer", "second".encodeToByteArray())

        assertEquals(1, fixture.reactor.writes.count { it.streamId == "saturated" })
        assertEquals(
            listOf("first", "second"),
            fixture.reactor.writes
                .filter { it.streamId == "peer" }
                .map { it.payload.decodeToString() },
        )
        assertEquals(1, fixture.bridge.activeStreamCount)
        assertEquals(emptyList<ErrorClass>(), fixture.failures)
        assertEquals(
            1,
            fixture.emittedFrames().count {
                it.decodeToString().contains("YWdlbnRfdW5hdmFpbGFibGU")
            },
        )
    }

    @Test
    fun `late data after relay bound saturation release is absorbed before close emission`() {
        val fixture = Fixture(
            outbound = OutboundMailbox(dataCapacity = 1, perStreamDataCapacity = 1),
            maxStreams = 2,
        )
        fixture.bridge.open("saturated", targetAddress())
        fixture.bridge.open("peer", targetAddress())
        val token = fixture.reactor.opened.getValue("saturated")
        assertTrue(fixture.listener.onData("saturated", token, byteArrayOf(1)))
        assertFalse(fixture.listener.onData("saturated", token, byteArrayOf(2)))

        fixture.listener.onTerminal("saturated", token, TargetTerminalReason.Backpressure)
        fixture.listener.onReleased("saturated", token)
        fixture.bridge.routeData("saturated", "after-release".encodeToByteArray())
        fixture.bridge.routeData("peer", "continues".encodeToByteArray())

        assertEquals(
            listOf("continues"),
            fixture.reactor.writes.map { it.payload.decodeToString() },
        )
        assertEquals(1, fixture.bridge.activeStreamCount)
        assertEquals(emptyList<ErrorClass>(), fixture.failures)
        assertEquals(
            listOf(
                "{\"version\":1,\"type\":\"close\",\"streamId\":\"saturated\"," +
                    "\"payload\":\"YWdlbnRfdW5hdmFpbGFibGU\"}",
            ),
            fixture.emittedFrames().map(ByteArray::decodeToString),
        )
    }

    @Test
    fun `eof saturation atomically replaces target closed before release finalizes`() {
        val fixture = Fixture(maxStreams = 1)
        fixture.bridge.open("stream", targetAddress())
        val token = fixture.reactor.opened.getValue("stream")
        fixture.listener.onTerminal("stream", token, TargetTerminalReason.TargetClosed)
        val releaseDone = CountDownLatch(1)
        fixture.reactor.writeAction = { call ->
            Thread {
                fixture.listener.onReleased(call.streamId, call.correlationToken)
                releaseDone.countDown()
            }.start()
            releaseDone.await(100, TimeUnit.MILLISECONDS)
            ReactorSubmitResult.StreamSaturated
        }

        fixture.bridge.routeData("stream", byteArrayOf(1))

        assertTrue(releaseDone.await(2, TimeUnit.SECONDS))
        assertEquals(0, fixture.bridge.activeStreamCount)
        assertEquals(emptyList<ErrorClass>(), fixture.failures)
        assertEquals(
            listOf(
                "{\"version\":1,\"type\":\"close\",\"streamId\":\"stream\"," +
                    "\"payload\":\"YWdlbnRfdW5hdmFpbGFibGU\"}",
            ),
            fixture.emittedFrames().map(ByteArray::decodeToString),
        )
    }

    @Test
    fun `graceful close emission cannot overtake a crossing target write submission`() {
        val fixture = Fixture(maxStreams = 1)
        fixture.bridge.open("stream", targetAddress())
        val token = fixture.reactor.opened.getValue("stream")
        fixture.listener.onTerminal("stream", token, TargetTerminalReason.TargetClosed)
        val targetClosed = requireNotNull(fixture.mailbox.poll())
        val writeEntered = CountDownLatch(1)
        val allowWrite = CountDownLatch(1)
        fixture.reactor.writeAction = {
            writeEntered.countDown()
            allowWrite.await(2, TimeUnit.SECONDS)
            ReactorSubmitResult.Accepted
        }
        val routeFailure = arrayOfNulls<Throwable>(1)
        val emissionResult = arrayOfNulls<OutboundEmission>(1)
        val senderCalled = CountDownLatch(1)
        val router = thread(name = "graceful-crossing-write") {
            try {
                fixture.bridge.routeData("stream", "crossing".encodeToByteArray())
            } catch (error: Throwable) {
                routeFailure[0] = error
            }
        }
        assertTrue(writeEntered.await(2, TimeUnit.SECONDS))
        val emitter = thread(name = "graceful-close-emitter") {
            emissionResult[0] = fixture.mailbox.emit(targetClosed) {
                senderCalled.countDown()
                true
            }
        }

        try {
            waitUntil { senderCalled.count == 0L || emitter.state == Thread.State.BLOCKED }
            assertEquals(1L, senderCalled.count)
            assertEquals(1L, fixture.reactor.releaseCalled.count)
        } finally {
            allowWrite.countDown()
            router.join(2_000)
            emitter.join(2_000)
        }

        assertEquals(null, routeFailure[0])
        assertEquals(OutboundEmission.Emitted, emissionResult[0])
        assertTrue(senderCalled.await(2, TimeUnit.SECONDS))
        assertTrue(fixture.reactor.releaseCalled.await(2, TimeUnit.SECONDS))
        assertEquals(
            listOf("crossing"),
            fixture.reactor.writes.map { it.payload.decodeToString() },
        )
    }

    @Test
    fun `crossing saturation wins before target closed sender and emits one terminal`() {
        val fixture = Fixture(maxStreams = 1)
        fixture.bridge.open("stream", targetAddress())
        val token = fixture.reactor.opened.getValue("stream")
        fixture.listener.onTerminal("stream", token, TargetTerminalReason.TargetClosed)
        val targetClosed = requireNotNull(fixture.mailbox.poll())
        val writeEntered = CountDownLatch(1)
        val allowWrite = CountDownLatch(1)
        fixture.reactor.writeAction = {
            writeEntered.countDown()
            allowWrite.await(2, TimeUnit.SECONDS)
            ReactorSubmitResult.StreamSaturated
        }
        val sent = Collections.synchronizedList(mutableListOf<String>())
        val senderCalled = CountDownLatch(1)
        val routeFailure = arrayOfNulls<Throwable>(1)
        val emissionResult = arrayOfNulls<OutboundEmission>(1)
        val router = thread(name = "saturation-before-close-sender") {
            try {
                fixture.bridge.routeData("stream", byteArrayOf(1))
            } catch (error: Throwable) {
                routeFailure[0] = error
            }
        }
        assertTrue(writeEntered.await(2, TimeUnit.SECONDS))
        val emitter = thread(name = "target-closed-sender") {
            emissionResult[0] = fixture.mailbox.emit(targetClosed) { bytes ->
                senderCalled.countDown()
                sent += bytes.decodeToString()
                true
            }
        }

        try {
            waitUntil { senderCalled.count == 0L || emitter.state == Thread.State.BLOCKED }
            assertEquals(1L, senderCalled.count)
        } finally {
            allowWrite.countDown()
            router.join(2_000)
            emitter.join(2_000)
        }
        while (true) {
            val frame = fixture.mailbox.poll() ?: break
            fixture.mailbox.emit(frame) { bytes ->
                sent += bytes.decodeToString()
                true
            }
        }

        assertEquals(null, routeFailure[0])
        assertEquals(OutboundEmission.Canceled, emissionResult[0])
        assertEquals(
            listOf(
                "{\"version\":1,\"type\":\"close\",\"streamId\":\"stream\"," +
                    "\"payload\":\"YWdlbnRfdW5hdmFpbGFibGU\"}",
            ),
            sent,
        )
        assertEquals(emptyList<ErrorClass>(), fixture.failures)
    }

    @Test
    fun `target closed emission advances state before sender can route data`() {
        val fixture = Fixture(maxStreams = 1)
        fixture.bridge.open("stream", targetAddress())
        val token = fixture.reactor.opened.getValue("stream")
        fixture.listener.onTerminal("stream", token, TargetTerminalReason.TargetClosed)
        val targetClosed = requireNotNull(fixture.mailbox.poll())
        fixture.reactor.writeResults["stream"] = ReactorSubmitResult.StreamSaturated
        val sent = mutableListOf<String>()
        var routeFailure: Throwable? = null

        val result = fixture.mailbox.emit(targetClosed) { bytes ->
            sent += bytes.decodeToString()
            try {
                fixture.bridge.routeData("stream", byteArrayOf(1))
            } catch (error: Throwable) {
                routeFailure = error
            }
            true
        }
        while (true) {
            val frame = fixture.mailbox.poll() ?: break
            fixture.mailbox.emit(frame) { bytes ->
                sent += bytes.decodeToString()
                true
            }
        }

        assertEquals(OutboundEmission.Emitted, result)
        assertEquals(null, routeFailure)
        assertEquals(
            listOf(
                "{\"version\":1,\"type\":\"close\",\"streamId\":\"stream\"," +
                    "\"payload\":\"dGFyZ2V0X2Nsb3NlZA\"}",
            ),
            sent,
        )
        assertEquals(emptyList<FakeReactor.Write>(), fixture.reactor.writes)
        assertTrue(fixture.reactor.releaseCalled.await(2, TimeUnit.SECONDS))
        assertEquals(emptyList<ErrorClass>(), fixture.failures)
    }

    @Test
    fun `reactor terminal reservation removal cannot make correlated data session fatal`() {
        val fixture = Fixture(maxStreams = 1)
        fixture.bridge.open("stream", targetAddress())
        val token = fixture.reactor.opened.getValue("stream")
        val callbackStarted = CountDownLatch(1)
        val allowTerminal = CountDownLatch(1)
        val terminalDone = CountDownLatch(1)
        fixture.reactor.writeAction = {
            Thread {
                callbackStarted.countDown()
                allowTerminal.await(2, TimeUnit.SECONDS)
                fixture.listener.onTerminal("stream", token, TargetTerminalReason.TargetFailure)
                fixture.listener.onReleased("stream", token)
                terminalDone.countDown()
            }.start()
            assertTrue(callbackStarted.await(2, TimeUnit.SECONDS))
            ReactorSubmitResult.MissingOrClosed
        }
        var routeFailure: Throwable? = null

        try {
            fixture.bridge.routeData("stream", byteArrayOf(1))
        } catch (error: Throwable) {
            routeFailure = error
        } finally {
            allowTerminal.countDown()
        }

        assertEquals(null, routeFailure)
        assertTrue(terminalDone.await(2, TimeUnit.SECONDS))
        var unknownFailure: Throwable? = null
        try {
            fixture.bridge.routeData("never-opened", byteArrayOf(2))
        } catch (error: Throwable) {
            unknownFailure = error
        }
        assertTrue(unknownFailure is com.mobileegress.agent.protocol.ProtocolException)
        assertEquals(emptyList<ErrorClass>(), fixture.failures)
    }

    @Test
    fun `captured unopened terminal cannot reject a replacement generation`() {
        val fixture = Fixture(maxStreams = 1)
        fixture.bridge.open("same", targetAddress())
        val oldToken = fixture.reactor.opened.getValue("same")
        fixture.reactor.cancelResult = ReactorSubmitResult.MissingOrClosed
        val oldLockHeld = CountDownLatch(1)
        val performCancelAndReuse = CountDownLatch(1)
        val replacementOpened = CountDownLatch(1)
        fixture.reactor.writeAction = {
            oldLockHeld.countDown()
            performCancelAndReuse.await(2, TimeUnit.SECONDS)
            fixture.bridge.closeFromRelay("same")
            fixture.bridge.open("same", targetAddress())
            replacementOpened.countDown()
            ReactorSubmitResult.Accepted
        }
        val router = thread(name = "old-generation-lock-owner") {
            fixture.bridge.routeData("same", byteArrayOf(1))
        }
        assertTrue(oldLockHeld.await(2, TimeUnit.SECONDS))
        val terminal = thread(name = "captured-old-open-terminal") {
            fixture.listener.onTerminal("same", oldToken, TargetTerminalReason.OpenSetupFailure)
        }
        waitUntil { terminal.state == Thread.State.BLOCKED }

        performCancelAndReuse.countDown()
        assertTrue(replacementOpened.await(2, TimeUnit.SECONDS))
        router.join(2_000)
        terminal.join(2_000)
        fixture.reactor.writeAction = null
        val replacementToken = fixture.reactor.opened.getValue("same")

        assertNotEquals(oldToken, replacementToken)
        assertEquals(1, fixture.bridge.activeStreamCount)
        assertTrue(fixture.listener.onData("same", replacementToken, byteArrayOf(2)))
        fixture.bridge.routeData("same", byteArrayOf(3))
        assertEquals(
            listOf(WireProtocol.encode("data", "same", byteArrayOf(2)).toList()),
            fixture.emittedFrames().map(ByteArray::toList),
        )
        assertEquals(
            listOf(1, 3),
            fixture.reactor.writes.map { it.payload.single().toInt() },
        )
        assertEquals(emptyList<ErrorClass>(), fixture.failures)
    }

    @Test
    fun `checked target failure cannot mutate a replacement generation`() {
        val callbackChecked = CountDownLatch(1)
        val allowMailboxCommit = CountDownLatch(1)
        val pauseOnce = AtomicBoolean(true)
        val fixture = Fixture(
            maxStreams = 1,
            beforeMailboxCommit = {
                if (pauseOnce.compareAndSet(true, false)) {
                    callbackChecked.countDown()
                    allowMailboxCommit.await(2, TimeUnit.SECONDS)
                }
            },
        )
        fixture.bridge.open("same", targetAddress())
        val oldToken = fixture.reactor.opened.getValue("same")
        fixture.reactor.cancelResult = ReactorSubmitResult.MissingOrClosed
        val terminal = thread(name = "checked-old-target-failure") {
            fixture.listener.onTerminal("same", oldToken, TargetTerminalReason.TargetFailure)
        }
        assertTrue(callbackChecked.await(2, TimeUnit.SECONDS))
        val replacementOpened = CountDownLatch(1)
        val closeStarted = CountDownLatch(1)
        val raceTrace = Collections.synchronizedList(mutableListOf<String>())
        val closer = thread(name = "target-failure-close-and-reuse") {
            closeStarted.countDown()
            fixture.bridge.closeFromRelay("same")
            raceTrace += "after-close active=${fixture.bridge.activeStreamCount}"
            fixture.bridge.open("same", targetAddress())
            raceTrace += "after-open active=${fixture.bridge.activeStreamCount} token=${fixture.reactor.opened["same"]}"
            replacementOpened.countDown()
        }
        assertTrue(closeStarted.await(2, TimeUnit.SECONDS))
        waitUntil { replacementOpened.count == 0L || closer.state == Thread.State.BLOCKED }
        val replacementEscapedOldLock = replacementOpened.count == 0L

        allowMailboxCommit.countDown()
        terminal.join(2_000)
        closer.join(2_000)
        val replacementToken = fixture.reactor.opened.getValue("same")

        assertFalse(replacementEscapedOldLock)
        assertNotEquals("race=$raceTrace status=${fixture.status.activeCounts}", oldToken, replacementToken)
        assertEquals(1, fixture.bridge.activeStreamCount)
        assertTrue(fixture.listener.onData("same", replacementToken, byteArrayOf(2)))
        assertEquals(
            listOf(WireProtocol.encode("data", "same", byteArrayOf(2)).toList()),
            fixture.emittedFrames().map(ByteArray::toList),
        )
        assertEquals(listOf(ErrorClass.TargetConnect), fixture.status.errors)
        assertEquals(emptyList<ErrorClass>(), fixture.failures)
    }

    @Test
    fun `checked opened callback cannot enqueue control for a replacement generation`() {
        val callbackChecked = CountDownLatch(1)
        val allowMailboxCommit = CountDownLatch(1)
        val pauseOnce = AtomicBoolean(true)
        val fixture = Fixture(
            maxStreams = 1,
            beforeMailboxCommit = {
                if (pauseOnce.compareAndSet(true, false)) {
                    callbackChecked.countDown()
                    allowMailboxCommit.await(2, TimeUnit.SECONDS)
                }
            },
        )
        fixture.bridge.open("same", targetAddress())
        val oldToken = fixture.reactor.opened.getValue("same")
        fixture.reactor.cancelResult = ReactorSubmitResult.MissingOrClosed
        val opened = thread(name = "checked-old-opened") {
            fixture.listener.onOpened("same", oldToken)
        }
        assertTrue(callbackChecked.await(2, TimeUnit.SECONDS))
        val replacementOpened = CountDownLatch(1)
        val closeStarted = CountDownLatch(1)
        val closer = thread(name = "opened-close-and-reuse") {
            closeStarted.countDown()
            fixture.bridge.closeFromRelay("same")
            fixture.bridge.open("same", targetAddress())
            replacementOpened.countDown()
        }
        assertTrue(closeStarted.await(2, TimeUnit.SECONDS))
        waitUntil { replacementOpened.count == 0L || closer.state == Thread.State.BLOCKED }
        val replacementEscapedOldLock = replacementOpened.count == 0L

        allowMailboxCommit.countDown()
        opened.join(2_000)
        closer.join(2_000)

        assertFalse(replacementEscapedOldLock)
        assertNotEquals(oldToken, fixture.reactor.opened.getValue("same"))
        assertEquals(1, fixture.bridge.activeStreamCount)
        assertEquals(emptyList<ByteArray>(), fixture.emittedFrames())
        assertEquals(emptyList<ErrorClass>(), fixture.failures)
    }

    @Test
    fun `checked data callback cannot enqueue payload for a replacement generation`() {
        val callbackChecked = CountDownLatch(1)
        val allowMailboxCommit = CountDownLatch(1)
        val pauseOnce = AtomicBoolean(true)
        val fixture = Fixture(
            maxStreams = 1,
            beforeMailboxCommit = {
                if (pauseOnce.compareAndSet(true, false)) {
                    callbackChecked.countDown()
                    allowMailboxCommit.await(2, TimeUnit.SECONDS)
                }
            },
        )
        fixture.bridge.open("same", targetAddress())
        val oldToken = fixture.reactor.opened.getValue("same")
        fixture.reactor.cancelResult = ReactorSubmitResult.MissingOrClosed
        val oldAccepted = AtomicBoolean(false)
        val data = thread(name = "checked-old-data") {
            oldAccepted.set(fixture.listener.onData("same", oldToken, byteArrayOf(1)))
        }
        assertTrue(callbackChecked.await(2, TimeUnit.SECONDS))
        val replacementOpened = CountDownLatch(1)
        val closeStarted = CountDownLatch(1)
        val closer = thread(name = "data-close-and-reuse") {
            closeStarted.countDown()
            fixture.bridge.closeFromRelay("same")
            fixture.bridge.open("same", targetAddress())
            replacementOpened.countDown()
        }
        assertTrue(closeStarted.await(2, TimeUnit.SECONDS))
        waitUntil { replacementOpened.count == 0L || closer.state == Thread.State.BLOCKED }
        val replacementEscapedOldLock = replacementOpened.count == 0L

        allowMailboxCommit.countDown()
        data.join(2_000)
        closer.join(2_000)
        val replacementToken = fixture.reactor.opened.getValue("same")
        assertTrue(fixture.listener.onData("same", replacementToken, byteArrayOf(2)))

        assertFalse(replacementEscapedOldLock)
        assertTrue(oldAccepted.get())
        assertNotEquals(oldToken, replacementToken)
        assertEquals(1, fixture.bridge.activeStreamCount)
        assertEquals(
            listOf(WireProtocol.encode("data", "same", byteArrayOf(2)).toList()),
            fixture.emittedFrames().map(ByteArray::toList),
        )
        assertEquals(emptyList<ErrorClass>(), fixture.failures)
    }

    @Test
    fun `tombstoned close cannot cancel a replacement generation`() {
        val pauseClose = AtomicBoolean(false)
        val closeChecked = CountDownLatch(1)
        val allowCloseCommit = CountDownLatch(1)
        val fixture = Fixture(
            maxStreams = 1,
            beforeMailboxCommit = {
                if (pauseClose.get()) {
                    closeChecked.countDown()
                    allowCloseCommit.await(2, TimeUnit.SECONDS)
                }
            },
        )
        fixture.bridge.open("same", targetAddress())
        val oldToken = fixture.reactor.opened.getValue("same")
        fixture.listener.onTerminal("same", oldToken, TargetTerminalReason.TargetFailure)
        assertEquals(0, fixture.bridge.activeStreamCount)
        pauseClose.set(true)
        val oldClose = thread(name = "tombstoned-old-close") {
            fixture.bridge.closeFromRelay("same")
        }
        assertTrue(closeChecked.await(2, TimeUnit.SECONDS))
        val replacementOpened = CountDownLatch(1)
        val opener = thread(name = "replacement-open") {
            fixture.bridge.open("same", targetAddress())
            replacementOpened.countDown()
        }
        waitUntil { replacementOpened.count == 0L || opener.state == Thread.State.BLOCKED }
        val replacementEscapedClose = replacementOpened.count == 0L

        allowCloseCommit.countDown()
        oldClose.join(2_000)
        opener.join(2_000)
        val replacementToken = fixture.reactor.opened.getValue("same")

        assertFalse(replacementEscapedClose)
        assertNotEquals(oldToken, replacementToken)
        assertEquals(1, fixture.bridge.activeStreamCount)
        assertTrue(fixture.listener.onData("same", replacementToken, byteArrayOf(2)))
        assertEquals(
            listOf(WireProtocol.encode("data", "same", byteArrayOf(2)).toList()),
            fixture.emittedFrames().map(ByteArray::toList),
        )
        assertEquals(emptyList<ErrorClass>(), fixture.failures)
    }

    @Test
    fun `full admission rejects malformed and policy denied opens as stream limit`() {
        val fixture = Fixture(maxStreams = 1)
        fixture.bridge.open("active", targetAddress())

        fixture.bridge.open(malformedOpen("malformed"))
        fixture.bridge.open(policyDeniedOpen("policy"))

        assertEquals(
            listOf("YWdlbnRfc3RyZWFtX2xpbWl0", "YWdlbnRfc3RyZWFtX2xpbWl0"),
            fixture.emittedFrames().map { frame ->
                Regex("\\\"payload\\\":\\\"([^\\\"]+)\\\"")
                    .find(frame.decodeToString())
                    ?.groupValues
                    ?.get(1)
            },
        )
        assertEquals(emptyList<ErrorClass>(), fixture.status.errors)
        assertEquals(setOf("active"), fixture.reactor.opened.keys)
    }

    @Test
    fun `duplicate admission rejects malformed and policy denied opens as stream limit`() {
        val fixture = Fixture(maxStreams = 2)
        fixture.bridge.open("same", targetAddress())

        fixture.bridge.open(malformedOpen("same"))
        fixture.bridge.open(policyDeniedOpen("same"))

        assertEquals(
            listOf("YWdlbnRfc3RyZWFtX2xpbWl0", "YWdlbnRfc3RyZWFtX2xpbWl0"),
            fixture.emittedFrames().map { frame ->
                Regex("\\\"payload\\\":\\\"([^\\\"]+)\\\"")
                    .find(frame.decodeToString())
                    ?.groupValues
                    ?.get(1)
            },
        )
        assertEquals(emptyList<ErrorClass>(), fixture.status.errors)
        assertEquals(1, fixture.reactor.opened.size)
    }

    @Test
    fun `any matching terminal crossing cancel releases admission exactly once`() {
        val fixture = Fixture(maxStreams = 1)
        fixture.bridge.open("same", targetAddress())
        val firstToken = fixture.reactor.opened.getValue("same")
        fixture.bridge.closeFromRelay("same")

        fixture.listener.onTerminal("same", firstToken, TargetTerminalReason.TargetFailure)
        fixture.listener.onTerminal("same", firstToken, TargetTerminalReason.Canceled)
        fixture.listener.onReleased("same", firstToken)

        assertEquals(0, fixture.bridge.activeStreamCount)
        assertEquals(listOf(1, 0), fixture.status.activeCounts)
        fixture.bridge.open("same", targetAddress())
        val secondToken = fixture.reactor.opened.getValue("same")
        assertNotEquals(firstToken, secondToken)
        assertEquals(1, fixture.bridge.activeStreamCount)
    }

    @Test
    fun `late callbacks from a prior same id token cannot mutate replacement`() {
        val fixture = Fixture(maxStreams = 1)
        fixture.bridge.open("same", targetAddress())
        val firstToken = fixture.reactor.opened.getValue("same")
        fixture.listener.onTerminal("same", firstToken, TargetTerminalReason.TargetFailure)
        fixture.bridge.open("same", targetAddress())
        val secondToken = fixture.reactor.opened.getValue("same")

        fixture.listener.onOpened("same", firstToken)
        assertFalse(fixture.listener.onData("same", firstToken, byteArrayOf(1)))
        fixture.listener.onBytesWritten("same", firstToken, 11)
        fixture.listener.onTerminal("same", firstToken, TargetTerminalReason.TargetClosed)
        fixture.listener.onReleased("same", firstToken)
        fixture.listener.onOpened("same", secondToken)

        assertEquals(1, fixture.bridge.activeStreamCount)
        assertEquals(0, fixture.status.bytesDown)
        assertEquals(0, fixture.status.bytesUp)
        assertEquals(
            listOf(WireProtocol.encode("opened", "same").toList()),
            fixture.emittedFrames().map(ByteArray::toList),
        )
    }

    @Test
    fun `target eof permits relay writes until close emission then drains release`() {
        val fixture = Fixture(maxStreams = 1)
        fixture.bridge.open("stream", targetAddress())
        val token = fixture.reactor.opened.getValue("stream")
        fixture.bridge.routeData("stream", "before".encodeToByteArray())
        assertTrue(fixture.listener.onData("stream", token, "down".encodeToByteArray()))
        fixture.listener.onTerminal("stream", token, TargetTerminalReason.TargetClosed)

        fixture.bridge.routeData("stream", "crossing".encodeToByteArray())
        val emitted = fixture.emittedFrames()

        assertEquals(
            listOf("before", "crossing"),
            fixture.reactor.writes.map { it.payload.decodeToString() },
        )
        assertEquals(
            listOf(
                WireProtocol.encode("data", "stream", "down".encodeToByteArray()).toList(),
                WireProtocol.encode("close", "stream", "target_closed".encodeToByteArray()).toList(),
            ),
            emitted.map(ByteArray::toList),
        )
        assertEquals(listOf(FakeReactor.Call("stream", token)), fixture.reactor.releases)
        assertEquals(1, fixture.bridge.activeStreamCount)

        fixture.bridge.routeData("stream", "too-late".encodeToByteArray())
        assertEquals(2, fixture.reactor.writes.size)
        fixture.listener.onReleased("stream", token)
        fixture.listener.onReleased("stream", token)
        assertEquals(0, fixture.bridge.activeStreamCount)
        assertEquals(listOf(1, 0), fixture.status.activeCounts)
    }

    @Test
    fun `production bridge and nio reactor drain a crossing write after loopback eof`() {
        val mailbox = OutboundMailbox()
        val failures = Collections.synchronizedList(mutableListOf<ErrorClass>())
        val received = Collections.synchronizedList(mutableListOf<Byte>())
        val accepted = CountDownLatch(1)
        val inputDrained = CountDownLatch(1)
        ServerSocket(0, 1, InetAddress.getLoopbackAddress()).use { server ->
            val target = thread(name = "bridge-half-close-target") {
                server.accept().use { socket ->
                    socket.shutdownOutput()
                    accepted.countDown()
                    val buffer = ByteArray(64)
                    while (true) {
                        val read = socket.getInputStream().read(buffer)
                        if (read < 0) break
                        repeat(read) { index -> received += buffer[index] }
                    }
                    inputDrained.countDown()
                }
            }
            val bridge = AgentTargetBridge(
                outbound = mailbox,
                reactorFactory = { listener ->
                    TargetIoReactor(TargetSocketBinder {}, listener)
                },
                onSessionFailure = failures::add,
                maxStreams = 1,
            )
            assertTrue(bridge.start())
            try {
                bridge.open(
                    "stream",
                    InetSocketAddress(InetAddress.getLoopbackAddress(), server.localPort),
                )
                assertTrue(accepted.await(2, TimeUnit.SECONDS))
                val opened = pollEventually(mailbox)
                assertEquals(
                    WireProtocol.encode("opened", "stream").toList(),
                    opened.bytes.toList(),
                )
                assertEquals(OutboundEmission.Emitted, mailbox.emit(opened) { true })
                val targetClosed = pollEventually(mailbox)
                assertEquals(
                    WireProtocol.encode("close", "stream", "target_closed".encodeToByteArray()).toList(),
                    targetClosed.bytes.toList(),
                )

                bridge.routeData("stream", "crossing".encodeToByteArray())
                assertEquals(OutboundEmission.Emitted, mailbox.emit(targetClosed) { true })

                assertTrue(inputDrained.await(2, TimeUnit.SECONDS))
                waitUntil { bridge.activeStreamCount == 0 }
                assertEquals("crossing", received.toByteArray().decodeToString())
                assertEquals(emptyList<ErrorClass>(), failures)
            } finally {
                bridge.shutdownAndAwait(2, TimeUnit.SECONDS)
                server.close()
                target.join(2_000)
            }
        }
    }

    @Test
    fun `non reactor shutdown waits for backend completion barrier`() {
        val fixture = Fixture()
        fixture.bridge.open("stream", targetAddress())
        fixture.reactor.blockAwait.set(true)
        val returned = AtomicBoolean(false)
        val closer = thread(name = "bridge-close-test") {
            fixture.bridge.shutdownAndAwait(2, TimeUnit.SECONDS)
            returned.set(true)
        }

        assertTrue(fixture.reactor.shutdownCalled.await(1, TimeUnit.SECONDS))
        assertFalse(returned.get())
        fixture.reactor.allowStopped.countDown()
        closer.join(2_000)

        assertTrue(returned.get())
        assertTrue(fixture.reactor.shutdown.get())
        assertEquals(0, fixture.bridge.activeStreamCount)
    }

    @Test
    fun `reactor callback shutdown never waits on its own completion barrier`() {
        lateinit var bridge: AgentTargetBridge
        lateinit var listener: TargetReactorListener
        val reactor = FakeReactor(ReactorSubmitResult.Accepted).apply {
            reactorThread.set(true)
        }
        val callbackReturned = AtomicBoolean(false)
        bridge = AgentTargetBridge(
            outbound = OutboundMailbox(),
            reactorFactory = {
                listener = it
                reactor
            },
            onSessionFailure = {
                bridge.shutdownAndAwait(2, TimeUnit.SECONDS)
                callbackReturned.set(true)
            },
        )
        assertTrue(bridge.start())

        listener.onFatalFailure()

        assertTrue(callbackReturned.get())
        assertTrue(reactor.shutdown.get())
        assertEquals(0, reactor.awaitCalls.get())
    }

    @Test
    fun `shutdown racing reactor factory cannot return before created reactor stops`() {
        val factoryEntered = CountDownLatch(1)
        val allowFactory = CountDownLatch(1)
        val reactor = FakeReactor(ReactorSubmitResult.Accepted).apply {
            blockAwait.set(true)
        }
        val bridge = AgentTargetBridge(
            outbound = OutboundMailbox(),
            reactorFactory = {
                factoryEntered.countDown()
                allowFactory.await(2, TimeUnit.SECONDS)
                reactor
            },
            onSessionFailure = {},
        )
        val startReturned = AtomicBoolean(false)
        val barrierReturned = AtomicBoolean(false)
        val starter = thread(name = "bridge-start-race") {
            bridge.start()
            startReturned.set(true)
        }
        assertTrue(factoryEntered.await(2, TimeUnit.SECONDS))
        val closer = thread(name = "bridge-close-race") {
            barrierReturned.set(bridge.shutdownAndAwait(2, TimeUnit.SECONDS))
        }

        Thread.sleep(25)
        assertFalse(barrierReturned.get())
        allowFactory.countDown()
        assertTrue(reactor.shutdownCalled.await(2, TimeUnit.SECONDS))
        assertFalse(barrierReturned.get())
        reactor.allowStopped.countDown()
        starter.join(2_000)
        closer.join(2_000)

        assertTrue(startReturned.get())
        assertTrue(barrierReturned.get())
        assertTrue(reactor.shutdown.get())
    }

    @Test
    fun `shutdown cannot linearize through an in flight open submission`() {
        val fixture = Fixture(maxStreams = 1)
        val openEntered = CountDownLatch(1)
        val allowOpen = CountDownLatch(1)
        fixture.reactor.openAction = {
            openEntered.countDown()
            allowOpen.await(2, TimeUnit.SECONDS)
            ReactorSubmitResult.Accepted
        }
        val opener = thread(name = "bridge-open-race") {
            fixture.bridge.open("racing", targetAddress())
        }
        assertTrue(openEntered.await(2, TimeUnit.SECONDS))
        val shutdownReturned = CountDownLatch(1)
        val shutdownStarted = CountDownLatch(1)
        val closer = thread(name = "bridge-shutdown-race") {
            shutdownStarted.countDown()
            fixture.bridge.shutdownAndAwait(2, TimeUnit.SECONDS)
            shutdownReturned.countDown()
        }

        try {
            assertTrue(shutdownStarted.await(2, TimeUnit.SECONDS))
            waitUntil { shutdownReturned.count == 0L || closer.state == Thread.State.BLOCKED }
            assertEquals(1L, shutdownReturned.count)
        } finally {
            allowOpen.countDown()
            opener.join(2_000)
            closer.join(2_000)
        }

        assertEquals(0, fixture.bridge.activeStreamCount)
        assertEquals(listOf(1, 0, 0), fixture.status.activeCounts)
        assertTrue(fixture.reactor.shutdown.get())
    }

    private class Fixture(
        outbound: OutboundMailbox = OutboundMailbox(),
        openResult: ReactorSubmitResult = ReactorSubmitResult.Accepted,
        maxStreams: Int = 256,
        beforeMailboxCommit: () -> Unit = {},
    ) {
        lateinit var listener: TargetReactorListener
        val reactor = FakeReactor(openResult)
        val failures = Collections.synchronizedList(mutableListOf<ErrorClass>())
        val status = RecordingStatus()
        val bridge = AgentTargetBridge(
            outbound = outbound,
            reactorFactory = {
                listener = it
                reactor
            },
            onSessionFailure = failures::add,
            status = status,
            maxStreams = maxStreams,
            beforeMailboxCommit = beforeMailboxCommit,
        )
        val mailbox = outbound

        init {
            assertTrue(bridge.start())
        }

        fun emittedFrames(): List<ByteArray> = buildList {
            while (true) {
                val frame = mailbox.poll() ?: break
                mailbox.emit(frame) { bytes ->
                    add(bytes)
                    true
                }
            }
        }
    }

    private class FakeReactor(
        private val openResult: ReactorSubmitResult,
    ) : TargetReactorPort {
        data class Call(val streamId: String, val correlationToken: Long)
        data class Write(val streamId: String, val correlationToken: Long, val payload: ByteArray)

        val opened = HashMap<String, Long>()
        @Volatile var openAction: (() -> ReactorSubmitResult)? = null
        val writes = Collections.synchronizedList(mutableListOf<Write>())
        val cancels = Collections.synchronizedList(mutableListOf<Call>())
        val releases = Collections.synchronizedList(mutableListOf<Call>())
        val writeResults = HashMap<String, ReactorSubmitResult>()
        val releaseCalled = CountDownLatch(1)
        @Volatile var writeAction: ((Write) -> ReactorSubmitResult)? = null
        @Volatile var cancelResult = ReactorSubmitResult.Accepted
        val shutdown = AtomicBoolean(false)
        val shutdownCalled = CountDownLatch(1)
        val allowStopped = CountDownLatch(1)
        val blockAwait = AtomicBoolean(false)
        val reactorThread = AtomicBoolean(false)
        val awaitCalls = AtomicInteger()

        override fun start(): Boolean = true

        override fun open(
            streamId: String,
            correlationToken: Long,
            address: InetSocketAddress,
        ): ReactorSubmitResult {
            opened[streamId] = correlationToken
            return openAction?.invoke() ?: openResult
        }

        override fun write(
            streamId: String,
            correlationToken: Long,
            payload: ByteArray,
        ): ReactorSubmitResult {
            val call = Write(streamId, correlationToken, payload.copyOf())
            writes += call
            return writeAction?.invoke(call)
                ?: writeResults.remove(streamId)
                ?: ReactorSubmitResult.Accepted
        }

        override fun cancel(streamId: String, correlationToken: Long): ReactorSubmitResult {
            cancels += Call(streamId, correlationToken)
            return cancelResult
        }

        override fun release(streamId: String, correlationToken: Long): ReactorSubmitResult {
            releases += Call(streamId, correlationToken)
            releaseCalled.countDown()
            return ReactorSubmitResult.Accepted
        }

        override fun shutdown() {
            shutdown.set(true)
            shutdownCalled.countDown()
        }

        override fun awaitStopped(timeout: Long, unit: TimeUnit): Boolean {
            awaitCalls.incrementAndGet()
            return !blockAwait.get() || allowStopped.await(timeout, unit)
        }

        override fun isReactorThread(): Boolean = reactorThread.get()
    }

    private class RecordingStatus : AgentTargetStatusSink {
        val activeCounts = Collections.synchronizedList(mutableListOf<Int>())
        val errors = Collections.synchronizedList(mutableListOf<ErrorClass>())
        var bytesDown = 0
        var bytesUp = 0

        override fun onActiveStreams(count: Int) {
            activeCounts += count
        }

        override fun onBytesDown(byteCount: Int) {
            bytesDown += byteCount
        }

        override fun onBytesUp(byteCount: Int) {
            bytesUp += byteCount
        }

        override fun onError(errorClass: ErrorClass) {
            errors += errorClass
        }
    }

    private fun targetAddress() = InetSocketAddress(InetAddress.getLoopbackAddress(), 9)

    private fun malformedOpen(streamId: String) = WireProtocol.parseAgentInbound(
        WireProtocol.encode("open", streamId, "not-json".encodeToByteArray()),
    )

    private fun policyDeniedOpen(streamId: String) = WireProtocol.parseAgentInbound(
        WireProtocol.encode(
            "open",
            streamId,
            "{\"ip\":\"127.0.0.1\",\"port\":80}".encodeToByteArray(),
        ),
    )

    private fun pollEventually(mailbox: OutboundMailbox): OutboundFrame {
        var frame: OutboundFrame? = null
        waitUntil { mailbox.poll()?.also { frame = it } != null }
        return requireNotNull(frame)
    }

    private fun waitUntil(timeoutMillis: Long = 2_000, condition: () -> Boolean) {
        val deadline = System.nanoTime() + TimeUnit.MILLISECONDS.toNanos(timeoutMillis)
        while (!condition()) {
            if (System.nanoTime() >= deadline) throw AssertionError("condition was not met")
            Thread.sleep(2)
        }
    }
}
