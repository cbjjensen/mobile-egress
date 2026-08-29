package com.mobileegress.agent.session

import java.util.ArrayDeque
import kotlinx.coroutines.channels.Channel

class OutboundFrame internal constructor(
    val bytes: ByteArray,
    internal val streamId: String? = null,
    internal val isData: Boolean = false,
    internal val gracefulStreamId: String? = null,
    internal val onEmitted: () -> Unit = {},
)

enum class OutboundEmission { Emitted, Canceled, Failed }

class OutboundMailbox(
    private val controlCapacity: Int,
    private val dataCapacity: Int,
    private val perStreamDataCapacity: Int,
) {
    private val lock = Any()
    private val controls = ArrayDeque<ControlFrame>()
    private val dataByStream = LinkedHashMap<String, ArrayDeque<OutboundFrame>>()
    private val readyStreams = ArrayDeque<String>()
    private val blockedDataStreams = LinkedHashSet<String>()
    private val canceledDataStreams = LinkedHashSet<String>()
    private val pendingGracefulStreams = HashSet<String>()
    private val canceledGracefulStreams = LinkedHashSet<String>()
    private val available = Channel<Unit>(Channel.CONFLATED)
    private var dataSize = 0
    private var closed = false

    init {
        require(controlCapacity > 0)
        require(dataCapacity > 0)
        require(perStreamDataCapacity in 1..dataCapacity)
    }

    fun offerData(streamId: String, frame: ByteArray): Boolean {
        val queued = synchronized(lock) {
            if (closed || streamId in blockedDataStreams || dataSize >= dataCapacity) return@synchronized false
            val streamData = dataByStream.getOrPut(streamId) { ArrayDeque() }
            if (streamData.size >= perStreamDataCapacity) return@synchronized false
            if (streamData.isEmpty()) readyStreams.addLast(streamId)
            streamData.addLast(OutboundFrame(frame, streamId = streamId, isData = true))
            dataSize += 1
            true
        }
        if (queued) available.trySend(Unit)
        return queued
    }

    fun blockAndDiscardData(streamId: String) {
        synchronized(lock) {
            blockDataStream(streamId)
            cancelDataStream(streamId)
            discardData(streamId)
        }
    }

    fun cancelGracefulStream(streamId: String): Boolean = synchronized(lock) {
        if (!pendingGracefulStreams.remove(streamId)) return@synchronized false
        blockDataStream(streamId)
        cancelDataStream(streamId)
        discardData(streamId)
        canceledGracefulStreams += streamId
        trim(canceledGracefulStreams)
        controls.removeAll { it.afterDataStreamId == streamId }
        true
    }

    fun allowData(streamId: String) = synchronized(lock) {
        blockedDataStreams -= streamId
        canceledDataStreams -= streamId
        canceledGracefulStreams -= streamId
    }

    fun offerRequiredControl(frame: ByteArray, onSaturated: () -> Unit): Boolean {
        val queued = offerControl(ControlFrame(OutboundFrame(frame)))
        if (!queued) onSaturated()
        return queued
    }

    fun offerRequiredControlAfterData(
        streamId: String,
        frame: ByteArray,
        onEmitted: () -> Unit = {},
        onSaturated: () -> Unit,
    ): Boolean {
        val queued = synchronized(lock) {
            if (closed || controls.size >= controlCapacity) return@synchronized false
            blockDataStream(streamId)
            pendingGracefulStreams += streamId
            controls.addLast(
                ControlFrame(
                    frame = OutboundFrame(
                        bytes = frame,
                        streamId = streamId,
                        gracefulStreamId = streamId,
                        onEmitted = onEmitted,
                    ),
                    afterDataStreamId = streamId,
                ),
            )
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
        var emittedCallback: (() -> Unit)? = null
        val result = synchronized(lock) {
            val canceledData = frame.isData && frame.streamId?.let { it in canceledDataStreams } == true
            val canceledTerminal =
                frame.gracefulStreamId?.let { it in canceledGracefulStreams } == true
            val canceled = canceledData || canceledTerminal
            if (canceled) {
                OutboundEmission.Canceled
            } else if (!sender(frame.bytes)) {
                OutboundEmission.Failed
            } else {
                frame.gracefulStreamId?.let { streamId ->
                    pendingGracefulStreams -= streamId
                    emittedCallback = frame.onEmitted
                }
                OutboundEmission.Emitted
            }
        }
        emittedCallback?.invoke()
        return result
    }

    fun close() {
        synchronized(lock) {
            if (closed) return
            closed = true
            controls.clear()
            dataByStream.clear()
            readyStreams.clear()
            blockedDataStreams.clear()
            canceledDataStreams.clear()
            pendingGracefulStreams.clear()
            canceledGracefulStreams.clear()
            dataSize = 0
        }
        available.close()
    }

    private fun offerControl(frame: ControlFrame): Boolean {
        val queued = synchronized(lock) {
            if (closed || controls.size >= controlCapacity) return@synchronized false
            controls.addLast(frame)
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
        dataSize -= 1
        if (streamData.isEmpty()) {
            dataByStream.remove(streamId)
        } else {
            readyStreams.addLast(streamId)
        }
        return frame
    }

    private fun discardData(streamId: String) {
        dataByStream.remove(streamId)?.let { discarded -> dataSize -= discarded.size }
        readyStreams.removeAll { it == streamId }
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
        if (streams.size > MAX_BLOCKED_STREAMS) streams.remove(streams.first())
    }

    private data class ControlFrame(
        val frame: OutboundFrame,
        val afterDataStreamId: String? = null,
    )

    private companion object {
        const val MAX_BLOCKED_STREAMS = 128
    }
}
