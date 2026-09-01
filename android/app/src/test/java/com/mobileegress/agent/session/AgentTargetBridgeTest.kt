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

    private class Fixture(
        outbound: OutboundMailbox = OutboundMailbox(),
        openResult: ReactorSubmitResult = ReactorSubmitResult.Accepted,
        maxStreams: Int = 256,
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
        )
        private val mailbox = outbound

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
        val writes = Collections.synchronizedList(mutableListOf<Write>())
        val cancels = Collections.synchronizedList(mutableListOf<Call>())
        val releases = Collections.synchronizedList(mutableListOf<Call>())
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
            return openResult
        }

        override fun write(
            streamId: String,
            correlationToken: Long,
            payload: ByteArray,
        ): ReactorSubmitResult {
            writes += Write(streamId, correlationToken, payload.copyOf())
            return ReactorSubmitResult.Accepted
        }

        override fun cancel(streamId: String, correlationToken: Long): ReactorSubmitResult {
            cancels += Call(streamId, correlationToken)
            return ReactorSubmitResult.Accepted
        }

        override fun release(streamId: String, correlationToken: Long): ReactorSubmitResult {
            releases += Call(streamId, correlationToken)
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
