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
    internal var transportOwned = false
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
    private var outstandingControlFrames = 0
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
            if (closed || outstandingControlFrames >= controlCapacity) return@synchronized false
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

    fun poll(
        maxDataBytes: Long = Long.MAX_VALUE,
        maxControlBytes: Long = Long.MAX_VALUE,
    ): OutboundFrame? = synchronized(lock) {
        pollEligibleControl(maxControlBytes) ?: pollData(maxDataBytes)
    }

    internal suspend fun awaitAvailable(): Boolean = !available.receiveCatching().isClosed

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

    fun emit(
        frame: OutboundFrame,
        retainUntilDrained: Boolean = false,
        sender: (ByteArray) -> Boolean,
    ): OutboundEmission {
        if (frame.beforeEmission?.invoke() == false) {
            synchronized(lock) { release(frame) }
            return OutboundEmission.Canceled
        }
        var emittedCallback: (() -> Unit)? = null
        val result = synchronized(lock) {
            val canceledStream = frame.streamCancellation?.canceled == true
            val canceledData = frame.dataCancellation?.canceled == true
            val canceled = closed || frame.released || canceledStream || canceledData
            if (canceled) {
                OutboundEmission.Canceled
            } else {
                // Claim before send: cancellation may run reentrantly in sender callbacks.
                frame.transportOwned = retainUntilDrained
                try {
                    if (!sender(frame.bytes)) {
                        frame.transportOwned = false
                        OutboundEmission.Failed
                    } else {
                        emittedCallback = frame.onEmitted
                        OutboundEmission.Emitted
                    }
                } catch (error: Throwable) {
                    frame.transportOwned = false
                    release(frame)
                    throw error
                }
            }.also { if (it != OutboundEmission.Emitted || !retainUntilDrained) release(frame) }
        }
        emittedCallback?.invoke()
        return result
    }

    internal fun drained(frame: OutboundFrame) = synchronized(lock) { release(frame) }

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
            check(outstandingDataFrames == 0 && outstandingDataBytes == 0L && outstandingControlFrames == 0)
        }
        available.close()
    }

    internal fun snapshot(): OutboundMailboxSnapshot = synchronized(lock) {
        OutboundMailboxSnapshot(outstandingDataFrames, outstandingDataBytes)
    }

    private fun offerControl(bytes: ByteArray, streamId: String?): Boolean {
        val queued = synchronized(lock) {
            if (closed || outstandingControlFrames >= controlCapacity) return@synchronized false
            controls.addLast(ControlFrame(createFrame(bytes, streamId = streamId)))
            true
        }
        if (queued) available.trySend(Unit)
        return queued
    }

    private fun pollEligibleControl(maxBytes: Long = Long.MAX_VALUE): OutboundFrame? {
        repeat(controls.size) {
            val control = controls.removeFirst()
            if (control.frame.bytes.size <= maxBytes &&
                (control.afterDataStreamId == null || control.afterDataStreamId !in dataByStream)
            ) {
                return control.frame
            }
            controls.addLast(control)
        }
        return null
    }

    private fun pollData(maxBytes: Long = Long.MAX_VALUE): OutboundFrame? {
        repeat(readyStreams.size) {
            val streamId = readyStreams.removeFirst()
            val streamData = requireNotNull(dataByStream[streamId])
            if (streamData.first.bytes.size > maxBytes) {
                readyStreams.addLast(streamId)
            } else {
                val frame = streamData.removeFirst()
                if (streamData.isEmpty()) {
                    dataByStream.remove(streamId)
                } else {
                    readyStreams.addLast(streamId)
                }
                return frame
            }
        }
        return null
    }

    private fun discardData(streamId: String) {
        dataByStream.remove(streamId)?.let { discarded ->
            discarded.forEach(::release)
        }
        readyStreams.removeAll { it == streamId }
        outstandingFrames
            .filter { frame -> frame.streamId == streamId && frame.dataCancellation != null && !frame.transportOwned }
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
            } else {
                outstandingControlFrames += 1
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
        } else {
            outstandingControlFrames -= 1
            check(outstandingControlFrames >= 0)
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
