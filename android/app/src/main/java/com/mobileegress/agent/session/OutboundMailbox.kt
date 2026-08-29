package com.mobileegress.agent.session

import java.util.ArrayDeque
import kotlinx.coroutines.channels.Channel

class OutboundMailbox(
    private val controlCapacity: Int,
    private val dataCapacity: Int,
    private val perStreamDataCapacity: Int,
) {
    private val lock = Any()
    private val controls = ArrayDeque<ControlFrame>()
    private val dataByStream = LinkedHashMap<String, ArrayDeque<ByteArray>>()
    private val readyStreams = ArrayDeque<String>()
    private val blockedDataStreams = LinkedHashSet<String>()
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
            streamData.addLast(frame)
            dataSize += 1
            true
        }
        if (queued) available.trySend(Unit)
        return queued
    }

    fun blockAndDiscardData(streamId: String) {
        synchronized(lock) {
            blockDataStream(streamId)
            dataByStream.remove(streamId)?.let { discarded -> dataSize -= discarded.size }
            readyStreams.removeAll { it == streamId }
        }
    }

    fun allowData(streamId: String) = synchronized(lock) {
        blockedDataStreams -= streamId
    }

    fun offerRequiredControl(frame: ByteArray, onSaturated: () -> Unit): Boolean {
        val queued = offerControl(ControlFrame(frame))
        if (!queued) onSaturated()
        return queued
    }

    fun offerRequiredControlAfterData(
        streamId: String,
        frame: ByteArray,
        onSaturated: () -> Unit,
    ): Boolean {
        val queued = synchronized(lock) {
            if (closed || controls.size >= controlCapacity) return@synchronized false
            blockDataStream(streamId)
            controls.addLast(ControlFrame(frame, afterDataStreamId = streamId))
            true
        }
        if (queued) available.trySend(Unit)
        if (!queued) onSaturated()
        return queued
    }

    fun poll(): ByteArray? = synchronized(lock) {
        pollEligibleControl() ?: pollData()
    }

    suspend fun receive(): ByteArray? {
        while (true) {
            synchronized(lock) {
                if (closed) return null
                pollEligibleControl()?.let { return it }
                pollData()?.let { return it }
            }
            if (available.receiveCatching().isClosed) return null
        }
    }

    fun close() {
        synchronized(lock) {
            if (closed) return
            closed = true
            controls.clear()
            dataByStream.clear()
            readyStreams.clear()
            blockedDataStreams.clear()
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

    private fun pollEligibleControl(): ByteArray? {
        repeat(controls.size) {
            val control = controls.removeFirst()
            if (control.afterDataStreamId == null || control.afterDataStreamId !in dataByStream) {
                return control.frame
            }
            controls.addLast(control)
        }
        return null
    }

    private fun pollData(): ByteArray? {
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

    private fun blockDataStream(streamId: String) {
        blockedDataStreams += streamId
        if (blockedDataStreams.size > MAX_BLOCKED_STREAMS) {
            blockedDataStreams.remove(blockedDataStreams.first())
        }
    }

    private data class ControlFrame(
        val frame: ByteArray,
        val afterDataStreamId: String? = null,
    )

    private companion object {
        const val MAX_BLOCKED_STREAMS = 128
    }
}
