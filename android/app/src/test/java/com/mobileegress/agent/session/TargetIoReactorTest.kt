package com.mobileegress.agent.session

import com.mobileegress.agent.status.ErrorClass
import java.io.ByteArrayOutputStream
import java.net.InetAddress
import java.net.InetSocketAddress
import java.nio.ByteBuffer
import java.util.ArrayDeque
import java.util.Collections
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicInteger
import kotlin.random.Random
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class TargetIoReactorTest {
    @Test
    fun `immediate and deferred connects each emit opened exactly once`() {
        val backend = FakeSelectorBackend(
            FakeConnection("immediate", connectedImmediately = true),
            FakeConnection("deferred", connectedImmediately = false),
        )
        val listener = RecordingListener(openCount = 2)
        val reactor = reactor(backend, listener)
        reactor.start()

        assertEquals(ReactorSubmitResult.Accepted, reactor.open("immediate", targetAddress()))
        assertEquals(ReactorSubmitResult.Accepted, reactor.open("deferred", targetAddress()))
        waitUntil { listener.opened.count { it == "immediate" } == 1 }
        assertEquals(0, listener.opened.count { it == "deferred" })

        backend.ready("deferred", connectable = true)

        assertTrue(listener.opens.await(2, TimeUnit.SECONDS))
        assertEquals(listOf("deferred", "immediate"), listener.opened.sorted())
        assertEquals(1, backend.connection("deferred").finishConnectCalls.get())
        reactor.shutdown()
        assertTrue(reactor.awaitStopped(2, TimeUnit.SECONDS))
    }

    @Test
    fun `post open interest failure closes the active channel before terminal result`() {
        val connection = FakeConnection(
            "stream",
            connectedImmediately = true,
            failInterests = true,
        )
        val backend = FakeSelectorBackend(connection)
        val listener = RecordingListener(openCount = 1, terminalCount = 1)
        val reactor = reactor(backend, listener)
        reactor.start()
        try {
            assertEquals(ReactorSubmitResult.Accepted, reactor.open("stream", targetAddress()))

            assertTrue(listener.terminals.await(2, TimeUnit.SECONDS))
            waitUntil { connection.closeCalls.get() == 1 }
            assertEquals(listOf(TargetTerminalReason.TargetFailure), listener.terminalReasons["stream"])
        } finally {
            reactor.shutdown()
            assertTrue(reactor.awaitStopped(2, TimeUnit.SECONDS))
        }
    }

    @Test
    fun `partial write offset is preserved and eof waits for explicit release`() {
        val connection = FakeConnection("stream", connectedImmediately = true, maxWriteBytes = 2)
        val backend = FakeSelectorBackend(connection)
        val listener = RecordingListener(openCount = 1, dataCount = 1, terminalCount = 1)
        val reactor = reactor(backend, listener)
        reactor.start()
        assertEquals(ReactorSubmitResult.Accepted, reactor.open("stream", targetAddress()))
        assertTrue(listener.opens.await(2, TimeUnit.SECONDS))

        assertEquals(ReactorSubmitResult.Accepted, reactor.write("stream", "abcdef".encodeToByteArray()))
        waitUntil { connection.writeInterested.get() }
        backend.ready("stream", writable = true)
        waitUntil { connection.written().decodeToString() == "ab" }

        connection.enqueueRead("xyz".encodeToByteArray())
        backend.ready("stream", readable = true)
        assertTrue(listener.data.await(2, TimeUnit.SECONDS))
        connection.enqueueEof()
        backend.ready("stream", readable = true)

        assertTrue(listener.terminals.await(2, TimeUnit.SECONDS))
        assertEquals(listOf(TargetTerminalReason.TargetClosed), listener.terminalReasons["stream"])
        assertEquals(0, connection.closeCalls.get())
        assertEquals(ReactorSubmitResult.Accepted, reactor.write("stream", byteArrayOf(9)))

        backend.ready("stream", writable = true)
        waitUntil { connection.written().decodeToString() == "abcd" }
        backend.ready("stream", writable = true)
        waitUntil { connection.written().decodeToString() == "abcdef" }
        assertEquals(ReactorSubmitResult.Accepted, reactor.release("stream"))
        backend.ready("stream", writable = true)
        waitUntil { connection.closeCalls.get() == 1 }
        assertEquals("abcdef\t", connection.written().decodeToString())
        assertEquals(1, listener.terminalReasons.getValue("stream").size)

        reactor.shutdown()
        assertTrue(reactor.awaitStopped(2, TimeUnit.SECONDS))
    }

    @Test
    fun `release is missing after eof stream closes before mailbox emission`() {
        val clock = MutableNanoClock()
        val connection = FakeConnection("stream", connectedImmediately = true)
        val backend = FakeSelectorBackend(connection)
        val listener = RecordingListener(openCount = 1, terminalCount = 1)
        val reactor = reactor(
            backend,
            listener,
            idleTimeoutMillis = 100,
            nanoTime = clock::read,
        )
        reactor.start()
        try {
            assertEquals(ReactorSubmitResult.Accepted, reactor.open("stream", targetAddress()))
            assertTrue(listener.opens.await(2, TimeUnit.SECONDS))
            connection.enqueueEof()
            backend.ready("stream", readable = true)
            assertTrue(listener.terminals.await(2, TimeUnit.SECONDS))

            clock.advanceMillis(100)
            backend.ready("stream")
            waitUntil { connection.closeCalls.get() == 1 }

            assertEquals(ReactorSubmitResult.MissingOrClosed, reactor.release("stream"))
            assertEquals(listOf(TargetTerminalReason.TargetClosed), listener.terminalReasons["stream"])
        } finally {
            reactor.shutdown()
            assertTrue(reactor.awaitStopped(2, TimeUnit.SECONDS))
        }
    }

    @Test
    fun `cancellation closes once and ignores stale readiness`() {
        val connection = FakeConnection("stream", connectedImmediately = true)
        val backend = FakeSelectorBackend(connection)
        val listener = RecordingListener(openCount = 1, terminalCount = 1)
        val reactor = reactor(backend, listener)
        reactor.start()
        reactor.open("stream", targetAddress())
        assertTrue(listener.opens.await(2, TimeUnit.SECONDS))

        assertEquals(ReactorSubmitResult.Accepted, reactor.cancel("stream"))
        assertTrue(listener.terminals.await(2, TimeUnit.SECONDS))
        backend.ready("stream", readable = true, writable = true)
        Thread.sleep(25)

        assertEquals(listOf(TargetTerminalReason.Canceled), listener.terminalReasons["stream"])
        assertEquals(1, connection.closeCalls.get())
        reactor.shutdown()
        assertTrue(reactor.awaitStopped(2, TimeUnit.SECONDS))
    }

    @Test
    fun `connect deadline is target failure and idle deadline is idle timeout`() {
        val connecting = FakeConnection("connecting", connectedImmediately = false)
        val idle = FakeConnection("idle", connectedImmediately = true)
        val backend = FakeSelectorBackend(connecting, idle)
        val listener = RecordingListener(openCount = 1, terminalCount = 2)
        val reactor = reactor(
            backend,
            listener,
            connectTimeoutMillis = 40,
            idleTimeoutMillis = 80,
        )
        reactor.start()

        reactor.open("connecting", targetAddress())
        reactor.open("idle", targetAddress())

        assertTrue(listener.terminals.await(2, TimeUnit.SECONDS))
        assertEquals(listOf(TargetTerminalReason.TargetFailure), listener.terminalReasons["connecting"])
        assertEquals(listOf(TargetTerminalReason.IdleTimeout), listener.terminalReasons["idle"])
        assertEquals(1, connecting.closeCalls.get())
        assertEquals(1, idle.closeCalls.get())
        reactor.shutdown()
        assertTrue(reactor.awaitStopped(2, TimeUnit.SECONDS))
    }

    @Test
    fun `connect deadline starts when open command is admitted`() {
        val clock = MutableNanoClock()
        val backend = FakeSelectorBackend(FakeConnection("late", connectedImmediately = false))
        val listener = RecordingListener(terminalCount = 1)
        val reactor = reactor(
            backend,
            listener,
            connectTimeoutMillis = 40,
            nanoTime = clock::read,
        )
        assertEquals(ReactorSubmitResult.Accepted, reactor.open("late", targetAddress()))
        clock.advanceMillis(41)

        reactor.start()

        try {
            assertTrue(listener.terminals.await(2, TimeUnit.SECONDS))
            assertEquals(listOf(TargetTerminalReason.TargetFailure), listener.terminalReasons["late"])
            assertEquals(0, backend.openCalls.get())
        } finally {
            reactor.shutdown()
            assertTrue(reactor.awaitStopped(2, TimeUnit.SECONDS))
        }
    }

    @Test
    fun `slow backend open crossing connect deadline never emits opened`() {
        val clock = MutableNanoClock()
        val backend = FakeSelectorBackend(FakeConnection("late", connectedImmediately = true)).apply {
            beforeOpen = { clock.advanceMillis(40) }
        }
        val listener = RecordingListener(terminalCount = 1)
        val reactor = reactor(
            backend,
            listener,
            connectTimeoutMillis = 40,
            nanoTime = clock::read,
        )
        reactor.start()
        try {
            assertEquals(ReactorSubmitResult.Accepted, reactor.open("late", targetAddress()))

            assertTrue(listener.terminals.await(2, TimeUnit.SECONDS))
            assertEquals(emptyList<String>(), listener.opened)
            assertEquals(listOf(TargetTerminalReason.TargetFailure), listener.terminalReasons["late"])
        } finally {
            reactor.shutdown()
            assertTrue(reactor.awaitStopped(2, TimeUnit.SECONDS))
        }
    }

    @Test
    fun `connect readiness exactly at deadline loses to timeout`() {
        val clock = MutableNanoClock()
        val connection = FakeConnection("late", connectedImmediately = false)
        val backend = FakeSelectorBackend(connection)
        val listener = RecordingListener(terminalCount = 1)
        val reactor = reactor(
            backend,
            listener,
            connectTimeoutMillis = 40,
            nanoTime = clock::read,
        )
        reactor.start()
        try {
            reactor.open("late", targetAddress())
            backend.connection("late")

            backend.beforeSelectReturn = { clock.advanceMillis(40) }
            backend.ready("late", connectable = true)

            assertTrue(listener.terminals.await(2, TimeUnit.SECONDS))
            assertEquals(0, connection.finishConnectCalls.get())
            assertEquals(emptyList<String>(), listener.opened)
            assertEquals(listOf(TargetTerminalReason.TargetFailure), listener.terminalReasons["late"])
        } finally {
            reactor.shutdown()
            assertTrue(reactor.awaitStopped(2, TimeUnit.SECONDS))
        }
    }

    @Test
    fun `read and write readiness exactly at idle deadline lose to timeout`() {
        val clock = MutableNanoClock()
        val connection = FakeConnection("idle", connectedImmediately = true)
        val backend = FakeSelectorBackend(connection)
        val listener = RecordingListener(openCount = 1, terminalCount = 1)
        val reactor = reactor(
            backend,
            listener,
            idleTimeoutMillis = 40,
            nanoTime = clock::read,
        )
        reactor.start()
        try {
            reactor.open("idle", targetAddress())
            assertTrue(listener.opens.await(2, TimeUnit.SECONDS))
            assertEquals(ReactorSubmitResult.Accepted, reactor.write("idle", byteArrayOf(7)))
            connection.enqueueRead(byteArrayOf(9))
            backend.beforeSelectReturn = { clock.advanceMillis(40) }
            backend.ready("idle", readable = true, writable = true)

            assertTrue(listener.terminals.await(2, TimeUnit.SECONDS))
            assertEquals(0, connection.writeCalls.get())
            assertFalse(listener.received.containsKey("idle"))
            assertEquals(listOf(TargetTerminalReason.IdleTimeout), listener.terminalReasons["idle"])
        } finally {
            reactor.shutdown()
            assertTrue(reactor.awaitStopped(2, TimeUnit.SECONDS))
        }
    }

    @Test
    fun `eof interest update failure still emits exactly one target failure`() {
        val connection = FakeConnection("stream", connectedImmediately = true)
        val backend = FakeSelectorBackend(connection)
        val listener = RecordingListener(openCount = 1, terminalCount = 1)
        val reactor = reactor(backend, listener)
        reactor.start()
        try {
            reactor.open("stream", targetAddress())
            assertTrue(listener.opens.await(2, TimeUnit.SECONDS))
            connection.failInterestsNow.set(true)
            connection.enqueueEof()

            backend.ready("stream", readable = true)

            assertTrue(listener.terminals.await(2, TimeUnit.SECONDS))
            assertEquals(listOf(TargetTerminalReason.TargetFailure), listener.terminalReasons["stream"])
            assertEquals(1, connection.closeCalls.get())
        } finally {
            reactor.shutdown()
            assertTrue(reactor.awaitStopped(2, TimeUnit.SECONDS))
        }
    }

    @Test
    fun `graceful release drains accepted partial writes before closing`() {
        val connection = FakeConnection("stream", connectedImmediately = true, maxWriteBytes = 2)
        val backend = FakeSelectorBackend(connection)
        val listener = RecordingListener(openCount = 1, terminalCount = 1)
        val reactor = reactor(backend, listener)
        reactor.start()
        try {
            reactor.open("stream", targetAddress())
            assertTrue(listener.opens.await(2, TimeUnit.SECONDS))
            assertEquals(ReactorSubmitResult.Accepted, reactor.write("stream", "abcdef".encodeToByteArray()))
            waitUntil { connection.writeInterested.get() }
            backend.ready("stream", writable = true)
            waitUntil { connection.written().decodeToString() == "ab" }
            connection.enqueueEof()
            backend.ready("stream", readable = true)
            assertTrue(listener.terminals.await(2, TimeUnit.SECONDS))

            assertEquals(ReactorSubmitResult.Accepted, reactor.write("stream", "gh".encodeToByteArray()))
            assertEquals(ReactorSubmitResult.Accepted, reactor.release("stream"))
            assertEquals(ReactorSubmitResult.MissingOrClosed, reactor.write("stream", byteArrayOf(1)))
            repeat(3) {
                backend.ready("stream", writable = true)
                Thread.sleep(5)
            }

            waitUntil { connection.closeCalls.get() == 1 }
            assertEquals("abcdefgh", connection.written().decodeToString())
            assertEquals(1, listener.terminalReasons.getValue("stream").size)
        } finally {
            reactor.shutdown()
            assertTrue(reactor.awaitStopped(2, TimeUnit.SECONDS))
        }
    }

    @Test
    fun `actual transferred bytes reset idle deadline`() {
        val connection = FakeConnection("active", connectedImmediately = true)
        val backend = FakeSelectorBackend(connection)
        val listener = RecordingListener(openCount = 1, dataCount = 1, terminalCount = 1)
        val reactor = reactor(backend, listener, idleTimeoutMillis = 100)
        reactor.start()
        reactor.open("active", targetAddress())
        assertTrue(listener.opens.await(2, TimeUnit.SECONDS))
        Thread.sleep(60)

        connection.enqueueRead(byteArrayOf(1))
        backend.ready("active", readable = true)
        assertTrue(listener.data.await(2, TimeUnit.SECONDS))
        Thread.sleep(60)
        assertFalse(listener.terminalReasons.containsKey("active"))

        assertTrue(listener.terminals.await(2, TimeUnit.SECONDS))
        assertEquals(listOf(TargetTerminalReason.IdleTimeout), listener.terminalReasons["active"])
        reactor.shutdown()
        assertTrue(reactor.awaitStopped(2, TimeUnit.SECONDS))
    }

    @Test
    fun `blocked target cannot prevent peer read write open or close`() {
        val blocked = FakeConnection("blocked", connectedImmediately = true, maxWriteBytes = 0)
        val peer = FakeConnection("peer", connectedImmediately = true)
        val newcomer = FakeConnection("newcomer", connectedImmediately = true)
        val backend = FakeSelectorBackend(blocked, peer, newcomer)
        val listener = RecordingListener(openCount = 3, dataCount = 1, terminalCount = 1)
        val reactor = reactor(backend, listener)
        reactor.start()
        reactor.open("blocked", targetAddress())
        reactor.open("peer", targetAddress())
        waitUntil { listener.opened.size == 2 }
        reactor.write("blocked", byteArrayOf(1))
        reactor.write("peer", byteArrayOf(2))
        waitUntil { blocked.writeInterested.get() && peer.writeInterested.get() }

        peer.enqueueRead(byteArrayOf(3))
        backend.ready("blocked", writable = true)
        backend.ready("peer", readable = true, writable = true)
        reactor.open("newcomer", targetAddress())
        reactor.cancel("peer")

        assertTrue(listener.opens.await(2, TimeUnit.SECONDS))
        assertTrue(listener.data.await(2, TimeUnit.SECONDS))
        assertTrue(listener.terminals.await(2, TimeUnit.SECONDS))
        assertEquals(0, blocked.written().size)
        assertEquals(listOf(2.toByte()), peer.written().toList())
        assertEquals(listOf(3.toByte()), listener.received.getValue("peer").toList())
        assertEquals(listOf(TargetTerminalReason.Canceled), listener.terminalReasons["peer"])

        reactor.shutdown()
        assertTrue(reactor.awaitStopped(2, TimeUnit.SECONDS))
    }

    @Test
    fun `two hundred fifty six ready streams each write one queued chunk per cycle`() {
        val connections = Array(256) { index ->
            FakeConnection("stream-$index", connectedImmediately = true)
        }
        val backend = FakeSelectorBackend(*connections)
        val listener = RecordingListener(openCount = 256)
        val reactor = reactor(backend, listener)
        reactor.start()
        repeat(256) { index ->
            assertEquals(ReactorSubmitResult.Accepted, reactor.open("stream-$index", targetAddress()))
        }
        assertTrue(listener.opens.await(2, TimeUnit.SECONDS))

        repeat(256) { index ->
            assertEquals(ReactorSubmitResult.Accepted, reactor.write("stream-$index", byteArrayOf(1)))
            assertEquals(ReactorSubmitResult.Accepted, reactor.write("stream-$index", byteArrayOf(2)))
        }
        waitUntil { connections.all { it.writeInterested.get() } }
        val stableShuffledOrder = (0 until 256).shuffled(Random(20260901))

        backend.readyBatch(stableShuffledOrder.map { "stream-$it" })
        waitUntil { connections.all { it.written().contentEquals(byteArrayOf(1)) } }
        backend.readyBatch(stableShuffledOrder.reversed().map { "stream-$it" })
        waitUntil { connections.all { it.written().contentEquals(byteArrayOf(1, 2)) } }

        assertEquals(
            1,
            Thread.getAllStackTraces().keys.count { it.name == TargetIoReactor.REACTOR_THREAD_NAME },
        )
        assertEquals(256, connections.count { it.writeCalls.get() == 2 })
        reactor.shutdown()
        assertTrue(reactor.awaitStopped(2, TimeUnit.SECONDS))
    }

    @Test
    fun `per stream and aggregate data saturation fail only the affected stream`() {
        val perStreamBackend = FakeSelectorBackend(FakeConnection("busy", connectedImmediately = true))
        val perStreamListener = RecordingListener(terminalCount = 1)
        val perStream = reactor(perStreamBackend, perStreamListener, commandCapacity = 8)
        assertEquals(ReactorSubmitResult.Accepted, perStream.open("busy", targetAddress()))
        assertEquals(ReactorSubmitResult.Accepted, perStream.write("busy", byteArrayOf(1)))
        assertEquals(ReactorSubmitResult.Accepted, perStream.write("busy", byteArrayOf(2)))
        assertEquals(ReactorSubmitResult.StreamSaturated, perStream.write("busy", byteArrayOf(3)))
        perStream.start()
        assertTrue(perStreamListener.terminals.await(2, TimeUnit.SECONDS))
        assertEquals(listOf(TargetTerminalReason.Backpressure), perStreamListener.terminalReasons["busy"])
        assertFalse(perStreamListener.fatal.get())
        perStream.shutdown()
        assertTrue(perStream.awaitStopped(2, TimeUnit.SECONDS))

        val aggregateBackend = FakeSelectorBackend(FakeConnection("aggregate", connectedImmediately = true))
        val aggregateListener = RecordingListener(terminalCount = 1)
        val aggregate = reactor(aggregateBackend, aggregateListener, commandCapacity = 2)
        assertEquals(ReactorSubmitResult.Accepted, aggregate.open("aggregate", targetAddress()))
        assertEquals(ReactorSubmitResult.Accepted, aggregate.write("aggregate", byteArrayOf(1)))
        assertEquals(ReactorSubmitResult.StreamSaturated, aggregate.write("aggregate", byteArrayOf(2)))
        aggregate.start()
        assertTrue(aggregateListener.terminals.await(2, TimeUnit.SECONDS))
        assertEquals(listOf(TargetTerminalReason.Backpressure), aggregateListener.terminalReasons["aggregate"])
        assertFalse(aggregateListener.fatal.get())
        aggregate.shutdown()
        assertTrue(aggregate.awaitStopped(2, TimeUnit.SECONDS))
    }

    @Test
    fun `required command overflow is session fatal but shutdown cannot be stranded`() {
        val backend = FakeSelectorBackend(
            FakeConnection("one", connectedImmediately = true),
            FakeConnection("two", connectedImmediately = true),
            FakeConnection("three", connectedImmediately = true),
        )
        val listener = RecordingListener(terminalCount = 2)
        val reactor = reactor(backend, listener, commandCapacity = 2, maxStreams = 3)
        assertEquals(ReactorSubmitResult.Accepted, reactor.open("one", targetAddress()))
        assertEquals(ReactorSubmitResult.Accepted, reactor.open("two", targetAddress()))
        assertEquals(ReactorSubmitResult.SessionSaturated, reactor.open("three", targetAddress()))

        reactor.start()
        waitUntil { listener.opened.size == 2 }
        assertEquals(ReactorSubmitResult.Accepted, reactor.write("one", byteArrayOf(1)))
        assertEquals(ReactorSubmitResult.Accepted, reactor.write("two", byteArrayOf(2)))
        assertEquals(ReactorSubmitResult.SessionSaturated, reactor.cancel("one"))
        reactor.shutdown()

        assertTrue(reactor.awaitStopped(2, TimeUnit.SECONDS))
        assertTrue(backend.closed.get())
        assertEquals(listOf(TargetTerminalReason.Shutdown), listener.terminalReasons["one"])
        assertEquals(listOf(TargetTerminalReason.Shutdown), listener.terminalReasons["two"])
    }

    @Test
    fun `default command queue admits five hundred twelve commands and shutdown drains out of band`() {
        val backend = FakeSelectorBackend()
        val listener = RecordingListener(terminalCount = 512)
        val reactor = reactor(backend, listener, maxStreams = 513)
        repeat(512) { index ->
            assertEquals(
                ReactorSubmitResult.Accepted,
                reactor.open("stream-$index", targetAddress()),
            )
        }
        assertEquals(
            ReactorSubmitResult.SessionSaturated,
            reactor.open("stream-512", targetAddress()),
        )

        reactor.shutdown()

        assertTrue(listener.terminals.await(2, TimeUnit.SECONDS))
        assertTrue(reactor.awaitStopped(2, TimeUnit.SECONDS))
        assertTrue(backend.closed.get())
        assertEquals(512, listener.terminalReasons.size)
        assertTrue(listener.terminalReasons.values.all { it == listOf(TargetTerminalReason.Shutdown) })
    }

    @Test
    fun `selector shutdown and selector failure each notify once`() {
        val shutdownBackend = FakeSelectorBackend(
            FakeConnection("one", connectedImmediately = true),
            FakeConnection("two", connectedImmediately = true),
        )
        val shutdownListener = RecordingListener(openCount = 2, terminalCount = 2)
        val shuttingDown = reactor(shutdownBackend, shutdownListener)
        shuttingDown.start()
        shuttingDown.open("one", targetAddress())
        shuttingDown.open("two", targetAddress())
        assertTrue(shutdownListener.opens.await(2, TimeUnit.SECONDS))
        shuttingDown.shutdown()
        assertTrue(shuttingDown.awaitStopped(2, TimeUnit.SECONDS))
        assertEquals(listOf(TargetTerminalReason.Shutdown), shutdownListener.terminalReasons["one"])
        assertEquals(listOf(TargetTerminalReason.Shutdown), shutdownListener.terminalReasons["two"])
        assertFalse(shutdownListener.fatal.get())

        val failedBackend = FakeSelectorBackend(FakeConnection("failed", connectedImmediately = true))
        val failedListener = RecordingListener(openCount = 1, terminalCount = 1)
        val failed = reactor(failedBackend, failedListener)
        failed.start()
        failed.open("failed", targetAddress())
        assertTrue(failedListener.opens.await(2, TimeUnit.SECONDS))
        failedBackend.failSelect.set(true)
        failedBackend.wakeup()
        assertTrue(failed.awaitStopped(2, TimeUnit.SECONDS))

        assertTrue(failedListener.fatal.get())
        assertEquals(1, failedListener.fatalCalls.get())
        assertEquals(listOf(TargetTerminalReason.Shutdown), failedListener.terminalReasons["failed"])
    }

    @Test
    fun `selector wakeup failure is fatal exactly once and teardown stays bounded`() {
        val backend = FakeSelectorBackend(FakeConnection("stream", connectedImmediately = true))
        val listener = RecordingListener(openCount = 1, terminalCount = 1)
        val reactor = reactor(backend, listener)
        assertTrue(reactor.start())
        assertEquals(ReactorSubmitResult.Accepted, reactor.open("stream", targetAddress()))
        assertTrue(listener.opens.await(2, TimeUnit.SECONDS))
        backend.failWakeup.set(true)

        assertEquals(ReactorSubmitResult.Accepted, reactor.write("stream", byteArrayOf(1)))

        assertTrue(reactor.awaitStopped(2, TimeUnit.SECONDS))
        assertTrue(backend.closed.get())
        assertEquals(1, listener.fatalCalls.get())
        assertEquals(listOf(TargetTerminalReason.Shutdown), listener.terminalReasons["stream"])
    }

    @Test
    fun `production bridge turns selector wakeup failure into session termination`() {
        val backend = FakeSelectorBackend(FakeConnection("stream", connectedImmediately = true))
        val mailbox = OutboundMailbox()
        val failures = Collections.synchronizedList(mutableListOf<ErrorClass>())
        lateinit var bridge: AgentTargetBridge
        bridge = AgentTargetBridge(
            outbound = mailbox,
            reactorFactory = { listener ->
                TargetIoReactor(
                    binder = TargetSocketBinder {},
                    listener = listener,
                    backend = backend,
                )
            },
            onSessionFailure = { failure ->
                failures += failure
                bridge.shutdownAndAwait(2, TimeUnit.SECONDS)
            },
            maxStreams = 1,
        )
        assertTrue(bridge.start())
        bridge.open("stream", targetAddress())
        var opened: OutboundFrame? = null
        waitUntil { mailbox.poll()?.also { opened = it } != null }
        assertEquals(OutboundEmission.Emitted, mailbox.emit(requireNotNull(opened)) { true })
        backend.failWakeup.set(true)

        bridge.routeData("stream", byteArrayOf(1))

        waitUntil { failures.isNotEmpty() }
        assertEquals(listOf(ErrorClass.Internal), failures)
        assertTrue(bridge.shutdownAndAwait(2, TimeUnit.SECONDS))
        assertTrue(backend.closed.get())
        assertEquals(0, bridge.activeStreamCount)
    }

    @Test
    fun `selector backend ownership begins at start and factory failure leaks nothing`() {
        val created = AtomicInteger()
        val backend = FakeSelectorBackend()
        val listener = RecordingListener()
        val reactor = TargetIoReactor(
            binder = TargetSocketBinder {},
            listener = listener,
            backendFactory = {
                created.incrementAndGet()
                backend
            },
        )

        assertEquals(0, created.get())
        assertTrue(reactor.start())
        assertEquals(1, created.get())
        reactor.shutdown()
        assertTrue(reactor.awaitStopped(2, TimeUnit.SECONDS))
        assertTrue(backend.closed.get())

        val failed = TargetIoReactor(
            binder = TargetSocketBinder {},
            listener = RecordingListener(),
            backendFactory = { throw IllegalStateException("selector creation failed") },
        )
        assertFalse(failed.start())
        assertTrue(failed.awaitStopped(2, TimeUnit.SECONDS))
        assertEquals(
            0,
            Thread.getAllStackTraces().keys.count { it.name == TargetIoReactor.REACTOR_THREAD_NAME },
        )
    }

    @Test
    fun `non reactor shutdown barrier waits for channels backend and thread to stop`() {
        val connection = FakeConnection("stream", connectedImmediately = true)
        val backend = FakeSelectorBackend(connection)
        val listener = RecordingListener(openCount = 1, terminalCount = 1)
        val reactor = reactor(backend, listener)
        reactor.start()
        reactor.open("stream", targetAddress())
        assertTrue(listener.opens.await(2, TimeUnit.SECONDS))
        backend.blockClose.set(true)

        reactor.shutdown()

        assertTrue(backend.closeEntered.await(2, TimeUnit.SECONDS))
        assertFalse(reactor.awaitStopped(25, TimeUnit.MILLISECONDS))
        assertEquals(1, connection.closeCalls.get())
        backend.allowClose.countDown()
        assertTrue(reactor.awaitStopped(2, TimeUnit.SECONDS))
        assertTrue(backend.closed.get())
        assertTrue(
            Thread.getAllStackTraces().keys.none { it.name == TargetIoReactor.REACTOR_THREAD_NAME },
        )
    }

    @Test
    fun `shutdown racing backend creation cannot satisfy barrier before backend stops`() {
        val factoryEntered = CountDownLatch(1)
        val allowFactory = CountDownLatch(1)
        val backend = FakeSelectorBackend()
        val reactor = TargetIoReactor(
            binder = TargetSocketBinder {},
            listener = RecordingListener(),
            backendFactory = {
                factoryEntered.countDown()
                allowFactory.await(2, TimeUnit.SECONDS)
                backend
            },
        )
        val startReturned = AtomicBoolean(false)
        val barrierReturned = AtomicBoolean(false)
        val starter = Thread {
            reactor.start()
            startReturned.set(true)
        }.apply { start() }
        assertTrue(factoryEntered.await(2, TimeUnit.SECONDS))
        val closer = Thread {
            reactor.shutdown()
            barrierReturned.set(reactor.awaitStopped(2, TimeUnit.SECONDS))
        }.apply { start() }

        Thread.sleep(25)
        assertFalse(barrierReturned.get())
        allowFactory.countDown()
        starter.join(2_000)
        closer.join(2_000)

        assertTrue(startReturned.get())
        assertTrue(barrierReturned.get())
        assertTrue(backend.closed.get())
        assertTrue(
            Thread.getAllStackTraces().keys.none { it.name == TargetIoReactor.REACTOR_THREAD_NAME },
        )
    }

    @Test
    fun `shutdown requested from reactor terminal callback never joins itself`() {
        val backend = FakeSelectorBackend(FakeConnection("stream", connectedImmediately = true))
        val terminal = CountDownLatch(1)
        val reasons = Collections.synchronizedList(mutableListOf<TargetTerminalReason>())
        lateinit var reactor: TargetIoReactor
        val listener = object : TargetReactorListener {
            override fun onOpened(streamId: String, correlationToken: Long) = Unit
            override fun onData(streamId: String, correlationToken: Long, payload: ByteArray): Boolean = true
            override fun onBytesWritten(streamId: String, correlationToken: Long, byteCount: Int) = Unit
            override fun onTerminal(
                streamId: String,
                correlationToken: Long,
                reason: TargetTerminalReason,
            ) {
                reasons += reason
                reactor.shutdown()
                terminal.countDown()
            }
            override fun onReleased(streamId: String, correlationToken: Long) = Unit
            override fun onFatalFailure() = Unit
        }
        reactor = TargetIoReactor(TargetSocketBinder {}, listener, backend = backend)
        reactor.start()
        reactor.open("stream", targetAddress())
        backend.connection("stream")

        assertEquals(ReactorSubmitResult.Accepted, reactor.cancel("stream"))

        assertTrue(terminal.await(2, TimeUnit.SECONDS))
        assertTrue(reactor.awaitStopped(2, TimeUnit.SECONDS))
        assertEquals(listOf(TargetTerminalReason.Canceled), reasons)
    }

    @Test
    fun `outbound reads prefer sixteen KiB while one thirty two KiB inbound frame is accepted`() {
        val connection = FakeConnection("stream", connectedImmediately = true)
        val backend = FakeSelectorBackend(connection)
        val listener = RecordingListener(openCount = 1, dataCount = 2)
        val reactor = reactor(backend, listener)
        reactor.start()
        reactor.open("stream", targetAddress())
        assertTrue(listener.opens.await(2, TimeUnit.SECONDS))

        val outbound = ByteArray(32 * 1024) { (it % 251).toByte() }
        connection.enqueueRead(outbound)
        backend.ready("stream", readable = true)
        waitUntil { listener.receivedChunks.size == 1 }
        backend.ready("stream", readable = true)
        assertTrue(listener.data.await(2, TimeUnit.SECONDS))
        assertEquals(listOf(16 * 1024, 16 * 1024), listener.receivedChunks.map(ByteArray::size))
        assertEquals(outbound.toList(), listener.received.getValue("stream").toList())

        val inbound = ByteArray(32 * 1024) { (it % 239).toByte() }
        assertEquals(ReactorSubmitResult.Accepted, reactor.write("stream", inbound))
        waitUntil { connection.writeInterested.get() }
        backend.ready("stream", writable = true)
        waitUntil { connection.written().size == inbound.size }
        assertEquals(1, connection.writeCalls.get())
        assertEquals(inbound.toList(), connection.written().toList())

        reactor.shutdown()
        assertTrue(reactor.awaitStopped(2, TimeUnit.SECONDS))
    }

    private fun reactor(
        backend: FakeSelectorBackend,
        listener: RecordingListener,
        maxStreams: Int = 256,
        commandCapacity: Int = 512,
        connectTimeoutMillis: Long = 30_000,
        idleTimeoutMillis: Long = 5 * 60_000,
        nanoTime: () -> Long = System::nanoTime,
    ) = TargetIoReactor(
        binder = TargetSocketBinder {},
        listener = listener,
        maxStreams = maxStreams,
        commandCapacity = commandCapacity,
        connectTimeoutMillis = connectTimeoutMillis,
        idleTimeoutMillis = idleTimeoutMillis,
        backend = backend,
        nanoTime = nanoTime,
    )

    private fun targetAddress() = InetSocketAddress(InetAddress.getLoopbackAddress(), 9)

    private fun waitUntil(timeoutMillis: Long = 2_000, condition: () -> Boolean) {
        val deadline = System.nanoTime() + TimeUnit.MILLISECONDS.toNanos(timeoutMillis)
        while (!condition()) {
            if (System.nanoTime() >= deadline) throw AssertionError("condition was not met")
            Thread.sleep(2)
        }
    }

    private class RecordingListener(
        openCount: Int = 0,
        dataCount: Int = 0,
        terminalCount: Int = 0,
    ) : TargetReactorListener {
        val opens = CountDownLatch(openCount)
        val data = CountDownLatch(dataCount)
        val terminals = CountDownLatch(terminalCount)
        val opened = Collections.synchronizedList(mutableListOf<String>())
        val received = ConcurrentHashMap<String, ByteArray>()
        val receivedChunks = Collections.synchronizedList(mutableListOf<ByteArray>())
        val terminalReasons = ConcurrentHashMap<String, MutableList<TargetTerminalReason>>()
        val fatal = AtomicBoolean(false)
        val fatalCalls = AtomicInteger()

        override fun onOpened(streamId: String, correlationToken: Long) {
            opened += streamId
            opens.countDown()
        }

        override fun onData(streamId: String, correlationToken: Long, payload: ByteArray): Boolean {
            receivedChunks += payload
            received.merge(streamId, payload) { left, right -> left + right }
            data.countDown()
            return true
        }

        override fun onBytesWritten(streamId: String, correlationToken: Long, byteCount: Int) = Unit

        override fun onTerminal(
            streamId: String,
            correlationToken: Long,
            reason: TargetTerminalReason,
        ) {
            terminalReasons.computeIfAbsent(streamId) { Collections.synchronizedList(mutableListOf()) }.add(reason)
            terminals.countDown()
        }

        override fun onReleased(streamId: String, correlationToken: Long) = Unit

        override fun onFatalFailure() {
            fatal.set(true)
            fatalCalls.incrementAndGet()
        }
    }

    private class FakeSelectorBackend(vararg connections: FakeConnection) : TargetSelectorBackend {
        private val lock = Object()
        private val planned = ArrayDeque(connections.toList())
        private val opened = ConcurrentHashMap<String, FakeConnection>()
        private val ready = ArrayDeque<ReactorReady>()
        private var wakeup = false
        val closed = AtomicBoolean(false)
        val failSelect = AtomicBoolean(false)
        val failWakeup = AtomicBoolean(false)
        val openCalls = AtomicInteger()
        val blockClose = AtomicBoolean(false)
        val closeEntered = CountDownLatch(1)
        val allowClose = CountDownLatch(1)
        @Volatile var beforeOpen: (() -> Unit)? = null
        @Volatile var beforeSelectReturn: (() -> Unit)? = null

        override fun open(
            streamId: String,
            generation: Long,
            address: InetSocketAddress,
            binder: TargetSocketBinder,
        ): TargetReactorConnection {
            openCalls.incrementAndGet()
            beforeOpen?.invoke()
            val connection = synchronized(lock) { planned.removeFirst() }
            check(connection.plannedStreamId == streamId)
            connection.assign(generation)
            opened[streamId] = connection
            return connection
        }

        override fun select(timeoutMillis: Long): List<ReactorReady> = synchronized(lock) {
            if (ready.isEmpty() && !wakeup && !failSelect.get()) lock.wait(timeoutMillis)
            wakeup = false
            if (failSelect.get()) throw IllegalStateException("selector failed")
            beforeSelectReturn?.invoke()
            beforeSelectReturn = null
            buildList {
                while (ready.isNotEmpty()) add(ready.removeFirst())
            }
        }

        fun ready(
            streamId: String,
            connectable: Boolean = false,
            readable: Boolean = false,
            writable: Boolean = false,
        ) {
            val connection = connection(streamId)
            synchronized(lock) {
                ready += ReactorReady(connection, connectable, readable, writable)
                lock.notifyAll()
            }
        }

        fun readyBatch(streamIds: List<String>) {
            val connections = streamIds.map(::connection)
            synchronized(lock) {
                connections.forEach { connection ->
                    ready += ReactorReady(connection, writable = true)
                }
                lock.notifyAll()
            }
        }

        fun connection(streamId: String): FakeConnection {
            val deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(2)
            while (true) {
                opened[streamId]?.let { return it }
                if (System.nanoTime() >= deadline) throw AssertionError("connection $streamId was not opened")
                Thread.sleep(2)
            }
        }

        override fun wakeup() {
            synchronized(lock) {
                if (failWakeup.get()) throw IllegalStateException("selector wakeup failed")
                wakeup = true
                lock.notifyAll()
            }
        }

        override fun close() {
            closeEntered.countDown()
            if (blockClose.get()) allowClose.await(2, TimeUnit.SECONDS)
            closed.set(true)
            wakeup()
        }
    }

    private class MutableNanoClock {
        @Volatile private var value = 0L

        fun read(): Long = value

        fun advanceMillis(millis: Long) {
            value += TimeUnit.MILLISECONDS.toNanos(millis)
        }
    }

    private class FakeConnection(
        val plannedStreamId: String,
        override val connectedImmediately: Boolean,
        private val maxWriteBytes: Int = Int.MAX_VALUE,
        failInterests: Boolean = false,
    ) : TargetReactorConnection {
        private val readActions = ArrayDeque<ReadAction>()
        private var currentRead: ByteBuffer? = null
        private val output = ByteArrayOutputStream()
        override val streamId: String
            get() = plannedStreamId
        @Volatile override var generation: Long = -1
            private set
        val finishConnectCalls = AtomicInteger()
        val closeCalls = AtomicInteger()
        val writeCalls = AtomicInteger()
        val writeInterested = AtomicBoolean(false)
        val failInterestsNow = AtomicBoolean(failInterests)

        fun assign(value: Long) {
            generation = value
        }

        fun enqueueRead(payload: ByteArray) = synchronized(readActions) {
            readActions += ReadAction.Data(ByteBuffer.wrap(payload.copyOf()))
        }

        fun enqueueEof() = synchronized(readActions) { readActions += ReadAction.Eof }

        override fun finishConnect(): Boolean {
            finishConnectCalls.incrementAndGet()
            return true
        }

        override fun read(buffer: ByteBuffer): Int {
            val action = synchronized(readActions) {
                currentRead?.let { return@synchronized ReadAction.Data(it) }
                readActions.pollFirst()
            } ?: return 0
            if (action is ReadAction.Eof) return -1
            val source = (action as ReadAction.Data).buffer
            currentRead = source
            val count = minOf(buffer.remaining(), source.remaining())
            val bytes = ByteArray(count)
            source.get(bytes)
            buffer.put(bytes)
            if (!source.hasRemaining()) currentRead = null
            return count
        }

        override fun write(buffer: ByteBuffer): Int {
            writeCalls.incrementAndGet()
            val count = minOf(maxWriteBytes, buffer.remaining())
            if (count == 0) return 0
            val bytes = ByteArray(count)
            buffer.get(bytes)
            synchronized(output) { output.write(bytes) }
            return count
        }

        fun written(): ByteArray = synchronized(output) { output.toByteArray() }

        override fun setInterests(connect: Boolean, read: Boolean, write: Boolean) {
            if (failInterestsNow.get()) throw IllegalStateException("interest update failed")
            writeInterested.set(write)
        }

        override fun close() {
            closeCalls.incrementAndGet()
        }

        private sealed interface ReadAction {
            data class Data(val buffer: ByteBuffer) : ReadAction
            data object Eof : ReadAction
        }
    }
}
