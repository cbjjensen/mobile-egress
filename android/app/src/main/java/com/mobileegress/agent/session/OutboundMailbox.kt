package com.mobileegress.agent.session

import java.util.ArrayDeque
import kotlinx.coroutines.channels.Channel

class OutboundMailbox(
    private val controlCapacity: Int,
    private val dataCapacity: Int,
) {
    private val lock = Any()
    private val controls = ArrayDeque<ByteArray>()
    private val data = ArrayDeque<DataFrame>()
    private val blockedDataStreams = LinkedHashSet<String>()
    private val available = Channel<Unit>(Channel.CONFLATED)
    private var closed = false

    init {
        require(controlCapacity > 0)
        require(dataCapacity > 0)
    }

    fun offerData(streamId: String, frame: ByteArray): Boolean {
        val queued = synchronized(lock) {
            if (closed || streamId in blockedDataStreams || data.size >= dataCapacity) return@synchronized false
            data.addLast(DataFrame(streamId, frame))
            true
        }
        if (queued) available.trySend(Unit)
        return queued
    }

    fun blockAndDiscardData(streamId: String) = synchronized(lock) {
        blockedDataStreams += streamId
        if (blockedDataStreams.size > MAX_BLOCKED_STREAMS) {
            blockedDataStreams.remove(blockedDataStreams.first())
        }
        data.removeAll { it.streamId == streamId }
    }

    fun allowData(streamId: String) = synchronized(lock) {
        blockedDataStreams -= streamId
    }

    fun offerRequiredControl(frame: ByteArray, onSaturated: () -> Unit): Boolean {
        val queued = offer(controls, controlCapacity, frame)
        if (!queued) onSaturated()
        return queued
    }

    fun poll(): ByteArray? = synchronized(lock) {
        when {
            controls.isNotEmpty() -> controls.removeFirst()
            data.isNotEmpty() -> data.removeFirst().frame
            else -> null
        }
    }

    suspend fun receive(): ByteArray? {
        while (true) {
            synchronized(lock) {
                if (closed) return null
                if (controls.isNotEmpty()) return controls.removeFirst()
                if (data.isNotEmpty()) return data.removeFirst().frame
            }
            if (available.receiveCatching().isClosed) return null
        }
    }

    fun close() {
        synchronized(lock) {
            if (closed) return
            closed = true
            controls.clear()
            data.clear()
            blockedDataStreams.clear()
        }
        available.close()
    }

    private fun offer(queue: ArrayDeque<ByteArray>, capacity: Int, frame: ByteArray): Boolean {
        val queued = synchronized(lock) {
            if (closed || queue.size >= capacity) return@synchronized false
            queue.addLast(frame)
            true
        }
        if (queued) available.trySend(Unit)
        return queued
    }

    private data class DataFrame(val streamId: String, val frame: ByteArray)

    private companion object {
        const val MAX_BLOCKED_STREAMS = 128
    }
}
