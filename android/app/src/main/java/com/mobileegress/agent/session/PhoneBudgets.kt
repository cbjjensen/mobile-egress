package com.mobileegress.agent.session

/** Actual ownership only: no saved peer reserves space and idle capacity is fully borrowable. */
class SharedFrameBudget(private val frameLimit: Int, private val byteLimit: Long) {
    private var frames = 0
    private var bytes = 0L
    @Synchronized fun acquire(size: Int): Boolean {
        require(size >= 0)
        if (frames >= frameLimit || size.toLong() > byteLimit - bytes) return false
        frames++; bytes += size
        return true
    }
    @Synchronized fun release(size: Long, count: Int = 1) {
        require(size >= 0 && count >= 0 && frames >= count && bytes >= size)
        frames -= count; bytes -= size
    }
    @Synchronized fun snapshot() = frames to bytes
}

/** Serializes only a bounded nonblocking native read; waiting peers take FIFO turns. */
class PeerReadGate {
    private val lock = Any()
    private val waiters = LinkedHashSet<String>()
    private val callbacks = HashMap<String, () -> Unit>()
    private var owner: String? = null
    fun register(id: String, wake: () -> Unit) = synchronized(lock) { callbacks[id] = wake }
    fun enter(id: String): Boolean = synchronized(lock) {
        if (id !in callbacks) return@synchronized false
        if (owner == null && (waiters.isEmpty() || waiters.first() == id)) {
            waiters.remove(id); owner = id; true
        } else { waiters.add(id); false }
    }
    fun leave(id: String) {
        val wake = synchronized(lock) {
            if (owner == id) owner = null
            waiters.firstOrNull()?.let(callbacks::get)
        }
        wake?.invoke()
    }
    fun remove(id: String) {
        val wake = synchronized(lock) {
            callbacks.remove(id); waiters.remove(id)
            if (owner == id) owner = null
            waiters.firstOrNull()?.let(callbacks::get)
        }
        wake?.invoke()
    }
    fun withdraw(id: String) {
        val wake = synchronized(lock) {
            waiters.remove(id)
            waiters.firstOrNull()?.let(callbacks::get)
        }
        wake?.invoke()
    }
}

class PhoneBudgets {
    val outbound = SharedFrameBudget(8192, 64L * 1024 * 1024)
    val inbound = SharedFrameBudget(8192, 64L * 1024 * 1024)
    val outboundControls = SharedFrameBudget(512, 64L * 1024 * 1024)
    val reactorControls = SharedFrameBudget(1024, 0)
    val reads = PeerReadGate()
}
