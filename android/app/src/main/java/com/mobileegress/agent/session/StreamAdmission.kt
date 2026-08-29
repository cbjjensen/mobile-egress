package com.mobileegress.agent.session

class StreamAdmission(private val limit: Int) {
    private val reserved = LinkedHashSet<String>()

    val size: Int
        @Synchronized get() = reserved.size

    @Synchronized
    fun tryReserve(streamId: String): Boolean {
        if (reserved.size >= limit || streamId in reserved) return false
        reserved += streamId
        return true
    }

    @Synchronized
    fun release(streamId: String) {
        reserved -= streamId
    }

    @Synchronized
    fun clear(): Set<String> = reserved.toSet().also { reserved.clear() }
}
