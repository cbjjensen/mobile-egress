package com.mobileegress.agent.direct

import android.net.Network
import com.mobileegress.agent.security.DeviceKeyStore
import com.mobileegress.agent.session.AgentSession
import com.mobileegress.agent.session.AgentSessionListener
import com.mobileegress.agent.session.AgentTargetStatusSink
import com.mobileegress.agent.session.PhoneBudgets
import com.mobileegress.agent.status.AgentStatusBus
import com.mobileegress.agent.status.ErrorClass
import com.mobileegress.agent.status.RelayHealth
import kotlinx.coroutines.*

internal interface DirectPeerConnection {
    fun connect()
    fun close()
}
internal class DirectPeerSupervisor(
    private val registry: DirectRegistry,
    private val keys: DeviceKeyStore,
    private val scope: CoroutineScope,
    private val maintain: suspend (String, Network) -> DirectRecord,
    private val budgets: PhoneBudgets = PhoneBudgets(),
    private val workerDispatcher: CoroutineDispatcher = Dispatchers.IO,
    private val connectionFactory: (Network, DirectRecord, AgentSessionListener, AgentTargetStatusSink) -> DirectPeerConnection =
        { network, record, listener, status ->
            val session = AgentSession(network, requireNotNull(record.identity), keys, scope, listener,
                phoneBudgets = budgets, statusSink = status)
            object : DirectPeerConnection {
                override fun connect() = session.connect()
                override fun close() = session.close()
            }
        },
) {
    private val lock = Any()
    private val entries = HashMap<String, Entry>()
    private var desiredNetwork: Network? = null
    private var revision = 0L
    private val tracker = PeerRuntimeTracker { peers ->
        DirectRuntimeBus.publish(peers)
        AgentStatusBus.update { old -> old.copy(
            relay = when { peers.any { it.connected } -> RelayHealth.Connected; peers.isNotEmpty() -> RelayHealth.Connecting; else -> RelayHealth.Disconnected },
            activeStreams = peers.sumOf { it.streams },
            bytesUp = peers.sumOf { it.bytesUp }, bytesDown = peers.sumOf { it.bytesDown },
            errorClass = peers.firstOrNull { it.error != ErrorClass.None }?.error ?: ErrorClass.None,
        ) }
    }
    private class Entry(val record: DirectRecord, val network: Network) {
        var job: Job? = null
        @Volatile var session: DirectPeerConnection? = null
        var callbacksEnabled = false // Accessed only under the supervisor lock.
    }
    private val removeObserver = registry.removals.observeBlocked { id ->
        val session = synchronized(lock) {
            revision++
            detachLocked(id)
        }
        // Removal is invoked on IO. Abort this transport before attempting credential persistence.
        session?.close()
    }
    init { scope.coroutineContext[Job]?.invokeOnCompletion { removeObserver() } }

    fun close() { removeObserver(); stop() }
    fun retry(id: String): Boolean {
        synchronized(lock) {
            if (desiredNetwork == null) return false
            revision++
            stopLocked(id)
        }
        reconcile()
        return synchronized(lock) { desiredNetwork != null && id in entries }
    }

    /** Selection is serialized with the service's desired lifecycle/network state. */
    fun selectNetwork(network: Network) = synchronized(lock) {
        if (desiredNetwork !== network) {
            revision++
            desiredNetwork = network
            entries.keys.toList().forEach(::stopLocked)
        }
    }
    fun reconcile() {
        val (network, expectedRevision) = synchronized(lock) { (desiredNetwork ?: return) to ++revision }
        val records = try { registry.snapshot().records.filter { it.enabled } } catch (_: Exception) { return }
        synchronized(lock) {
            if (desiredNetwork !== network || revision != expectedRevision) return
            entries.keys.toList().forEach { id ->
                val entry = entries.getValue(id)
                val desired = records.find { it.clientId == id }
                if (desired == null || entry.network !== network || desired.keyAlias != entry.record.keyAlias ||
                    desired.endpoint != entry.record.endpoint || desired.generation != entry.record.generation) stopLocked(id)
            }
            records.forEach { record ->
                if (!registry.removals.isBlocked(record.clientId) && record.clientId !in entries) {
                    val entry = Entry(record, network)
                    entries[record.clientId] = entry
                    entry.job = scope.launch(workerDispatcher) { runPeer(entry) }
                }
            }
        }
    }
    fun stop() = synchronized(lock) {
        revision++
        desiredNetwork = null
        entries.keys.toList().forEach(::stopLocked)
    }
    private fun stopLocked(id: String) {
        val session = detachLocked(id)
        if (session != null) scope.launch(workerDispatcher) { session.close() }
    }
    private fun detachLocked(id: String): DirectPeerConnection? {
        val entry = entries.remove(id) ?: return null
        tracker.remove(id)
        entry.job?.cancel()
        val session = entry.session
        entry.session = null
        return session
    }
    private fun current(entry: Entry) = synchronized(lock) {
        entries[entry.record.clientId] === entry && !registry.removals.isBlocked(entry.record.clientId)
    }

    private suspend fun runPeer(entry: Entry) {
        val id = entry.record.clientId
        var attempt = 0
        while (currentCoroutineContext().isActive && current(entry)) {
            val epoch = synchronized(lock) {
                if (!current(entry)) return
                tracker.begin(id)
            }
            val ended = CompletableDeferred<ErrorClass>()
            var recovery = DirectRecovery.None
            var session: DirectPeerConnection? = null
            fun updateActive(transform: (PeerRuntime) -> PeerRuntime) = synchronized(lock) {
                if (current(entry) && entry.callbacksEnabled && session != null && entry.session === session) {
                    tracker.update(id, epoch, transform)
                }
            }
            try {
                val record = maintain(id, entry.network)
                currentCoroutineContext().ensureActive()
                if (!current(entry) || !registry.get(id).enabled) return
                session = connectionFactory(
                    entry.network, record,
                    object : AgentSessionListener {
                        override fun onConnected() {
                            updateActive { it.copy(connected = true, error = ErrorClass.None, recovery = DirectRecovery.None) }
                        }
                        override fun onTerminated(errorClass: ErrorClass) { ended.complete(errorClass) }
                    },
                    object : AgentTargetStatusSink {
                        override fun onActiveStreams(count: Int) { updateActive { it.copy(streams = count) } }
                        override fun onBytesDown(byteCount: Int) { updateActive { it.copy(bytesDown = it.bytesDown + byteCount) } }
                        override fun onBytesUp(byteCount: Int) { updateActive { it.copy(bytesUp = it.bytesUp + byteCount) } }
                        override fun onError(errorClass: ErrorClass) { updateActive { it.copy(error = errorClass) } }
                    },
                )
                synchronized(lock) {
                    if (!current(entry)) { session.close(); return }
                    entry.session = session
                    entry.callbacksEnabled = true
                }
                session.connect()
                while (current(entry)) {
                    val error = withTimeoutOrNull(30_000) { ended.await() }
                    if (error != null) {
                        tracker.update(id, epoch) { it.copy(connected = false, streams = 0, error = error) }
                        break
                    }
                    attempt = 0
                    val updated = maintain(id, entry.network)
                    if (updated.endpoint != record.endpoint || updated.identity?.serial != record.identity?.serial) break
                }
            } catch (canceled: CancellationException) { throw canceled }
            catch (error: Exception) {
                recovery = directRecovery(error)
                synchronized(lock) {
                    entry.callbacksEnabled = false
                    tracker.update(id, epoch) { it.copy(connected = false, streams = 0, error = recovery.error, recovery = recovery) }
                }
            }
            finally {
                synchronized(lock) { entry.callbacksEnabled = false }
                withContext(NonCancellable + workerDispatcher) { session?.close() }
                synchronized(lock) { if (entry.session === session) entry.session = null }
                tracker.update(id, epoch) { it.copy(connected = false, streams = 0) }
            }
            val ceiling = (2_000L shl attempt.coerceAtMost(4)).coerceAtMost(30_000L)
            if (!recovery.automaticRetry) return
            attempt++
            delay(kotlin.random.Random.nextLong(ceiling / 2, ceiling + 1))
        }
    }
}
