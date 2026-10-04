package com.mobileegress.agent.direct

import com.mobileegress.agent.status.ErrorClass
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow

data class PeerRuntime(
    val clientId: String, val epoch: Long, val connected: Boolean = false,
    val streams: Int = 0, val bytesUp: Long = 0, val bytesDown: Long = 0,
    val error: ErrorClass = ErrorClass.None,
    val recovery: DirectRecovery = DirectRecovery.None,
)
class PeerRuntimeTracker(private val publish: (List<PeerRuntime>) -> Unit = {}) {
    private var next = 0L
    private val peers = LinkedHashMap<String, PeerRuntime>()
    @Synchronized fun begin(id: String): Long {
        val epoch = ++next
        peers[id] = PeerRuntime(id, epoch)
        publish(peers.values.toList())
        return epoch
    }
    @Synchronized fun update(id: String, epoch: Long, transform: (PeerRuntime) -> PeerRuntime): Boolean {
        val current = peers[id] ?: return false
        if (current.epoch != epoch) return false
        peers[id] = transform(current)
        publish(peers.values.toList())
        return true
    }
    @Synchronized fun remove(id: String) { peers.remove(id); publish(peers.values.toList()) }
    @Synchronized fun snapshot() = peers.values.toList()
}
object DirectRuntimeBus {
    private val mutable = MutableStateFlow<List<PeerRuntime>>(emptyList())
    val peers = mutable.asStateFlow()
    fun publish(values: List<PeerRuntime>) { mutable.value = values }
}
object DirectRegistrySignals {
    private val mutable = MutableStateFlow(0L)
    val revision = mutable.asStateFlow()
    @Synchronized fun changed() { mutable.value += 1 }
}

object DirectRetrySignals {
    private var handler: ((String) -> Boolean)? = null
    /** Registration lasts for the foreground service lifetime; acceptance confirms a live owner. */
    @Synchronized fun register(retry: (String) -> Boolean): () -> Unit {
        handler = retry
        return { synchronized(this) { if (handler === retry) handler = null } }
    }
    fun retry(id: String): Boolean = synchronized(this) { handler }?.invoke(id) ?: false
}
