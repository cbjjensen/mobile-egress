package com.mobileegress.agent.session

import java.util.ArrayDeque
import kotlinx.coroutines.channels.Channel

internal class OutboundCancellation(
    var canceled: Boolean,
    var outstanding: Int = 0,
)

class OutboundFrame internal constructor(
    val bytes: ByteArray,
    internal val streamId: String? = null,
    internal val streamCancellation: OutboundCancellation? = null,
    internal val dataCancellation: OutboundCancellation? = null,
    internal val dataByteCount: Int = 0,
    internal val beforeEmission: (() -> Boolean)? = null,
    internal val onEmitted: (() -> Unit)? = null,
) {
    internal var released = false
}

internal data class OutboundMailboxSnapshot(
    val outstandingDataFrames: Int,
    val outstandingDataBytes: Long,
)

enum class OutboundEmission { Emitted, Canceled, Failed }

class OutboundMailbox(
    private val controlCapacity: Int = AgentCapacity.OUTBOUND_CONTROL_CAPACITY,
    private val dataCapacity: Int = AgentCapacity.OUTBOUND_DATA_CAPACITY,
    private val perStreamDataCapacity: Int = AgentCapacity.OUTBOUND_PER_STREAM_DATA_CAPACITY,
    private val retainedStreamCapacity: Int = AgentCapacity.RETAINED_STREAM_CAPACITY,
    private val dataByteCapacity: Long = AgentCapacity.OUTBOUND_DATA_BYTE_CAPACITY.toLong(),
) {
    private val lock = Any()
    private val controls = ArrayDeque<ControlFrame>()
    private val dataByStream = LinkedHashMap<String, ArrayDeque<OutboundFrame>>()
    private val readyStreams = ArrayDeque<String>()
    private val blockedDataStreams = LinkedHashSet<String>()
    private val canceledDataStreams = LinkedHashSet<String>()
    private val canceledStreams = LinkedHashSet<String>()
    private val streamCancellations = HashMap<String, OutboundCancellation>()
    private val dataCancellations = HashMap<String, OutboundCancellation>()
    private val outstandingFrames = LinkedHashSet<OutboundFrame>()
    private val available = Channel<Unit>(Channel.CONFLATED)
    private var outstandingDataFrames = 0
    private var outstandingDataBytes = 0L
    private var closed = false

    init {
        require(controlCapacity > 0)
        require(dataCapacity > 0)
        require(perStreamDataCapacity in 1..dataCapacity)
        require(retainedStreamCapacity > 0)
        require(dataByteCapacity >= 0)
    }

    fun offerData(streamId: String, frame: ByteArray): Boolean {
        val queued = synchronized(lock) {
            if (
                closed ||
                streamId in blockedDataStreams ||
                outstandingDataFrames >= dataCapacity ||
                (dataCancellations[streamId]?.outstanding ?: 0) >= perStreamDataCapacity ||
                frame.size.toLong() > dataByteCapacity - outstandingDataBytes
            ) {
                return@synchronized false
            }
            val streamData = dataByStream.getOrPut(streamId) { ArrayDeque() }
            if (streamData.isEmpty()) readyStreams.addLast(streamId)
            val outboundFrame = createFrame(frame, streamId = streamId, isData = true)
            streamData.addLast(outboundFrame)
            true
        }
        if (queued) available.trySend(Unit)
        return queued
    }

    fun blockAndDiscardData(streamId: String) {
        synchronized(lock) {
            blockDataStream(streamId)
            cancelDataStream(streamId)
            dataCancellations[streamId]?.canceled = true
            discardData(streamId)
        }
    }

    fun cancelStream(streamId: String): Boolean = synchronized(lock) {
        val hadOutstandingFrame = (streamCancellations[streamId]?.outstanding ?: 0) > 0
        blockDataStream(streamId)
        cancelDataStream(streamId)
        streamCancellations[streamId]?.canceled = true
        dataCancellations[streamId]?.canceled = true
        discardData(streamId)
        canceledStreams += streamId
        trim(canceledStreams)
        controls.removeAll { control ->
            (control.frame.streamId == streamId).also { removed ->
                if (removed) release(control.frame)
            }
        }
        hadOutstandingFrame
    }

    fun allowData(streamId: String) = synchronized(lock) {
        streamCancellations.remove(streamId)?.canceled = true
        dataCancellations.remove(streamId)?.canceled = true
        blockedDataStreams -= streamId
        canceledDataStreams -= streamId
        canceledStreams -= streamId
    }

    fun offerRequiredControl(
        frame: ByteArray,
        streamId: String?,
        onSaturated: () -> Unit,
    ): Boolean {
        val queued = offerControl(frame, streamId)
        if (!queued) onSaturated()
        return queued
    }

    fun offerRequiredControlAfterData(
        streamId: String,
        frame: ByteArray,
        beforeEmission: () -> Boolean = { true },
        onEmitted: () -> Unit = {},
        onSaturated: () -> Unit,
    ): Boolean {
        val queued = synchronized(lock) {
            if (closed || controls.size >= controlCapacity) return@synchronized false
            blockDataStream(streamId)
            val outboundFrame = createFrame(
                bytes = frame,
                streamId = streamId,
                beforeEmission = beforeEmission,
                onEmitted = onEmitted,
            )
            controls.addLast(ControlFrame(frame = outboundFrame, afterDataStreamId = streamId))
            true
        }
        if (queued) available.trySend(Unit)
        if (!queued) onSaturated()
        return queued
    }

    fun poll(): OutboundFrame? = synchronized(lock) {
        pollEligibleControl() ?: pollData()
    }

    suspend fun receive(): OutboundFrame? {
        while (true) {
            synchronized(lock) {
                if (closed) return null
                pollEligibleControl()?.let { return it }
                pollData()?.let { return it }
            }
            if (available.receiveCatching().isClosed) return null
        }
    }

    fun emit(frame: OutboundFrame, sender: (ByteArray) -> Boolean): OutboundEmission {
        if (frame.beforeEmission?.invoke() == false) {
            synchronized(lock) { release(frame) }
            return OutboundEmission.Canceled
        }
        var emittedCallback: (() -> Unit)? = null
        val result = synchronized(lock) {
            val canceledStream = frame.streamCancellation?.canceled == true
            val canceledData = frame.dataCancellation?.canceled == true
            val canceled = canceledStream || canceledData
            if (canceled) {
                OutboundEmission.Canceled
            } else if (!sender(frame.bytes)) {
                OutboundEmission.Failed
            } else {
                emittedCallback = frame.onEmitted
                OutboundEmission.Emitted
            }.also { release(frame) }
        }
        emittedCallback?.invoke()
        return result
    }

    fun close() {
        synchronized(lock) {
            if (closed) return
            closed = true
            streamCancellations.values.forEach { it.canceled = true }
            dataCancellations.values.forEach { it.canceled = true }
            outstandingFrames.toList().forEach(::release)
            controls.clear()
            dataByStream.clear()
            readyStreams.clear()
            blockedDataStreams.clear()
            canceledDataStreams.clear()
            canceledStreams.clear()
            streamCancellations.clear()
            dataCancellations.clear()
            check(outstandingDataFrames == 0 && outstandingDataBytes == 0L)
        }
        available.close()
    }

    internal fun snapshot(): OutboundMailboxSnapshot = synchronized(lock) {
        OutboundMailboxSnapshot(outstandingDataFrames, outstandingDataBytes)
    }

    private fun offerControl(bytes: ByteArray, streamId: String?): Boolean {
        val queued = synchronized(lock) {
            if (closed || controls.size >= controlCapacity) return@synchronized false
            controls.addLast(ControlFrame(createFrame(bytes, streamId = streamId)))
            true
        }
        if (queued) available.trySend(Unit)
        return queued
    }

    private fun pollEligibleControl(): OutboundFrame? {
        repeat(controls.size) {
            val control = controls.removeFirst()
            if (control.afterDataStreamId == null || control.afterDataStreamId !in dataByStream) {
                return control.frame
            }
            controls.addLast(control)
        }
        return null
    }

    private fun pollData(): OutboundFrame? {
        val streamId = readyStreams.pollFirst() ?: return null
        val streamData = requireNotNull(dataByStream[streamId])
        val frame = streamData.removeFirst()
        if (streamData.isEmpty()) {
            dataByStream.remove(streamId)
        } else {
            readyStreams.addLast(streamId)
        }
        return frame
    }

    private fun discardData(streamId: String) {
        dataByStream.remove(streamId)?.let { discarded ->
            discarded.forEach(::release)
        }
        readyStreams.removeAll { it == streamId }
        outstandingFrames
            .filter { frame -> frame.streamId == streamId && frame.dataCancellation != null }
            .forEach(::release)
    }

    private fun createFrame(
        bytes: ByteArray,
        streamId: String?,
        isData: Boolean = false,
        beforeEmission: (() -> Boolean)? = null,
        onEmitted: (() -> Unit)? = null,
    ): OutboundFrame {
        val streamCancellation = streamId?.let {
            cancellation(streamCancellations, it, it in canceledStreams)
        }
        val dataCancellation = streamId?.takeIf { isData }?.let {
            cancellation(dataCancellations, it, it in canceledDataStreams)
        }
        return OutboundFrame(
            bytes = bytes,
            streamId = streamId,
            streamCancellation = streamCancellation,
            dataCancellation = dataCancellation,
            dataByteCount = if (isData) bytes.size else 0,
            beforeEmission = beforeEmission,
            onEmitted = onEmitted,
        ).also { frame ->
            outstandingFrames += frame
            if (isData) {
                outstandingDataFrames += 1
                outstandingDataBytes += bytes.size.toLong()
            }
        }
    }

    private fun cancellation(
        cancellations: MutableMap<String, OutboundCancellation>,
        streamId: String,
        canceled: Boolean,
    ): OutboundCancellation = cancellations.getOrPut(streamId) {
        OutboundCancellation(canceled = canceled)
    }.also { it.outstanding += 1 }

    private fun release(frame: OutboundFrame) {
        if (frame.released) return
        frame.released = true
        outstandingFrames -= frame
        if (frame.dataCancellation != null) {
            outstandingDataFrames -= 1
            outstandingDataBytes -= frame.dataByteCount.toLong()
            check(outstandingDataFrames >= 0 && outstandingDataBytes >= 0)
        }
        frame.streamId?.let { streamId ->
            release(streamCancellations, streamId, frame.streamCancellation)
            release(dataCancellations, streamId, frame.dataCancellation)
        }
    }

    private fun release(
        cancellations: MutableMap<String, OutboundCancellation>,
        streamId: String,
        cancellation: OutboundCancellation?,
    ) {
        cancellation ?: return
        cancellation.outstanding -= 1
        if (cancellation.outstanding == 0 && cancellations[streamId] === cancellation) {
            cancellations.remove(streamId)
        }
    }

    private fun blockDataStream(streamId: String) {
        blockedDataStreams += streamId
        trim(blockedDataStreams)
    }

    private fun cancelDataStream(streamId: String) {
        canceledDataStreams += streamId
        trim(canceledDataStreams)
    }

    private fun trim(streams: LinkedHashSet<String>) {
        if (streams.size > retainedStreamCapacity) streams.remove(streams.first())
    }

    private data class ControlFrame(
        val frame: OutboundFrame,
        val afterDataStreamId: String? = null,
    )
}
