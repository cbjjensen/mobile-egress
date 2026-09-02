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
    fun onOpened(streamId: String, correlationToken: Long)
    fun onData(streamId: String, correlationToken: Long, payload: ByteArray): Boolean
    fun onBytesWritten(streamId: String, correlationToken: Long, byteCount: Int)
    fun onTerminal(streamId: String, correlationToken: Long, reason: TargetTerminalReason)
    fun onReleased(streamId: String, correlationToken: Long)
    fun onFatalFailure()
}

internal interface TargetReactorPort {
    fun start(): Boolean
    fun open(streamId: String, correlationToken: Long, address: InetSocketAddress): ReactorSubmitResult
    fun write(streamId: String, correlationToken: Long, payload: ByteArray): ReactorSubmitResult
    fun cancel(streamId: String, correlationToken: Long): ReactorSubmitResult
    fun release(streamId: String, correlationToken: Long): ReactorSubmitResult
    fun shutdown()
    fun awaitStopped(timeout: Long, unit: TimeUnit): Boolean
    fun isReactorThread(): Boolean
}

internal data class TargetIoReactorSnapshot(
    val queuedCommands: Int,
    val queuedDataCommands: Int,
    val outstandingWriteFrames: Int,
    val outstandingWriteBytes: Long,
)

internal class TargetIoReactor(
    private val binder: TargetSocketBinder,
    private val listener: TargetReactorListener,
    private val maxStreams: Int = MAX_STREAMS,
    private val dataCommandCapacity: Int = DATA_COMMAND_CAPACITY,
    private val totalCommandCapacity: Int = COMMAND_CAPACITY,
    private val commandsPerCycle: Int = COMMANDS_PER_CYCLE,
    private val perStreamWriteCapacity: Int = PER_STREAM_WRITE_CAPACITY,
    private val sessionWriteCapacity: Int = SESSION_WRITE_CAPACITY,
    private val sessionWriteByteCapacity: Long = SESSION_WRITE_BYTE_CAPACITY,
    private val readChunkBytes: Int = READ_CHUNK_BYTES,
    connectTimeoutMillis: Long = CONNECT_TIMEOUT_MILLIS,
    idleTimeoutMillis: Long = IDLE_TIMEOUT_MILLIS,
    backend: TargetSelectorBackend? = null,
    private val backendFactory: () -> TargetSelectorBackend = { NioTargetSelectorBackend() },
    private val nanoTime: () -> Long = System::nanoTime,
    private val backpressureReporter: BackpressureReporter = LogcatBackpressureReporter,
) : TargetReactorPort {
    private val lock = Any()
    private val lifecycleLock = Any()
    private val commands = ArrayDeque<ReactorCommand>()
    private var queuedDataCommands = 0
    private val reservations = HashMap<String, Reservation>()
    private val outstandingWrites = HashMap<String, WriteCount>()
    private var outstandingWriteFrames = 0
    private var outstandingWriteBytes = 0L
    private val saturatedStreams = HashMap<String, Long>()
    private val terminalSignals = HashMap<String, Long>()
    private val releaseRequests = HashMap<String, Long>()
    private val active = HashMap<String, ReactorStream>()
    private val started = AtomicBoolean(false)
    private val shutdownRequested = AtomicBoolean(false)
    private val fatalFailureRequested = AtomicBoolean(false)
    private val stopped = CountDownLatch(1)
    private val connectTimeoutNanos = TimeUnit.MILLISECONDS.toNanos(connectTimeoutMillis)
    private val idleTimeoutNanos = TimeUnit.MILLISECONDS.toNanos(idleTimeoutMillis)
    private var nextGeneration = 1L
    private var nextImplicitCorrelation = 1L
    private val suppliedBackend = backend
    @Volatile private var activeBackend: TargetSelectorBackend? = null
    @Volatile private var reactorThread: Thread? = null

    init {
        require(maxStreams > 0)
        require(dataCommandCapacity > 0)
        require(totalCommandCapacity >= dataCommandCapacity)
        require(commandsPerCycle > 0)
        require(perStreamWriteCapacity > 0)
        require(sessionWriteCapacity > 0)
        require(sessionWriteByteCapacity > 0)
        require(readChunkBytes in 1..READ_CHUNK_BYTES)
        require(connectTimeoutMillis > 0)
        require(idleTimeoutMillis > 0)
    }

    override fun start(): Boolean {
        var startupFailed = false
        var failedBackend: TargetSelectorBackend? = null
        val result = synchronized(lifecycleLock) {
            if (shutdownRequested.get()) return@synchronized false
            if (started.get()) return@synchronized activeBackend != null
            val selectedBackend = try {
                suppliedBackend ?: backendFactory()
            } catch (_: Exception) {
                shutdownRequested.set(true)
                startupFailed = true
                return@synchronized false
            }
            val thread = Thread(::runLoop, REACTOR_THREAD_NAME).also { it.isDaemon = true }
            activeBackend = selectedBackend
            reactorThread = thread
            started.set(true)
            try {
                thread.start()
                true
            } catch (_: Exception) {
                shutdownRequested.set(true)
                activeBackend = null
                reactorThread = null
                failedBackend = selectedBackend
                startupFailed = true
                false
            }
        }
        if (startupFailed) completeStartupFailure(failedBackend)
        return result
    }

    private fun completeStartupFailure(backend: TargetSelectorBackend?) {
        shutdownRequested.set(true)
        try {
            closeEverything(TargetTerminalReason.Shutdown)
        } finally {
            try {
                backend?.close()
            } catch (_: Exception) {
                // A failed startup still owns and closes its selected backend.
            } finally {
                stopped.countDown()
            }
        }
    }

    fun open(streamId: String, address: InetSocketAddress): ReactorSubmitResult {
        val correlationToken = synchronized(lock) { nextImplicitCorrelation++ }
        return open(streamId, correlationToken, address)
    }

    override fun open(
        streamId: String,
        correlationToken: Long,
        address: InetSocketAddress,
    ): ReactorSubmitResult {
        var saturationSource: BackpressureSource? = null
        val result = synchronized(lock) {
            if (shutdownRequested.get() || streamId in reservations) {
                return@synchronized ReactorSubmitResult.MissingOrClosed
            }
            if (reservations.size >= maxStreams) return@synchronized ReactorSubmitResult.StreamLimit
            if (commands.size >= totalCommandCapacity) {
                saturationSource = BackpressureSource.RequiredControlSaturation
                return@synchronized ReactorSubmitResult.SessionSaturated
            }
            val generation = nextGeneration++
            reservations[streamId] = Reservation(generation, correlationToken)
            enqueueCommand(
                ReactorCommand.Open(
                    streamId = streamId,
                    generation = generation,
                    correlationToken = correlationToken,
                    address = address,
                    connectDeadlineNanos = deadlineAfter(nanoTime(), connectTimeoutNanos),
                ),
            )
            ReactorSubmitResult.Accepted
        }
        saturationSource?.let(backpressureReporter::report)
        if (result == ReactorSubmitResult.Accepted) wakeup()
        return result
    }

    fun write(streamId: String, payload: ByteArray): ReactorSubmitResult {
        val correlationToken = synchronized(lock) {
            reservations[streamId]?.correlationToken
                ?: return ReactorSubmitResult.MissingOrClosed
        }
        return write(streamId, correlationToken, payload)
    }

    override fun write(
        streamId: String,
        correlationToken: Long,
        payload: ByteArray,
    ): ReactorSubmitResult {
        var saturationSource: BackpressureSource? = null
        val result = synchronized(lock) {
            val reservation = reservations[streamId]
                ?: return@synchronized ReactorSubmitResult.MissingOrClosed
            if (reservation.correlationToken != correlationToken) {
                return@synchronized ReactorSubmitResult.MissingOrClosed
            }
            val generation = reservation.generation
            if (shutdownRequested.get()) return@synchronized ReactorSubmitResult.MissingOrClosed
            if (releaseRequests[streamId] == generation) {
                return@synchronized ReactorSubmitResult.MissingOrClosed
            }
            if (saturatedStreams[streamId] == generation) {
                return@synchronized ReactorSubmitResult.StreamSaturated
            }
            val writeCount = outstandingWrites.getOrPut(streamId) { WriteCount(generation) }
            if (writeCount.generation != generation) {
                outstandingWrites[streamId] = WriteCount(generation)
            }
            val currentCount = outstandingWrites.getValue(streamId)
            if (currentCount.frames >= perStreamWriteCapacity) {
                saturatedStreams[streamId] = generation
                saturationSource = BackpressureSource.TargetPerStreamLimit
                return@synchronized ReactorSubmitResult.StreamSaturated
            }
            if (outstandingWriteFrames >= sessionWriteCapacity) {
                saturatedStreams[streamId] = generation
                saturationSource = BackpressureSource.TargetSessionFrameLimit
                return@synchronized ReactorSubmitResult.StreamSaturated
            }
            if (payload.size.toLong() > sessionWriteByteCapacity - outstandingWriteBytes) {
                saturatedStreams[streamId] = generation
                saturationSource = BackpressureSource.TargetSessionByteLimit
                return@synchronized ReactorSubmitResult.StreamSaturated
            }
            if (queuedDataCommands >= dataCommandCapacity || commands.size >= totalCommandCapacity) {
                saturatedStreams[streamId] = generation
                saturationSource = BackpressureSource.TargetCommandQueue
                return@synchronized ReactorSubmitResult.StreamSaturated
            }
            currentCount.frames += 1
            currentCount.bytes += payload.size.toLong()
            outstandingWriteFrames += 1
            outstandingWriteBytes += payload.size.toLong()
            enqueueCommand(ReactorCommand.Write(streamId, generation, payload.copyOf()))
            ReactorSubmitResult.Accepted
        }
        saturationSource?.let(backpressureReporter::report)
        if (result == ReactorSubmitResult.Accepted || result == ReactorSubmitResult.StreamSaturated) {
            wakeup()
        }
        return result
    }

    fun cancel(streamId: String): ReactorSubmitResult {
        val correlationToken = synchronized(lock) {
            reservations[streamId]?.correlationToken
                ?: return ReactorSubmitResult.MissingOrClosed
        }
        return cancel(streamId, correlationToken)
    }

    override fun cancel(streamId: String, correlationToken: Long): ReactorSubmitResult {
        var saturationSource: BackpressureSource? = null
        val result = synchronized(lock) {
            val reservation = reservations[streamId]
                ?: return@synchronized ReactorSubmitResult.MissingOrClosed
            if (reservation.correlationToken != correlationToken) {
                return@synchronized ReactorSubmitResult.MissingOrClosed
            }
            val generation = reservation.generation
            if (shutdownRequested.get()) return@synchronized ReactorSubmitResult.MissingOrClosed
            if (commands.size >= totalCommandCapacity) {
                saturationSource = BackpressureSource.RequiredControlSaturation
                return@synchronized ReactorSubmitResult.SessionSaturated
            }
            enqueueCommand(ReactorCommand.Cancel(streamId, generation))
            ReactorSubmitResult.Accepted
        }
        saturationSource?.let(backpressureReporter::report)
        if (result == ReactorSubmitResult.Accepted) wakeup()
        return result
    }

    fun release(streamId: String): ReactorSubmitResult {
        val correlationToken = synchronized(lock) {
            reservations[streamId]?.correlationToken
                ?: return ReactorSubmitResult.MissingOrClosed
        }
        return release(streamId, correlationToken)
    }

    override fun release(streamId: String, correlationToken: Long): ReactorSubmitResult {
        var saturationSource: BackpressureSource? = null
        val result = synchronized(lock) {
            val reservation = reservations[streamId]
                ?: return@synchronized ReactorSubmitResult.MissingOrClosed
            if (reservation.correlationToken != correlationToken) {
                return@synchronized ReactorSubmitResult.MissingOrClosed
            }
            val generation = reservation.generation
            if (shutdownRequested.get() || terminalSignals[streamId] != generation) {
                return@synchronized ReactorSubmitResult.MissingOrClosed
            }
            if (releaseRequests[streamId] == generation) return@synchronized ReactorSubmitResult.Accepted
            if (commands.size >= totalCommandCapacity) {
                saturationSource = BackpressureSource.RequiredControlSaturation
                return@synchronized ReactorSubmitResult.SessionSaturated
            }
            releaseRequests[streamId] = generation
            enqueueCommand(ReactorCommand.Release(streamId, generation))
            ReactorSubmitResult.Accepted
        }
        saturationSource?.let(backpressureReporter::report)
        if (result == ReactorSubmitResult.Accepted) wakeup()
        return result
    }

    override fun shutdown() {
        val closeInline = synchronized(lifecycleLock) {
            if (!shutdownRequested.compareAndSet(false, true)) return
            !started.get()
        }
        if (closeInline) {
            closeWithoutStarting()
            return
        }
        wakeup()
    }

    override fun awaitStopped(timeout: Long, unit: TimeUnit): Boolean {
        if (Thread.currentThread() === reactorThread) return false
        return stopped.await(timeout, unit)
    }

    override fun isReactorThread(): Boolean = Thread.currentThread() === reactorThread

    private fun runLoop() {
        var fatalFailure = false
        val backend = requireNotNull(activeBackend)
        try {
            while (!shutdownRequested.get()) {
                drainCommands()
                failSaturatedStreams()
                expireDeadlines()
                if (shutdownRequested.get()) break
                val ready = backend.select(nextSelectTimeoutMillis())
                expireDeadlines()
                if (shutdownRequested.get()) break
                ready.forEach(::handleReady)
            }
        } catch (_: Exception) {
            fatalFailure = !shutdownRequested.get() || fatalFailureRequested.get()
        } finally {
            fatalFailure = fatalFailure || fatalFailureRequested.get()
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

    private fun closeWithoutStarting() {
        try {
            closeEverything(TargetTerminalReason.Shutdown)
            suppliedBackend?.close()
        } catch (_: Exception) {
            // A never-started reactor still has a bounded best-effort teardown path.
        } finally {
            stopped.countDown()
        }
    }

    private fun wakeup() {
        try {
            activeBackend?.wakeup()
        } catch (_: Exception) {
            if (!shutdownRequested.get()) fatalFailureRequested.set(true)
            shutdownRequested.set(true)
        }
    }

    internal fun snapshot(): TargetIoReactorSnapshot = synchronized(lock) {
        TargetIoReactorSnapshot(
            queuedCommands = commands.size,
            queuedDataCommands = queuedDataCommands,
            outstandingWriteFrames = outstandingWriteFrames,
            outstandingWriteBytes = outstandingWriteBytes,
        )
    }

    private fun drainCommands() {
        for (ignored in 0 until commandsPerCycle) {
            val command = synchronized(lock) {
                commands.pollFirst()?.also { dequeued ->
                    if (dequeued is ReactorCommand.Write) {
                        queuedDataCommands -= 1
                        check(queuedDataCommands >= 0)
                    }
                }
            } ?: break
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
            val connection = requireNotNull(activeBackend).open(
                command.streamId,
                command.generation,
                command.address,
                binder,
            )
            val now = nanoTime()
            if (now >= command.connectDeadlineNanos) {
                connection.close()
                terminatePending(command.streamId, command.generation, TargetTerminalReason.TargetFailure)
                return
            }
            val stream = ReactorStream(
                id = command.streamId,
                generation = command.generation,
                correlationToken = command.correlationToken,
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
            updateInterests(stream)
            if (stream.connected) listener.onOpened(stream.id, stream.correlationToken)
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
            completeWrite(command.streamId, command.generation, command.payload.size)
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
                val reservation = releaseReservation(command.streamId, command.generation)
                if (reservation != null) {
                    listener.onReleased(command.streamId, reservation.correlationToken)
                }
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
            stream.releaseAfterWrites = true
            if (stream.writes.isEmpty()) {
                closeWithoutTerminal(stream)
            } else {
                updateInterests(stream)
            }
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
                if (nanoTime() >= stream.connectDeadlineNanos) {
                    terminateStream(stream, TargetTerminalReason.TargetFailure)
                    return
                }
                stream.connected = true
                stream.idleDeadlineNanos = deadlineAfter(nanoTime(), idleTimeoutNanos)
                listener.onOpened(stream.id, stream.correlationToken)
                if (shutdownRequested.get()) return
            }
            if (stream.connected && ready.readable && active[stream.id] === stream) {
                readOnce(stream)
                if (shutdownRequested.get()) return
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
                if (!listener.onData(stream.id, stream.correlationToken, payload)) {
                    backpressureReporter.report(BackpressureSource.TargetOutboundMailbox)
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
            listener.onBytesWritten(stream.id, stream.correlationToken, written)
        }
        if (!chunk.hasRemaining()) {
            stream.writes.removeFirst()
            completeWrite(stream.id, stream.generation, chunk.capacity())
            if (stream.releaseAfterWrites && stream.writes.isEmpty()) {
                closeWithoutTerminal(stream)
            }
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
        listener.onTerminal(stream.id, stream.correlationToken, reason)
        listener.onReleased(stream.id, stream.correlationToken)
    }

    private fun signalTargetEof(stream: ReactorStream) {
        if (stream.readEof || terminalWasSignaled(stream.id, stream.generation)) return
        stream.readEof = true
        updateInterests(stream)
        if (!markTerminalSignaled(stream.id, stream.generation)) return
        listener.onTerminal(stream.id, stream.correlationToken, TargetTerminalReason.TargetClosed)
    }

    private fun closeWithoutTerminal(stream: ReactorStream) {
        if (!active.remove(stream.id, stream)) return
        try {
            stream.connection.close()
        } catch (_: Exception) {
            // The stream has already emitted its one terminal result.
        }
        releaseReservation(stream.id, stream.generation)
        listener.onReleased(stream.id, stream.correlationToken)
    }

    private fun terminatePending(streamId: String, generation: Long, reason: TargetTerminalReason) {
        val reservation = releaseReservation(streamId, generation) ?: return
        listener.onTerminal(streamId, reservation.correlationToken, reason)
        listener.onReleased(streamId, reservation.correlationToken)
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
        pending.forEach { (streamId, reservation) ->
            val generation = reservation.generation
            if (terminalWasSignaled(streamId, generation)) {
                val released = releaseReservation(streamId, generation)
                if (released != null) listener.onReleased(streamId, released.correlationToken)
            } else {
                terminatePending(streamId, generation, reason)
            }
        }
        synchronized(lock) {
            commands.clear()
            queuedDataCommands = 0
        }
    }

    private fun enqueueCommand(command: ReactorCommand) {
        commands.addLast(command)
        if (command is ReactorCommand.Write) queuedDataCommands += 1
    }

    private fun reservationMatches(streamId: String, generation: Long): Boolean =
        synchronized(lock) { reservations[streamId]?.generation == generation }

    private fun releaseReservation(streamId: String, generation: Long): Reservation? = synchronized(lock) {
        val reservation = reservations[streamId]
        if (reservation?.generation != generation) return@synchronized null
        reservations.remove(streamId)
        refundOutstandingWrites(streamId, generation)
        saturatedStreams.remove(streamId)
        terminalSignals.remove(streamId)
        releaseRequests.remove(streamId)
        reservation
    }

    private fun markTerminalSignaled(streamId: String, generation: Long): Boolean = synchronized(lock) {
        if (reservations[streamId]?.generation != generation || terminalSignals[streamId] == generation) {
            return@synchronized false
        }
        terminalSignals[streamId] = generation
        true
    }

    private fun terminalWasSignaled(streamId: String, generation: Long): Boolean =
        synchronized(lock) { terminalSignals[streamId] == generation }

    private fun completeWrite(streamId: String, generation: Long, byteCount: Int) = synchronized(lock) {
        val count = outstandingWrites[streamId]
        if (count?.generation != generation) return@synchronized
        if (count.frames <= 0 || count.bytes < byteCount) return@synchronized
        count.frames -= 1
        count.bytes -= byteCount.toLong()
        outstandingWriteFrames -= 1
        outstandingWriteBytes -= byteCount.toLong()
        check(outstandingWriteFrames >= 0 && outstandingWriteBytes >= 0)
        if (count.frames == 0) {
            check(count.bytes == 0L)
            outstandingWrites.remove(streamId)
        }
    }

    private fun refundOutstandingWrites(streamId: String, generation: Long) {
        val count = outstandingWrites[streamId]
        if (count?.generation != generation) return
        outstandingWrites.remove(streamId)
        outstandingWriteFrames -= count.frames
        outstandingWriteBytes -= count.bytes
        check(outstandingWriteFrames >= 0 && outstandingWriteBytes >= 0)
    }

    private fun deadlineAfter(now: Long, duration: Long): Long =
        if (now > Long.MAX_VALUE - duration) Long.MAX_VALUE else now + duration

    private sealed interface ReactorCommand {
        val streamId: String
        val generation: Long

        data class Open(
            override val streamId: String,
            override val generation: Long,
            val correlationToken: Long,
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

    private data class WriteCount(
        val generation: Long,
        var frames: Int = 0,
        var bytes: Long = 0L,
    )

    private data class Reservation(
        val generation: Long,
        val correlationToken: Long,
    )

    private data class ReactorStream(
        val id: String,
        val generation: Long,
        val correlationToken: Long,
        val connection: TargetReactorConnection,
        var connected: Boolean,
        val connectDeadlineNanos: Long,
        var idleDeadlineNanos: Long,
        val writes: ArrayDeque<ByteBuffer> = ArrayDeque(),
        val readBuffer: ByteBuffer,
        var readEof: Boolean = false,
        var releaseAfterWrites: Boolean = false,
    )

    internal companion object {
        const val MAX_STREAMS = AgentCapacity.MAX_STREAMS
        const val DATA_COMMAND_CAPACITY = AgentCapacity.REACTOR_DATA_COMMAND_CAPACITY
        const val COMMAND_CAPACITY = AgentCapacity.REACTOR_COMMAND_CAPACITY
        const val COMMANDS_PER_CYCLE = AgentCapacity.REACTOR_COMMANDS_PER_CYCLE
        const val PER_STREAM_WRITE_CAPACITY = AgentCapacity.TARGET_INBOUND_PER_STREAM_CAPACITY
        const val SESSION_WRITE_CAPACITY = AgentCapacity.TARGET_INBOUND_SESSION_FRAME_CAPACITY
        const val SESSION_WRITE_BYTE_CAPACITY = AgentCapacity.TARGET_INBOUND_SESSION_BYTE_CAPACITY.toLong()
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
