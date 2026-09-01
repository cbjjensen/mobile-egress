package com.mobileegress.agent.session

import java.net.InetAddress
import java.net.InetSocketAddress
import java.net.Socket
import java.nio.ByteBuffer
import java.nio.channels.SelectionKey
import java.nio.channels.Selector
import java.nio.channels.ServerSocketChannel
import java.nio.channels.SocketChannel
import java.util.Collections
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class TargetIoReactorIntegrationTest {
    @Test
    fun `two hundred fifty six loopback targets exchange data and target two hundred fifty seven is rejected`() {
        LoopbackEchoServer().use { server ->
            val listener = RecordingTargetListener(expectedOpens = 256, expectedData = 256)
            val reactor = TargetIoReactor(TargetSocketBinder {}, listener)
            reactor.start()

            repeat(256) { index ->
                assertEquals(
                    ReactorSubmitResult.Accepted,
                    reactor.open("stream-$index", server.address),
                )
            }
            assertEquals(
                ReactorSubmitResult.StreamLimit,
                reactor.open("stream-256", server.address),
            )
            assertTrue(listener.opens.await(10, TimeUnit.SECONDS))
            assertEquals(
                1,
                Thread.getAllStackTraces().keys.count { it.name == TargetIoReactor.REACTOR_THREAD_NAME },
            )

            repeat(256) { index ->
                assertEquals(
                    ReactorSubmitResult.Accepted,
                    reactor.write("stream-$index", byteArrayOf(index.toByte())),
                )
            }

            assertTrue(listener.data.await(10, TimeUnit.SECONDS))
            repeat(256) { index ->
                assertEquals(listOf(index.toByte()), listener.received.getValue("stream-$index").toList())
            }

            reactor.shutdown()
            assertTrue(reactor.awaitStopped(5, TimeUnit.SECONDS))
            waitUntil {
                Thread.getAllStackTraces().keys.none { it.name == TargetIoReactor.REACTOR_THREAD_NAME }
            }
        }
    }

    @Test
    fun `released reactor slot can be reused`() {
        LoopbackEchoServer().use { server ->
            val listener = RecordingTargetListener(expectedOpens = 2, expectedTerminals = 1)
            val reactor = TargetIoReactor(
                binder = TargetSocketBinder {},
                listener = listener,
                maxStreams = 1,
            )
            reactor.start()

            assertEquals(ReactorSubmitResult.Accepted, reactor.open("first", server.address))
            assertTrue(listener.firstOpen.await(5, TimeUnit.SECONDS))
            assertEquals(ReactorSubmitResult.StreamLimit, reactor.open("blocked", server.address))
            assertEquals(ReactorSubmitResult.Accepted, reactor.cancel("first"))
            assertTrue(listener.terminals.await(5, TimeUnit.SECONDS))
            assertEquals(listOf(TargetTerminalReason.Canceled), listener.terminalReasons["first"])

            assertEquals(ReactorSubmitResult.Accepted, reactor.open("replacement", server.address))
            assertTrue(listener.opens.await(5, TimeUnit.SECONDS))

            reactor.shutdown()
            assertTrue(reactor.awaitStopped(5, TimeUnit.SECONDS))
        }
    }

    @Test
    fun `cellular binding sees the channel socket before connect`() {
        LoopbackEchoServer().use { server ->
            val bound = CountDownLatch(1)
            val listener = RecordingTargetListener(expectedOpens = 1)
            val reactor = TargetIoReactor(
                binder = TargetSocketBinder { socket ->
                    assertFalse(socket.isConnected)
                    assertFalse(socket.channel?.isConnectionPending ?: false)
                    bound.countDown()
                },
                listener = listener,
            )
            reactor.start()

            assertEquals(ReactorSubmitResult.Accepted, reactor.open("bound", server.address))

            assertTrue(bound.await(5, TimeUnit.SECONDS))
            assertTrue(listener.opens.await(5, TimeUnit.SECONDS))
            reactor.shutdown()
            assertTrue(reactor.awaitStopped(5, TimeUnit.SECONDS))
        }
    }

    @Test
    fun `binding failure before connect is reported as open setup failure exactly once`() {
        val listener = RecordingTargetListener(expectedTerminals = 1)
        val reactor = TargetIoReactor(
            binder = TargetSocketBinder { throw IllegalStateException("bind failed") },
            listener = listener,
        )
        reactor.start()

        assertEquals(
            ReactorSubmitResult.Accepted,
            reactor.open("bind-failure", InetSocketAddress(InetAddress.getLoopbackAddress(), 9)),
        )

        assertTrue(listener.terminals.await(5, TimeUnit.SECONDS))
        assertEquals(listOf(TargetTerminalReason.OpenSetupFailure), listener.terminalReasons["bind-failure"])
        assertEquals(0, listener.opens.count)
        reactor.shutdown()
        assertTrue(reactor.awaitStopped(5, TimeUnit.SECONDS))
        assertEquals(1, listener.terminalReasons.getValue("bind-failure").size)
    }

    private class RecordingTargetListener(
        expectedOpens: Int = 0,
        expectedData: Int = 0,
        expectedTerminals: Int = 0,
    ) : TargetReactorListener {
        val opens = CountDownLatch(expectedOpens)
        val firstOpen = CountDownLatch(if (expectedOpens > 0) 1 else 0)
        val data = CountDownLatch(expectedData)
        val terminals = CountDownLatch(expectedTerminals)
        val received = ConcurrentHashMap<String, ByteArray>()
        val terminalReasons = ConcurrentHashMap<String, MutableList<TargetTerminalReason>>()

        override fun onOpened(streamId: String) {
            firstOpen.countDown()
            opens.countDown()
        }

        override fun onData(streamId: String, payload: ByteArray): Boolean {
            received.merge(streamId, payload) { left, right -> left + right }
            data.countDown()
            return true
        }

        override fun onBytesWritten(streamId: String, byteCount: Int) = Unit

        override fun onTerminal(streamId: String, reason: TargetTerminalReason) {
            terminalReasons.computeIfAbsent(streamId) { Collections.synchronizedList(mutableListOf()) }.add(reason)
            terminals.countDown()
        }

        override fun onFatalFailure() = Unit
    }

    private fun waitUntil(timeoutMillis: Long = 2_000, condition: () -> Boolean) {
        val deadline = System.nanoTime() + TimeUnit.MILLISECONDS.toNanos(timeoutMillis)
        while (!condition()) {
            if (System.nanoTime() >= deadline) throw AssertionError("condition was not met")
            Thread.sleep(2)
        }
    }

    private class LoopbackEchoServer : AutoCloseable {
        private val selector = Selector.open()
        private val server = ServerSocketChannel.open().apply {
            configureBlocking(false)
            bind(InetSocketAddress(InetAddress.getLoopbackAddress(), 0), 256)
            register(selector, SelectionKey.OP_ACCEPT)
        }
        private val running = java.util.concurrent.atomic.AtomicBoolean(true)
        private val channels = ConcurrentHashMap.newKeySet<SocketChannel>()
        private val thread = Thread(::run, "test-loopback-echo").apply {
            isDaemon = true
            start()
        }

        val address: InetSocketAddress
            get() = server.localAddress as InetSocketAddress

        private fun run() {
            val buffer = ByteBuffer.allocate(16 * 1024)
            while (running.get()) {
                selector.select(100)
                val keys = selector.selectedKeys().iterator()
                while (keys.hasNext()) {
                    val key = keys.next()
                    keys.remove()
                    if (!key.isValid) continue
                    if (key.isAcceptable) {
                        while (true) {
                            val accepted = server.accept() ?: break
                            accepted.configureBlocking(false)
                            accepted.register(selector, SelectionKey.OP_READ)
                            channels += accepted
                        }
                    } else if (key.isReadable) {
                        val channel = key.channel() as SocketChannel
                        buffer.clear()
                        val read = channel.read(buffer)
                        if (read < 0) {
                            channels -= channel
                            key.cancel()
                            channel.close()
                        } else if (read > 0) {
                            buffer.flip()
                            while (buffer.hasRemaining()) channel.write(buffer)
                        }
                    }
                }
            }
        }

        override fun close() {
            running.set(false)
            selector.wakeup()
            thread.join(2_000)
            channels.forEach(SocketChannel::close)
            server.close()
            selector.close()
        }
    }
}
