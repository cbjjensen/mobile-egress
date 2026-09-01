package com.mobileegress.agent.session

import java.net.InetSocketAddress
import java.net.Socket
import java.nio.ByteBuffer
import java.nio.channels.SelectionKey
import java.nio.channels.Selector
import java.nio.channels.SocketChannel
import java.util.ArrayDeque
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean
import kotlin.math.max

internal fun interface TargetSocketBinder {
    fun bind(socket: Socket)
}

internal enum class ReactorSubmitResult {
    Accepted,
    StreamLimit,
    StreamSaturated,
    SessionSaturated,
    MissingOrClosed,
}

internal enum class TargetTerminalReason {
    TargetClosed,
    OpenSetupFailure,
    TargetFailure,
    IdleTimeout,
    Backpressure,
    Canceled,
    Shutdown,
}

internal interface TargetReactorListener {
    fun onOpened(streamId: String)
    fun onData(streamId: String, payload: ByteArray): Boolean
    fun onBytesWritten(streamId: String, byteCount: Int)
    fun onTerminal(streamId: String, reason: TargetTerminalReason)
    fun onFatalFailure()
}

internal class TargetIoReactor(
    private val binder: TargetSocketBinder,
    private val listener: TargetReactorListener,
    private val maxStreams: Int = MAX_STREAMS,
    private val commandCapacity: Int = COMMAND_CAPACITY,
    private val perStreamWriteCapacity: Int = PER_STREAM_WRITE_CAPACITY,
    private val readChunkBytes: Int = READ_CHUNK_BYTES,
    connectTimeoutMillis: Long = CONNECT_TIMEOUT_MILLIS,
    idleTimeoutMillis: Long = IDLE_TIMEOUT_MILLIS,
    private val backend: TargetSelectorBackend = NioTargetSelectorBackend(),
    private val nanoTime: () -> Long = System::nanoTime,
) {
    private val lock = Any()
    private val commands = ArrayDeque<ReactorCommand>()
    private val reservations = HashMap<String, Long>()
    private val outstandingWrites = HashMap<String, WriteCount>()
    private val saturatedStreams = HashMap<String, Long>()
    private val terminalSignals = HashMap<String, Long>()
    private val active = HashMap<String, ReactorStream>()
    private val started = AtomicBoolean(false)
    private val shutdownRequested = AtomicBoolean(false)
    private val stopped = CountDownLatch(1)
    private val connectTimeoutNanos = TimeUnit.MILLISECONDS.toNanos(connectTimeoutMillis)
    private val idleTimeoutNanos = TimeUnit.MILLISECONDS.toNanos(idleTimeoutMillis)
    private var nextGeneration = 1L
    @Volatile private var reactorThread: Thread? = null

    init {
        require(maxStreams > 0)
        require(commandCapacity > 0)
        require(perStreamWriteCapacity > 0)
        require(readChunkBytes in 1..READ_CHUNK_BYTES)
        require(connectTimeoutMillis > 0)
        require(idleTimeoutMillis > 0)
    }

    fun start() {
        if (!started.compareAndSet(false, true)) return
        Thread(::runLoop, REACTOR_THREAD_NAME).also { thread ->
            thread.isDaemon = true
            reactorThread = thread
            thread.start()
        }
    }

    fun open(streamId: String, address: InetSocketAddress): ReactorSubmitResult {
        val result = synchronized(lock) {
            if (shutdownRequested.get() || streamId in reservations) {
                return@synchronized ReactorSubmitResult.MissingOrClosed
            }
            if (reservations.size >= maxStreams) return@synchronized ReactorSubmitResult.StreamLimit
            if (commands.size >= commandCapacity) return@synchronized ReactorSubmitResult.SessionSaturated
            val generation = nextGeneration++
            reservations[streamId] = generation
            commands.addLast(
                ReactorCommand.Open(
                    streamId = streamId,
                    generation = generation,
                    address = address,
                    connectDeadlineNanos = deadlineAfter(nanoTime(), connectTimeoutNanos),
                ),
            )
            ReactorSubmitResult.Accepted
        }
        if (result == ReactorSubmitResult.Accepted) backend.wakeup()
        return result
    }

    fun write(streamId: String, payload: ByteArray): ReactorSubmitResult {
        val result = synchronized(lock) {
            val generation = reservations[streamId]
                ?: return@synchronized ReactorSubmitResult.MissingOrClosed
            if (shutdownRequested.get()) return@synchronized ReactorSubmitResult.MissingOrClosed
            if (terminalSignals[streamId] == generation) {
                return@synchronized ReactorSubmitResult.MissingOrClosed
            }
            if (saturatedStreams[streamId] == generation) {
                return@synchronized ReactorSubmitResult.StreamSaturated
            }
            val writeCount = outstandingWrites.getOrPut(streamId) { WriteCount(generation, 0) }
            if (writeCount.generation != generation) {
                outstandingWrites[streamId] = WriteCount(generation, 0)
            }
            val currentCount = outstandingWrites.getValue(streamId)
            if (currentCount.count >= perStreamWriteCapacity) {
                saturatedStreams[streamId] = generation
                return@synchronized ReactorSubmitResult.StreamSaturated
            }
            if (commands.size >= commandCapacity) {
                saturatedStreams[streamId] = generation
                return@synchronized ReactorSubmitResult.StreamSaturated
            }
            currentCount.count += 1
            commands.addLast(ReactorCommand.Write(streamId, generation, payload.copyOf()))
            ReactorSubmitResult.Accepted
        }
        if (result == ReactorSubmitResult.Accepted || result == ReactorSubmitResult.StreamSaturated) {
            backend.wakeup()
        }
        return result
    }

    fun cancel(streamId: String): ReactorSubmitResult {
        val result = synchronized(lock) {
            val generation = reservations[streamId]
                ?: return@synchronized ReactorSubmitResult.MissingOrClosed
            if (shutdownRequested.get()) return@synchronized ReactorSubmitResult.MissingOrClosed
            if (commands.size >= commandCapacity) return@synchronized ReactorSubmitResult.SessionSaturated
            commands.addLast(ReactorCommand.Cancel(streamId, generation))
            ReactorSubmitResult.Accepted
        }
        if (result == ReactorSubmitResult.Accepted) backend.wakeup()
        return result
    }

    fun release(streamId: String): ReactorSubmitResult {
        val result = synchronized(lock) {
            val generation = reservations[streamId]
                ?: return@synchronized ReactorSubmitResult.MissingOrClosed
            if (shutdownRequested.get() || terminalSignals[streamId] != generation) {
                return@synchronized ReactorSubmitResult.MissingOrClosed
            }
            if (commands.size >= commandCapacity) return@synchronized ReactorSubmitResult.SessionSaturated
            commands.addLast(ReactorCommand.Release(streamId, generation))
            ReactorSubmitResult.Accepted
        }
        if (result == ReactorSubmitResult.Accepted) backend.wakeup()
        return result
    }

    fun shutdown() {
        if (!shutdownRequested.compareAndSet(false, true)) return
        backend.wakeup()
        start()
    }

    fun awaitStopped(timeout: Long, unit: TimeUnit): Boolean {
        if (Thread.currentThread() === reactorThread) return false
        return stopped.await(timeout, unit)
    }

    private fun runLoop() {
        var fatalFailure = false
        try {
            while (!shutdownRequested.get()) {
                drainCommands()
                failSaturatedStreams()
                expireDeadlines()
                if (shutdownRequested.get()) break
                val ready = backend.select(nextSelectTimeoutMillis())
                ready.forEach(::handleReady)
            }
        } catch (_: Exception) {
            fatalFailure = !shutdownRequested.get()
        } finally {
            shutdownRequested.set(true)
            closeEverything(TargetTerminalReason.Shutdown)
            try {
                backend.close()
            } catch (_: Exception) {
                // Reactor teardown is bounded and best effort.
            }
            if (fatalFailure) {
                try {
                    listener.onFatalFailure()
                } catch (_: Exception) {
                    // The session is already being torn down.
                }
            }
            stopped.countDown()
        }
    }

    private fun drainCommands() {
        for (ignored in 0 until commandCapacity) {
            val command = synchronized(lock) { commands.pollFirst() } ?: break
            when (command) {
                is ReactorCommand.Open -> handleOpen(command)
                is ReactorCommand.Write -> handleWrite(command)
                is ReactorCommand.Cancel -> handleCancel(command)
                is ReactorCommand.Release -> handleRelease(command)
            }
        }
    }

    private fun handleOpen(command: ReactorCommand.Open) {
        if (!reservationMatches(command.streamId, command.generation)) return
        if (nanoTime() >= command.connectDeadlineNanos) {
            terminatePending(command.streamId, command.generation, TargetTerminalReason.TargetFailure)
            return
        }
        var openedStream: ReactorStream? = null
        try {
            val connection = backend.open(
                command.streamId,
                command.generation,
                command.address,
                binder,
            )
            val now = nanoTime()
            val stream = ReactorStream(
                id = command.streamId,
                generation = command.generation,
                connection = connection,
                connected = connection.connectedImmediately,
                connectDeadlineNanos = command.connectDeadlineNanos,
                idleDeadlineNanos = if (connection.connectedImmediately) {
                    deadlineAfter(now, idleTimeoutNanos)
                } else {
                    Long.MAX_VALUE
                },
                readBuffer = ByteBuffer.allocate(readChunkBytes),
            )
            active[command.streamId] = stream
            openedStream = stream
            if (stream.connected) listener.onOpened(stream.id)
            updateInterests(stream)
        } catch (error: Exception) {
            val reason = if (error is TargetOpenException && !error.connectInitiated) {
                TargetTerminalReason.OpenSetupFailure
            } else {
                TargetTerminalReason.TargetFailure
            }
            val stream = openedStream
            if (stream == null) {
                terminatePending(command.streamId, command.generation, reason)
            } else {
                terminateStream(stream, TargetTerminalReason.TargetFailure)
            }
        }
    }

    private fun handleWrite(command: ReactorCommand.Write) {
        val stream = active[command.streamId]
        if (stream == null || stream.generation != command.generation) {
            completeWrite(command.streamId, command.generation)
            return
        }
        stream.writes.addLast(ByteBuffer.wrap(command.payload))
        updateInterests(stream)
    }

    private fun handleCancel(command: ReactorCommand.Cancel) {
        val stream = active[command.streamId]
        if (stream != null && stream.generation == command.generation) {
            if (terminalWasSignaled(stream.id, stream.generation)) {
                closeWithoutTerminal(stream)
            } else {
                terminateStream(stream, TargetTerminalReason.Canceled)
            }
        } else if (reservationMatches(command.streamId, command.generation)) {
            if (terminalWasSignaled(command.streamId, command.generation)) {
                releaseReservation(command.streamId, command.generation)
            } else {
                terminatePending(command.streamId, command.generation, TargetTerminalReason.Canceled)
            }
        }
    }

    private fun handleRelease(command: ReactorCommand.Release) {
        val stream = active[command.streamId]
        if (
            stream != null &&
            stream.generation == command.generation &&
            terminalWasSignaled(stream.id, stream.generation)
        ) {
            closeWithoutTerminal(stream)
        }
    }

    private fun failSaturatedStreams() {
        val failures = synchronized(lock) { saturatedStreams.toMap() }
        failures.forEach { (streamId, generation) ->
            val stream = active[streamId]
            if (stream != null && stream.generation == generation) {
                terminateStream(stream, TargetTerminalReason.Backpressure)
            } else if (reservationMatches(streamId, generation)) {
                terminatePending(streamId, generation, TargetTerminalReason.Backpressure)
            }
        }
    }

    private fun handleReady(ready: ReactorReady) {
        val stream = active[ready.connection.streamId] ?: return
        if (stream.generation != ready.connection.generation || stream.connection !== ready.connection) return
        try {
            if (!stream.connected && ready.connectable && stream.connection.finishConnect()) {
                stream.connected = true
                stream.idleDeadlineNanos = deadlineAfter(nanoTime(), idleTimeoutNanos)
                listener.onOpened(stream.id)
            }
            if (stream.connected && ready.readable && active[stream.id] === stream) {
                readOnce(stream)
            }
            if (stream.connected && ready.writable && active[stream.id] === stream) {
                writeOnce(stream)
            }
            if (active[stream.id] === stream) updateInterests(stream)
        } catch (_: Exception) {
            if (active[stream.id] === stream) terminateStream(stream, TargetTerminalReason.TargetFailure)
        }
    }

    private fun readOnce(stream: ReactorStream) {
        val buffer = stream.readBuffer
        buffer.clear()
        val read = stream.connection.read(buffer)
        when {
            read < 0 -> signalTargetEof(stream)
            read == 0 -> Unit
            else -> {
                stream.idleDeadlineNanos = deadlineAfter(nanoTime(), idleTimeoutNanos)
                buffer.flip()
                val payload = ByteArray(read)
                buffer.get(payload)
                if (!listener.onData(stream.id, payload)) {
                    terminateStream(stream, TargetTerminalReason.Backpressure)
                }
            }
        }
    }

    private fun writeOnce(stream: ReactorStream) {
        val chunk = stream.writes.peekFirst() ?: return
        val written = stream.connection.write(chunk)
        if (written < 0) throw IllegalStateException("Negative target write")
        if (written > 0) {
            stream.idleDeadlineNanos = deadlineAfter(nanoTime(), idleTimeoutNanos)
            listener.onBytesWritten(stream.id, written)
        }
        if (!chunk.hasRemaining()) {
            stream.writes.removeFirst()
            completeWrite(stream.id, stream.generation)
        }
    }

    private fun updateInterests(stream: ReactorStream) {
        stream.connection.setInterests(
            connect = !stream.connected,
            read = stream.connected && !stream.readEof,
            write = stream.connected && stream.writes.isNotEmpty(),
        )
    }

    private fun expireDeadlines() {
        val now = nanoTime()
        active.values.toList().forEach { stream ->
            when {
                !stream.connected && now >= stream.connectDeadlineNanos -> {
                    terminateStream(stream, TargetTerminalReason.TargetFailure)
                }
                stream.connected && now >= stream.idleDeadlineNanos -> {
                    terminateStream(stream, TargetTerminalReason.IdleTimeout)
                }
            }
        }
    }

    private fun nextSelectTimeoutMillis(): Long {
        val now = nanoTime()
        val nearest = active.values.minOfOrNull { stream ->
            if (stream.connected) stream.idleDeadlineNanos else stream.connectDeadlineNanos
        } ?: return MAX_SELECT_MILLIS
        val remainingNanos = max(0L, nearest - now)
        val roundedUpMillis = (remainingNanos + NANOS_PER_MILLI - 1) / NANOS_PER_MILLI
        return roundedUpMillis.coerceIn(1L, MAX_SELECT_MILLIS)
    }

    private fun terminateStream(stream: ReactorStream, reason: TargetTerminalReason) {
        if (terminalWasSignaled(stream.id, stream.generation)) {
            closeWithoutTerminal(stream)
            return
        }
        if (!active.remove(stream.id, stream)) return
        try {
            stream.connection.close()
        } catch (_: Exception) {
            // The terminal result is already fixed; close is best effort.
        }
        releaseReservation(stream.id, stream.generation)
        listener.onTerminal(stream.id, reason)
    }

    private fun signalTargetEof(stream: ReactorStream) {
        if (stream.readEof || !markTerminalSignaled(stream.id, stream.generation)) return
        stream.readEof = true
        updateInterests(stream)
        listener.onTerminal(stream.id, TargetTerminalReason.TargetClosed)
    }

    private fun closeWithoutTerminal(stream: ReactorStream) {
        if (!active.remove(stream.id, stream)) return
        try {
            stream.connection.close()
        } catch (_: Exception) {
            // The stream has already emitted its one terminal result.
        }
        releaseReservation(stream.id, stream.generation)
    }

    private fun terminatePending(streamId: String, generation: Long, reason: TargetTerminalReason) {
        if (!releaseReservation(streamId, generation)) return
        listener.onTerminal(streamId, reason)
    }

    private fun closeEverything(reason: TargetTerminalReason) {
        active.values.toList().forEach { stream ->
            if (terminalWasSignaled(stream.id, stream.generation)) {
                closeWithoutTerminal(stream)
            } else {
                terminateStream(stream, reason)
            }
        }
        val pending = synchronized(lock) { reservations.toMap() }
        pending.forEach { (streamId, generation) ->
            if (terminalWasSignaled(streamId, generation)) {
                releaseReservation(streamId, generation)
            } else {
                terminatePending(streamId, generation, reason)
            }
        }
        synchronized(lock) { commands.clear() }
    }

    private fun reservationMatches(streamId: String, generation: Long): Boolean =
        synchronized(lock) { reservations[streamId] == generation }

    private fun releaseReservation(streamId: String, generation: Long): Boolean = synchronized(lock) {
        if (reservations[streamId] != generation) return@synchronized false
        reservations.remove(streamId)
        outstandingWrites.remove(streamId)
        saturatedStreams.remove(streamId)
        terminalSignals.remove(streamId)
        true
    }

    private fun markTerminalSignaled(streamId: String, generation: Long): Boolean = synchronized(lock) {
        if (reservations[streamId] != generation || terminalSignals[streamId] == generation) {
            return@synchronized false
        }
        terminalSignals[streamId] = generation
        true
    }

    private fun terminalWasSignaled(streamId: String, generation: Long): Boolean =
        synchronized(lock) { terminalSignals[streamId] == generation }

    private fun completeWrite(streamId: String, generation: Long) = synchronized(lock) {
        val count = outstandingWrites[streamId]
        if (count?.generation != generation) return@synchronized
        count.count -= 1
        if (count.count == 0) outstandingWrites.remove(streamId)
    }

    private fun deadlineAfter(now: Long, duration: Long): Long =
        if (now > Long.MAX_VALUE - duration) Long.MAX_VALUE else now + duration

    private sealed interface ReactorCommand {
        val streamId: String
        val generation: Long

        data class Open(
            override val streamId: String,
            override val generation: Long,
            val address: InetSocketAddress,
            val connectDeadlineNanos: Long,
        ) : ReactorCommand

        data class Write(
            override val streamId: String,
            override val generation: Long,
            val payload: ByteArray,
        ) : ReactorCommand

        data class Cancel(
            override val streamId: String,
            override val generation: Long,
        ) : ReactorCommand

        data class Release(
            override val streamId: String,
            override val generation: Long,
        ) : ReactorCommand
    }

    private data class WriteCount(val generation: Long, var count: Int)

    private data class ReactorStream(
        val id: String,
        val generation: Long,
        val connection: TargetReactorConnection,
        var connected: Boolean,
        val connectDeadlineNanos: Long,
        var idleDeadlineNanos: Long,
        val writes: ArrayDeque<ByteBuffer> = ArrayDeque(),
        val readBuffer: ByteBuffer,
        var readEof: Boolean = false,
    )

    internal companion object {
        const val MAX_STREAMS = AgentCapacity.MAX_STREAMS
        const val COMMAND_CAPACITY = AgentCapacity.REACTOR_COMMAND_CAPACITY
        const val PER_STREAM_WRITE_CAPACITY = AgentCapacity.TARGET_INBOUND_PER_STREAM_CAPACITY
        const val READ_CHUNK_BYTES = AgentCapacity.PREFERRED_TARGET_READ_BYTES
        const val CONNECT_TIMEOUT_MILLIS = 30_000L
        const val IDLE_TIMEOUT_MILLIS = 5 * 60_000L
        const val REACTOR_THREAD_NAME = "mobile-egress-target-reactor"
        private const val MAX_SELECT_MILLIS = 1_000L
        private const val NANOS_PER_MILLI = 1_000_000L
    }
}

internal interface TargetSelectorBackend : AutoCloseable {
    fun open(
        streamId: String,
        generation: Long,
        address: InetSocketAddress,
        binder: TargetSocketBinder,
    ): TargetReactorConnection

    fun select(timeoutMillis: Long): List<ReactorReady>
    fun wakeup()
}

internal interface TargetReactorConnection : AutoCloseable {
    val streamId: String
    val generation: Long
    val connectedImmediately: Boolean
    fun finishConnect(): Boolean
    fun read(buffer: ByteBuffer): Int
    fun write(buffer: ByteBuffer): Int
    fun setInterests(connect: Boolean, read: Boolean, write: Boolean)
}

internal data class ReactorReady(
    val connection: TargetReactorConnection,
    val connectable: Boolean = false,
    val readable: Boolean = false,
    val writable: Boolean = false,
)

private class NioTargetSelectorBackend : TargetSelectorBackend {
    private val selector = Selector.open()

    override fun open(
        streamId: String,
        generation: Long,
        address: InetSocketAddress,
        binder: TargetSocketBinder,
    ): TargetReactorConnection {
        var channel: SocketChannel? = null
        var connectInitiated = false
        try {
            channel = SocketChannel.open()
            binder.bind(channel.socket())
            channel.configureBlocking(false)
            connectInitiated = true
            val connectedImmediately = channel.connect(address)
            if (connectedImmediately) channel.socket().tcpNoDelay = true
            val key = channel.register(
                selector,
                if (connectedImmediately) SelectionKey.OP_READ else SelectionKey.OP_CONNECT,
            )
            return NioTargetConnection(streamId, generation, channel, key, connectedImmediately).also {
                key.attach(it)
            }
        } catch (error: Exception) {
            channel?.close()
            throw TargetOpenException(connectInitiated, error)
        }
    }

    override fun select(timeoutMillis: Long): List<ReactorReady> {
        selector.select(timeoutMillis)
        val ready = ArrayList<ReactorReady>(selector.selectedKeys().size)
        val keys = selector.selectedKeys().iterator()
        while (keys.hasNext()) {
            val key = keys.next()
            keys.remove()
            if (!key.isValid) continue
            val connection = key.attachment() as? NioTargetConnection ?: continue
            ready += ReactorReady(
                connection = connection,
                connectable = key.isConnectable,
                readable = key.isReadable,
                writable = key.isWritable,
            )
        }
        return ready
    }

    override fun wakeup() {
        selector.wakeup()
    }

    override fun close() {
        selector.keys().toList().forEach { key ->
            try {
                key.channel().close()
            } catch (_: Exception) {
                // Best effort during bounded reactor teardown.
            }
        }
        selector.close()
    }

    private class NioTargetConnection(
        override val streamId: String,
        override val generation: Long,
        private val channel: SocketChannel,
        private val key: SelectionKey,
        override val connectedImmediately: Boolean,
    ) : TargetReactorConnection {
        override fun finishConnect(): Boolean = channel.finishConnect().also { connected ->
            if (connected) channel.socket().tcpNoDelay = true
        }

        override fun read(buffer: ByteBuffer): Int = channel.read(buffer)

        override fun write(buffer: ByteBuffer): Int = channel.write(buffer)

        override fun setInterests(connect: Boolean, read: Boolean, write: Boolean) {
            if (!key.isValid) return
            var interests = 0
            if (connect) interests = interests or SelectionKey.OP_CONNECT
            if (read) interests = interests or SelectionKey.OP_READ
            if (write) interests = interests or SelectionKey.OP_WRITE
            key.interestOps(interests)
        }

        override fun close() {
            key.cancel()
            channel.close()
        }
    }
}

private class TargetOpenException(
    val connectInitiated: Boolean,
    cause: Exception,
) : Exception(cause)
