package com.mobileegress.agent.session

import com.mobileegress.agent.network.DestinationRejected
import com.mobileegress.agent.network.PublicAddressPolicy
import com.mobileegress.agent.protocol.ProtocolException
import com.mobileegress.agent.protocol.WireEnvelope
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
    private val beforeMailboxCommit: () -> Unit = {},
    private val backpressureReporter: BackpressureReporter = LogcatBackpressureReporter,
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
        openReserved(streamId, address)
    }

    fun open(envelope: WireEnvelope) {
        val streamId = envelope.streamId
        if (closed.get()) return
        if (!admission.tryReserve(streamId)) {
            reject(streamId, "agent_stream_limit")
            return
        }
        val target = try {
            WireProtocol.parseOpen(envelope)
        } catch (_: ProtocolException) {
            admission.release(streamId)
            reject(streamId, "invalid_target")
            return
        }
        val address = try {
            InetSocketAddress(PublicAddressPolicy.validate(target.ip, target.port), target.port)
        } catch (_: DestinationRejected) {
            admission.release(streamId)
            reject(streamId, "policy_denied", ErrorClass.TargetPolicy)
            return
        }
        openReserved(streamId, address)
    }

    private fun openReserved(streamId: String, address: InetSocketAddress) {
        var sessionFailure: ErrorClass? = null
        synchronized(lifecycleLock) {
            if (closed.get()) {
                admission.release(streamId)
                return
            }
            val targetReactor = reactor
            if (targetReactor == null) {
                admission.release(streamId)
                sessionFailure = ErrorClass.Internal
            } else {
                val stream = TargetStream(streamId, nextCorrelation.getAndIncrement())
                outbound.allowData(streamId)
                streams[streamId] = stream
                status.onActiveStreams(admission.size)
                // Submission only updates the bounded reactor queue and wakes its selector.
                // Keeping result handling in the same lifecycle critical section prevents any
                // insertion, release, control, or status mutation after shutdown linearizes.
                when (targetReactor.open(streamId, stream.correlationToken, address)) {
                    ReactorSubmitResult.Accepted -> Unit
                    ReactorSubmitResult.StreamLimit -> {
                        finalizeStream(stream, ErrorClass.None)
                        if (!queueRejected(streamId, "agent_stream_limit")) {
                            sessionFailure = ErrorClass.Backpressure
                        }
                    }
                    ReactorSubmitResult.SessionSaturated -> {
                        sessionFailure = ErrorClass.Backpressure
                    }
                    ReactorSubmitResult.StreamSaturated,
                    ReactorSubmitResult.MissingOrClosed -> {
                        finalizeStream(stream, ErrorClass.TargetConnect)
                        if (!queueRejected(streamId, "target_failure")) {
                            sessionFailure = ErrorClass.Backpressure
                        }
                    }
                }
            }
        }
        sessionFailure?.let { errorClass ->
            if (errorClass == ErrorClass.Backpressure) failSessionBackpressure() else onSessionFailure(errorClass)
        }
    }

    fun reject(streamId: String, code: String, errorClass: ErrorClass = ErrorClass.None) {
        val queued = synchronized(lifecycleLock) {
            if (closed.get()) return
            queueRejected(streamId, code, errorClass)
        }
        if (!queued) failSessionBackpressure()
    }

    private fun queueRejected(
        streamId: String,
        code: String,
        errorClass: ErrorClass = ErrorClass.None,
    ): Boolean {
        tombstones.remember(streamId)
        if (errorClass != ErrorClass.None) status.onError(errorClass)
        if (closed.get()) return false
        return outbound.offerRequiredControl(
            WireProtocol.encode("rejected", streamId, WireProtocol.finiteErrorCode(code)),
            streamId = streamId,
        ) {}.also { accepted ->
            if (!accepted) backpressureReporter.report(BackpressureSource.RequiredControlSaturation)
        }
    }

    fun routeData(streamId: String, payload: ByteArray) {
        val stream = streams[streamId]
        if (stream == null) {
            if (tombstones.contains(streamId)) return
            throw ProtocolException("Data for an unknown stream")
        }
        val targetReactor = reactor ?: throw ProtocolException("Data for a closed stream")
        var backpressureQueued: Boolean? = null
        val result = synchronized(stream.lock) {
            when (stream.state) {
                StreamState.Open,
                StreamState.GracefulPending -> Unit
                StreamState.ReleasePending,
                StreamState.ReleaseAwaitingReactor,
                StreamState.ForcedPending,
                StreamState.Released -> return
                StreamState.CancelPending -> throw ProtocolException("Data for a closed stream")
            }
            // Reactor submission only mutates its bounded command queue and wakes the selector. Holding
            // the stream lock makes submission precede graceful release and saturation finalization.
            targetReactor.write(stream.id, stream.correlationToken, payload).also { submitted ->
                if (submitted == ReactorSubmitResult.StreamSaturated) {
                    backpressureQueued = forceBackpressureLocked(stream)
                }
            }
        }
        backpressureQueued?.let { queued ->
            if (!queued) {
                backpressureReporter.report(BackpressureSource.RequiredControlSaturation)
                failSessionBackpressure()
            }
        }
        when (result) {
            ReactorSubmitResult.Accepted -> Unit
            ReactorSubmitResult.StreamSaturated -> Unit
            ReactorSubmitResult.SessionSaturated -> failSessionBackpressure()
            ReactorSubmitResult.StreamLimit -> throw ProtocolException("Data for a closed stream")
            // The reactor drops its reservation before delivering the correlated terminal callback.
            // A stream found above is therefore valid even when that callback has not acquired its
            // bridge lock yet. Truly unknown IDs were rejected before reactor submission.
            ReactorSubmitResult.MissingOrClosed -> Unit
        }
    }

    fun closeFromRelay(streamId: String) {
        val stream = synchronized(lifecycleLock) {
            streams[streamId] ?: run {
                beforeMailboxCommit()
                val canceledPendingFrame = outbound.cancelStream(streamId)
                if (canceledPendingFrame || tombstones.contains(streamId)) return
                throw ProtocolException("Close for an unknown stream")
            }
        }
        val shouldCancel = synchronized(stream.lock) {
            if (
                stream.finalized ||
                stream.state == StreamState.Released ||
                stream.state == StreamState.CancelPending
            ) {
                return
            }
            outbound.cancelStream(stream.id)
            stream.state = StreamState.CancelPending
            true
        }
        if (!shouldCancel) return
        when (reactor?.cancel(stream.id, stream.correlationToken)) {
            ReactorSubmitResult.Accepted -> Unit
            ReactorSubmitResult.SessionSaturated -> failSessionBackpressure()
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
        val queued = synchronized(stream.lock) {
            if (
                stream.finalized ||
                stream.state != StreamState.Open ||
                streams[stream.id] !== stream ||
                closed.get()
            ) {
                return
            }
            beforeMailboxCommit()
            outbound.offerRequiredControl(
                WireProtocol.encode("opened", streamId),
                streamId = streamId,
            ) {}
        }
        if (!queued) {
            backpressureReporter.report(BackpressureSource.RequiredControlSaturation)
            failSessionBackpressure()
        }
    }

    override fun onData(streamId: String, correlationToken: Long, payload: ByteArray): Boolean {
        val stream = current(streamId, correlationToken) ?: return false
        val accepted = synchronized(stream.lock) {
            if (
                stream.finalized ||
                stream.state != StreamState.Open ||
                streams[stream.id] !== stream ||
                closed.get()
            ) {
                return false
            }
            beforeMailboxCommit()
            outbound.offerData(streamId, WireProtocol.encode("data", streamId, payload))
        }
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
        val terminalErrorClass = if (reason == TargetTerminalReason.Backpressure) {
            ErrorClass.None
        } else {
            action.errorClass
        }
        val code = action.code
        if (code == null) {
            if (reason == TargetTerminalReason.Canceled || reason == TargetTerminalReason.Shutdown) {
                finalizeStream(stream, ErrorClass.None)
            }
            return
        }
        when {
            action.rejectUnopenedStream -> rejectUnopenedStream(stream, code, terminalErrorClass)
            action.drainOutboundData -> closeAfterRelayData(stream, code)
            else -> failStream(stream, code, terminalErrorClass)
        }
    }

    override fun onReleased(streamId: String, correlationToken: Long) {
        val stream = current(streamId, correlationToken) ?: return
        synchronized(stream.lock) {
            if (
                stream.state == StreamState.GracefulPending ||
                stream.state == StreamState.ReleasePending
            ) {
                return
            }
            finalizeStream(stream, ErrorClass.None)
        }
    }

    override fun onFatalFailure() {
        onSessionFailure(ErrorClass.Internal)
    }

    private fun closeAfterRelayData(stream: TargetStream, code: String) {
        val terminalFrame = WireProtocol.encode("close", stream.id, WireProtocol.finiteErrorCode(code))
        val reserved = synchronized(stream.lock) {
            if (
                stream.finalized ||
                stream.state != StreamState.Open ||
                streams[stream.id] !== stream ||
                closed.get()
            ) {
                return
            }
            outbound.offerRequiredControlAfterData(
                stream.id,
                terminalFrame,
                beforeEmission = { beginGracefulCloseEmission(stream) },
                onEmitted = { onGracefulCloseEmitted(stream) },
            ) {}.also { accepted ->
                if (accepted) stream.state = StreamState.GracefulPending
            }
        }
        if (!reserved) {
            backpressureReporter.report(BackpressureSource.RequiredControlSaturation)
            failSessionBackpressure()
        }
    }

    private fun beginGracefulCloseEmission(stream: TargetStream): Boolean =
        synchronized(stream.lock) {
            if (stream.finalized || stream.state != StreamState.GracefulPending) {
                return@synchronized false
            }
            stream.state = StreamState.ReleasePending
            true
        }

    private fun onGracefulCloseEmitted(stream: TargetStream) {
        val shouldRelease = synchronized(stream.lock) {
            if (stream.finalized || stream.state != StreamState.ReleasePending) {
                false
            } else {
                stream.state = StreamState.ReleaseAwaitingReactor
                true
            }
        }
        if (!shouldRelease) return
        when (reactor?.release(stream.id, stream.correlationToken)) {
            ReactorSubmitResult.Accepted -> Unit
            ReactorSubmitResult.SessionSaturated -> failSessionBackpressure()
            ReactorSubmitResult.StreamLimit,
            ReactorSubmitResult.StreamSaturated,
            ReactorSubmitResult.MissingOrClosed,
            null -> finalizeStream(stream, ErrorClass.None)
        }
    }

    /** Called with [TargetStream.lock] held so reactor release cannot finalize ahead of the close. */
    private fun forceBackpressureLocked(stream: TargetStream): Boolean {
        if (stream.finalized || stream.state == StreamState.ForcedPending) return true
        stream.state = StreamState.ForcedPending
        outbound.cancelStream(stream.id)
        outbound.allowData(stream.id)
        outbound.blockAndDiscardData(stream.id)
        return outbound.offerRequiredControl(
            WireProtocol.encode(
                "close",
                stream.id,
                WireProtocol.finiteErrorCode(BACKPRESSURE_CLOSE_CODE),
            ),
            streamId = stream.id,
        ) {}
    }

    private fun failStream(stream: TargetStream, code: String, errorClass: ErrorClass) {
        val queued = synchronized(stream.lock) {
            if (
                stream.finalized ||
                stream.state != StreamState.Open ||
                streams[stream.id] !== stream ||
                closed.get()
            ) {
                return
            }
            stream.state = StreamState.ForcedPending
            beforeMailboxCommit()
            outbound.blockAndDiscardData(stream.id)
            outbound.offerRequiredControl(
                WireProtocol.encode("close", stream.id, WireProtocol.finiteErrorCode(code)),
                streamId = stream.id,
            ) {}
        }
        if (!queued) {
            backpressureReporter.report(BackpressureSource.RequiredControlSaturation)
            failSessionBackpressure()
        }
        finalizeStream(stream, errorClass)
    }

    private fun rejectUnopenedStream(stream: TargetStream, code: String, errorClass: ErrorClass) {
        val queued = synchronized(stream.lock) {
            if (
                stream.finalized ||
                stream.state != StreamState.Open ||
                streams[stream.id] !== stream ||
                closed.get()
            ) {
                return
            }
            stream.state = StreamState.ForcedPending
            outbound.blockAndDiscardData(stream.id)
            outbound.offerRequiredControl(
                WireProtocol.encode("rejected", stream.id, WireProtocol.finiteErrorCode(code)),
                streamId = stream.id,
            ) {}
        }
        if (!queued) {
            backpressureReporter.report(BackpressureSource.RequiredControlSaturation)
            failSessionBackpressure()
        }
        finalizeStream(stream, errorClass)
    }

    private fun finalizeStream(stream: TargetStream, errorClass: ErrorClass) {
        synchronized(stream.lock) {
            if (stream.finalized) return
            stream.finalized = true
            stream.state = StreamState.Released
            tombstones.remember(stream.id)
            streams.remove(stream.id, stream)
            admission.release(stream.id)
            status.onActiveStreams(admission.size)
            if (errorClass != ErrorClass.None) status.onError(errorClass)
        }
    }

    private fun failSessionBackpressure() {
        status.onError(ErrorClass.Backpressure)
        onSessionFailure(ErrorClass.Backpressure)
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
        ReleaseAwaitingReactor,
        ForcedPending,
        CancelPending,
        Released,
    }

    private companion object {
        const val BACKPRESSURE_CLOSE_CODE = "agent_unavailable"
    }
}
