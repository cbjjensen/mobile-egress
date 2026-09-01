package com.mobileegress.agent.session

import java.util.ArrayDeque

internal class StreamTombstones(
    private val capacity: Int = AgentCapacity.RETAINED_STREAM_CAPACITY,
) {
    private val lock = Any()
    private val ordered = ArrayDeque<String>()
    private val values = HashSet<String>()

    init {
        require(capacity > 0)
    }

    fun remember(streamId: String) = synchronized(lock) {
        if (!values.add(streamId)) return@synchronized
        ordered.addLast(streamId)
        if (ordered.size > capacity) values.remove(ordered.removeFirst())
    }

    fun contains(streamId: String): Boolean = synchronized(lock) { streamId in values }
}
