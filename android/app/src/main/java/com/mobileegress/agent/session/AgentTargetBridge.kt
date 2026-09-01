package com.mobileegress.agent.session

import com.mobileegress.agent.protocol.ProtocolException
import com.mobileegress.agent.protocol.WireProtocol
import com.mobileegress.agent.status.ErrorClass
import java.net.InetSocketAddress
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicLong

internal interface AgentTargetStatusSink {
    fun onActiveStreams(count: Int)
    fun onBytesDown(byteCount: Int)
    fun onBytesUp(byteCount: Int)
    fun onError(errorClass: ErrorClass)
}

internal object NoOpAgentTargetStatusSink : AgentTargetStatusSink {
    override fun onActiveStreams(count: Int) = Unit
    override fun onBytesDown(byteCount: Int) = Unit
    override fun onBytesUp(byteCount: Int) = Unit
    override fun onError(errorClass: ErrorClass) = Unit
}

/**
 * Owns the protocol-to-reactor stream state machine. This is deliberately below Android/TLS setup
 * so JVM tests exercise the same generation, mailbox, admission, and teardown logic as production.
 */
internal class AgentTargetBridge(
    private val outbound: OutboundMailbox,
    private val reactorFactory: (TargetReactorListener) -> TargetReactorPort,
    private val onSessionFailure: (ErrorClass) -> Unit,
    private val status: AgentTargetStatusSink = NoOpAgentTargetStatusSink,
    maxStreams: Int = AgentCapacity.MAX_STREAMS,
    retainedStreamCapacity: Int = AgentCapacity.RETAINED_STREAM_CAPACITY,
) : TargetReactorListener {
    private val lifecycleLock = Any()
    private val admission = StreamAdmission(maxStreams)
    private val streams = ConcurrentHashMap<String, TargetStream>()
    private val tombstones = StreamTombstones(retainedStreamCapacity)
    private val nextCorrelation = AtomicLong(1L)
    private val closed = AtomicBoolean(false)
    @Volatile private var reactor: TargetReactorPort? = null

    fun start(): Boolean {
        val startedSuccessfully = try {
            synchronized(lifecycleLock) {
                if (closed.get()) return false
                val targetReactor = reactor ?: reactorFactory(this).also { reactor = it }
                targetReactor.start()
            }
        } catch (_: Exception) {
            false
        }
        if (startedSuccessfully) return true
        onSessionFailure(ErrorClass.Internal)
        return false
    }

    fun open(streamId: String, address: InetSocketAddress) {
        if (closed.get()) return
        if (!admission.tryReserve(streamId)) {
            reject(streamId, "agent_stream_limit")
            return
        }
        val targetReactor = reactor
        if (targetReactor == null) {
            admission.release(streamId)
            onSessionFailure(ErrorClass.Internal)
            return
        }
        val stream = TargetStream(streamId, nextCorrelation.getAndIncrement())
        outbound.allowData(streamId)
        streams[streamId] = stream
        status.onActiveStreams(admission.size)
        when (targetReactor.open(streamId, stream.correlationToken, address)) {
            ReactorSubmitResult.Accepted -> Unit
            ReactorSubmitResult.StreamLimit -> {
                finalizeStream(stream, ErrorClass.None)
                reject(streamId, "agent_stream_limit")
            }
            ReactorSubmitResult.SessionSaturated -> onSessionFailure(ErrorClass.Backpressure)
            ReactorSubmitResult.StreamSaturated,
            ReactorSubmitResult.MissingOrClosed -> {
                finalizeStream(stream, ErrorClass.TargetConnect)
                reject(streamId, "target_failure")
            }
        }
    }

    fun reject(streamId: String, code: String, errorClass: ErrorClass = ErrorClass.None) {
        tombstones.remember(streamId)
        if (errorClass != ErrorClass.None) status.onError(errorClass)
        enqueueRequiredControl("rejected", streamId, WireProtocol.finiteErrorCode(code))
    }

    fun routeData(streamId: String, payload: ByteArray) {
        val stream = streams[streamId] ?: throw ProtocolException("Data for an unknown stream")
        val targetReactor = reactor ?: throw ProtocolException("Data for a closed stream")
        val state = synchronized(stream.lock) {
            when (stream.state) {
                StreamState.Open,
                StreamState.GracefulPending -> stream.state
                StreamState.ReleasePending -> return
                StreamState.ForcedPending,
                StreamState.CancelPending,
                StreamState.Released -> throw ProtocolException("Data for a closed stream")
            }
        }
        when (targetReactor.write(stream.id, stream.correlationToken, payload)) {
            ReactorSubmitResult.Accepted -> Unit
            ReactorSubmitResult.StreamSaturated -> failForBackpressure(stream)
            ReactorSubmitResult.SessionSaturated -> onSessionFailure(ErrorClass.Backpressure)
            ReactorSubmitResult.StreamLimit -> throw ProtocolException("Data for a closed stream")
            ReactorSubmitResult.MissingOrClosed -> {
                val gracefulRace = synchronized(stream.lock) {
                    state == StreamState.GracefulPending ||
                        stream.state == StreamState.GracefulPending ||
                        stream.state == StreamState.ReleasePending
                }
                if (!gracefulRace) throw ProtocolException("Data for a closed stream")
            }
        }
    }

    fun closeFromRelay(streamId: String) {
        val stream = streams[streamId]
        if (stream == null) {
            val canceledPendingFrame = outbound.cancelStream(streamId)
            if (canceledPendingFrame || tombstones.contains(streamId)) return
            throw ProtocolException("Close for an unknown stream")
        }
        val shouldCancel = synchronized(stream.lock) {
            if (stream.finalized || stream.state == StreamState.Released) return
            outbound.cancelStream(stream.id)
            stream.state = StreamState.CancelPending
            true
        }
        if (!shouldCancel) return
        when (reactor?.cancel(stream.id, stream.correlationToken)) {
            ReactorSubmitResult.Accepted -> Unit
            ReactorSubmitResult.SessionSaturated -> onSessionFailure(ErrorClass.Backpressure)
            ReactorSubmitResult.StreamLimit,
            ReactorSubmitResult.StreamSaturated,
            ReactorSubmitResult.MissingOrClosed,
            null -> finalizeStream(stream, ErrorClass.None)
        }
    }

    fun shutdownAndAwait(timeout: Long, unit: TimeUnit): Boolean {
        val (firstShutdown, targetReactor) = synchronized(lifecycleLock) {
            closed.compareAndSet(false, true) to reactor
        }
        if (!firstShutdown) {
            val existing = targetReactor ?: return true
            return existing.isReactorThread() || existing.awaitStopped(timeout, unit)
        }
        streams.values.toList().forEach { finalizeStream(it, ErrorClass.None) }
        admission.clear()
        status.onActiveStreams(0)
        targetReactor ?: return true
        targetReactor.shutdown()
        if (targetReactor.isReactorThread()) return false
        return targetReactor.awaitStopped(timeout, unit)
    }

    internal val activeStreamCount: Int
        get() = admission.size

    override fun onOpened(streamId: String, correlationToken: Long) {
        val stream = current(streamId, correlationToken) ?: return
        val eligible = synchronized(stream.lock) {
            !stream.finalized && stream.state == StreamState.Open
        }
        if (eligible) enqueueRequiredControl("opened", streamId)
    }

    override fun onData(streamId: String, correlationToken: Long, payload: ByteArray): Boolean {
        val stream = current(streamId, correlationToken) ?: return false
        val eligible = synchronized(stream.lock) {
            !stream.finalized && stream.state == StreamState.Open
        }
        if (!eligible || closed.get()) return false
        val accepted = outbound.offerData(streamId, WireProtocol.encode("data", streamId, payload))
        if (accepted) status.onBytesDown(payload.size)
        return accepted
    }

    override fun onBytesWritten(streamId: String, correlationToken: Long, byteCount: Int) {
        val stream = current(streamId, correlationToken) ?: return
        if (byteCount > 0 && !stream.finalized) status.onBytesUp(byteCount)
    }

    override fun onTerminal(
        streamId: String,
        correlationToken: Long,
        reason: TargetTerminalReason,
    ) {
        val stream = current(streamId, correlationToken) ?: return
        if (closed.get()) return
        val cancelWon = synchronized(stream.lock) { stream.state == StreamState.CancelPending }
        if (cancelWon) {
            finalizeStream(stream, ErrorClass.None)
            return
        }
        val action = reason.protocolAction()
        val code = action.code
        if (code == null) {
            if (reason == TargetTerminalReason.Canceled || reason == TargetTerminalReason.Shutdown) {
                finalizeStream(stream, ErrorClass.None)
            }
            return
        }
        when {
            action.rejectUnopenedStream -> rejectUnopenedStream(stream, code, action.errorClass)
            action.drainOutboundData -> closeAfterRelayData(stream, code)
            else -> failStream(stream, code, action.errorClass)
        }
    }

    override fun onReleased(streamId: String, correlationToken: Long) {
        val stream = current(streamId, correlationToken) ?: return
        finalizeStream(stream, ErrorClass.None)
    }

    override fun onFatalFailure() {
        onSessionFailure(ErrorClass.Internal)
    }

    private fun closeAfterRelayData(stream: TargetStream, code: String) {
        val terminalFrame = WireProtocol.encode("close", stream.id, WireProtocol.finiteErrorCode(code))
        val reserved = synchronized(stream.lock) {
            if (stream.finalized || stream.state != StreamState.Open || closed.get()) return
            outbound.offerRequiredControlAfterData(
                stream.id,
                terminalFrame,
                onEmitted = { onGracefulCloseEmitted(stream) },
            ) {}.also { accepted ->
                if (accepted) stream.state = StreamState.GracefulPending
            }
        }
        if (!reserved) onSessionFailure(ErrorClass.Backpressure)
    }

    private fun onGracefulCloseEmitted(stream: TargetStream) {
        val shouldRelease = synchronized(stream.lock) {
            if (stream.finalized || stream.state != StreamState.GracefulPending) return
            stream.state = StreamState.ReleasePending
            true
        }
        if (!shouldRelease) return
        when (reactor?.release(stream.id, stream.correlationToken)) {
            ReactorSubmitResult.Accepted -> Unit
            ReactorSubmitResult.SessionSaturated -> onSessionFailure(ErrorClass.Backpressure)
            ReactorSubmitResult.StreamLimit,
            ReactorSubmitResult.StreamSaturated,
            ReactorSubmitResult.MissingOrClosed,
            null -> finalizeStream(stream, ErrorClass.None)
        }
    }

    private fun failForBackpressure(stream: TargetStream) {
        val shouldFail = synchronized(stream.lock) {
            if (stream.finalized || stream.state == StreamState.ForcedPending) return
            stream.state = StreamState.ForcedPending
            true
        }
        if (!shouldFail) return
        outbound.cancelStream(stream.id)
        outbound.blockAndDiscardData(stream.id)
        val queued = outbound.offerRequiredControl(
            WireProtocol.encode(
                "close",
                stream.id,
                WireProtocol.finiteErrorCode(BACKPRESSURE_CLOSE_CODE),
            ),
            streamId = stream.id,
        ) {}
        if (!queued) onSessionFailure(ErrorClass.Backpressure)
        status.onError(ErrorClass.Backpressure)
    }

    private fun failStream(stream: TargetStream, code: String, errorClass: ErrorClass) {
        val shouldFail = synchronized(stream.lock) {
            if (stream.finalized || stream.state != StreamState.Open || closed.get()) return
            stream.state = StreamState.ForcedPending
            true
        }
        if (!shouldFail) return
        outbound.blockAndDiscardData(stream.id)
        val queued = outbound.offerRequiredControl(
            WireProtocol.encode("close", stream.id, WireProtocol.finiteErrorCode(code)),
            streamId = stream.id,
        ) {}
        if (!queued) onSessionFailure(ErrorClass.Backpressure)
        finalizeStream(stream, errorClass)
    }

    private fun rejectUnopenedStream(stream: TargetStream, code: String, errorClass: ErrorClass) {
        outbound.blockAndDiscardData(stream.id)
        finalizeStream(stream, errorClass)
        reject(stream.id, code)
    }

    private fun finalizeStream(stream: TargetStream, errorClass: ErrorClass) {
        val shouldFinalize = synchronized(stream.lock) {
            if (stream.finalized) return
            stream.finalized = true
            stream.state = StreamState.Released
            true
        }
        if (!shouldFinalize) return
        streams.remove(stream.id, stream)
        admission.release(stream.id)
        tombstones.remember(stream.id)
        status.onActiveStreams(admission.size)
        if (errorClass != ErrorClass.None) status.onError(errorClass)
    }

    private fun enqueueRequiredControl(
        type: String,
        streamId: String = "",
        payload: ByteArray = byteArrayOf(),
    ): Boolean {
        if (closed.get()) return false
        return outbound.offerRequiredControl(
            WireProtocol.encode(type, streamId, payload),
            streamId = streamId.takeIf(String::isNotEmpty),
        ) { onSessionFailure(ErrorClass.Backpressure) }
    }

    private fun current(streamId: String, correlationToken: Long): TargetStream? =
        streams[streamId]?.takeIf { it.correlationToken == correlationToken }

    private class TargetStream(
        val id: String,
        val correlationToken: Long,
    ) {
        val lock = Any()
        @Volatile var state = StreamState.Open
        @Volatile var finalized = false
    }

    private enum class StreamState {
        Open,
        GracefulPending,
        ReleasePending,
        ForcedPending,
        CancelPending,
        Released,
    }

    private companion object {
        const val BACKPRESSURE_CLOSE_CODE = "agent_unavailable"
    }
}
