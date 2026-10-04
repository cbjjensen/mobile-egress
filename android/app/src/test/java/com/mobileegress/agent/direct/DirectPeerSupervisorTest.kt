package com.mobileegress.agent.direct

import android.net.Network
import com.mobileegress.agent.security.AgentIdentity
import com.mobileegress.agent.security.DeviceKeyStore
import com.mobileegress.agent.session.AgentSessionListener
import com.mobileegress.agent.status.ErrorClass
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.*
import org.junit.Assert.*
import org.junit.Test

@OptIn(ExperimentalCoroutinesApi::class)
class DirectPeerSupervisorTest {
    @Test fun lateConnectedCallbackCannotClearPausedPairingRecovery() = runTest {
        val record = DirectRecord("a", "A", "https://client.example", "saved-key", "csr", "ca")
        val registry = DirectRegistry(MemoryDirectPersistence(DirectRegistryState(records = listOf(record))))
        var checks = 0
        lateinit var connection: FakeConnection
        val supervisor = DirectPeerSupervisor(registry, DeviceKeyStore(), backgroundScope,
            maintain = { _, _ -> if (++checks > 1) throw DirectException("client_repair_required") else record },
            workerDispatcher = StandardTestDispatcher(testScheduler),
            connectionFactory = { _, _, listener, _ -> FakeConnection(listener).also { connection = it } })
        val network = Network::class.java.getDeclaredConstructor().apply { isAccessible = true }.newInstance()
        supervisor.selectNetwork(network); supervisor.reconcile(); runCurrent()
        assertTrue(DirectRuntimeBus.peers.value.single().connected)
        advanceTimeBy(30_001); runCurrent()
        assertTrue(connection.closed)
        connection.listener.onConnected()
        assertFalse(DirectRuntimeBus.peers.value.single().connected)
        assertEquals(DirectRecovery.RePairRequired, DirectRuntimeBus.peers.value.single().recovery)
        supervisor.close(); runCurrent()
    }

    @Test fun explicitRetryResumesOnlyPeerWaitingForPhoneStorageOrClock() = runTest {
        for (code in listOf("credential_storage_unavailable", "client_clock_invalid")) {
            val records = listOf("a", "b").map { DirectRecord(it, it, "https://client.example", it, "csr", "ca") }
            val registry = DirectRegistry(MemoryDirectPersistence(DirectRegistryState(records = records)))
            var recovered = false
            var attempts = 0
            val connections = mutableMapOf<String, MutableList<FakeConnection>>()
            val supervisor = DirectPeerSupervisor(registry, DeviceKeyStore(), backgroundScope,
                maintain = { id, _ ->
                    if (id == "a") { attempts++; if (!recovered) throw DirectException(code) }
                    registry.get(id)
                }, workerDispatcher = StandardTestDispatcher(testScheduler),
                connectionFactory = { _, record, listener, _ -> FakeConnection(listener).also { connections.getOrPut(record.clientId) { mutableListOf() }.add(it) } })
            val network = Network::class.java.getDeclaredConstructor().apply { isAccessible = true }.newInstance()
            supervisor.selectNetwork(network); supervisor.reconcile(); runCurrent()
            assertFalse(DirectRuntimeBus.peers.value.single { it.clientId == "a" }.recovery.automaticRetry)
            advanceTimeBy(65_000); runCurrent()
            assertEquals(1, attempts)
            recovered = true
            supervisor.retry("a"); runCurrent()
            assertEquals(2, attempts)
            assertTrue(DirectRuntimeBus.peers.value.single { it.clientId == "a" }.connected)
            assertEquals(DirectRecovery.None, DirectRuntimeBus.peers.value.single { it.clientId == "a" }.recovery)
            assertFalse(connections.getValue("b").single().closed)
            assertEquals(records, registry.snapshot().records)
            supervisor.close(); runCurrent()
        }
    }

    @Test fun unavailableClientStorageRetriesAutomaticallyWithoutDroppingPairing() = runTest {
        val record = DirectRecord("a", "A", "https://client.example", "saved-key", "saved-csr", "ca")
        val registry = DirectRegistry(MemoryDirectPersistence(DirectRegistryState(records = listOf(record))))
        var attempts = 0
        val supervisor = DirectPeerSupervisor(registry, DeviceKeyStore(), backgroundScope,
            maintain = { _, _ -> attempts++; throw DirectException("client_storage_unavailable") },
            workerDispatcher = StandardTestDispatcher(testScheduler))
        val network = Network::class.java.getDeclaredConstructor().apply { isAccessible = true }.newInstance()
        supervisor.selectNetwork(network); supervisor.reconcile(); runCurrent()
        assertEquals(DirectRecovery.ClientStorageUnavailable, DirectRuntimeBus.peers.value.single().recovery)
        advanceTimeBy(2_001); runCurrent()
        assertEquals(2, attempts)
        assertEquals(record, registry.get("a"))
        supervisor.close(); runCurrent()
    }

    @Test fun revokedPeerNeedsRepairWithoutRetryStormOrStoppingHealthyPeer() = runTest {
        val records = listOf("a", "b").map { DirectRecord(it, it, "https://client.example", it, "csr", "ca") }
        val registry = DirectRegistry(MemoryDirectPersistence(DirectRegistryState(records = records)))
        var attempts = 0
        val connections = mutableListOf<FakeConnection>()
        val supervisor = DirectPeerSupervisor(registry, DeviceKeyStore(), backgroundScope,
            maintain = { id, _ -> if (id == "a") { attempts++; throw DirectException("client_repair_required") } else registry.get(id) },
            workerDispatcher = StandardTestDispatcher(testScheduler),
            connectionFactory = { _, _, listener, _ -> FakeConnection(listener).also(connections::add) })
        val network = Network::class.java.getDeclaredConstructor().apply { isAccessible = true }.newInstance()
        supervisor.selectNetwork(network); supervisor.reconcile(); runCurrent()
        assertEquals(ErrorClass.RelayAuth, DirectRuntimeBus.peers.value.single { it.clientId == "a" }.error)
        advanceTimeBy(65_000); runCurrent()
        assertEquals(1, attempts)
        assertFalse(connections.single().closed)
        assertEquals(2, registry.snapshot().records.size)
        supervisor.close(); runCurrent()
    }
    @Test fun failedRemovalStopsBeforeStorageAndCannotRestartTheSavedPeer() = runTest {
        val records = listOf("a", "b").map { DirectRecord(it, it, "https://client.example", it, "csr", "ca") }
        val memory = MemoryDirectPersistence(DirectRegistryState(records = records))
        val connections = mutableMapOf<String, MutableList<FakeConnection>>()
        var removing = false
        var readsFail = false
        val store = object : DirectPersistence {
            override val removalGate = memory.removalGate
            override fun snapshot(): DirectRegistryState {
                if (readsFail) throw DirectException("storage_failed")
                return memory.snapshot()
            }
            override fun change(transform: (DirectRegistryState) -> DirectRegistryState): DirectRegistryState {
                if (removing) {
                    assertTrue("Remove must close traffic before attempting the failing write", connections.getValue("a").single().closed)
                    throw DirectException("storage_failed")
                }
                return memory.change(transform)
            }
        }
        val registry = DirectRegistry(store)
        val supervisor = DirectPeerSupervisor(DirectRegistry(store), DeviceKeyStore(), backgroundScope,
            maintain = { id, _ -> registry.get(id) }, workerDispatcher = StandardTestDispatcher(testScheduler),
            connectionFactory = { _, record, listener, _ ->
                FakeConnection(listener).also { connections.getOrPut(record.clientId) { mutableListOf() }.add(it) }
            })
        val network = Network::class.java.getDeclaredConstructor().apply { isAccessible = true }.newInstance()
        supervisor.selectNetwork(network); supervisor.reconcile(); runCurrent()
        removing = true
        assertThrows(DirectException::class.java) { registry.remove("a") }
        runCurrent()
        assertFalse(connections.getValue("b").single().closed)
        readsFail = true
        supervisor.reconcile() // A storage failure must not kill the service's revision collector.
        assertFalse(connections.getValue("b").single().closed)
        readsFail = false
        supervisor.reconcile(); runCurrent()
        supervisor.stop(); supervisor.selectNetwork(network); supervisor.reconcile(); runCurrent()
        assertEquals(1, connections.getValue("a").size)
        assertEquals("a", memory.snapshot().records.single { it.clientId == "a" }.keyAlias)
        assertFalse(registry.get("a").enabled)
        supervisor.stop(); runCurrent()
    }
    @Test fun replacementNetworkInvalidatesOldReconcileSnapshot() = runTest {
        val loaded = CountDownLatch(1)
        val release = CountDownLatch(1)
        val first = AtomicBoolean(true)
        val record = DirectRecord("a", "a", "https://client.example", "key", "csr", "ca")
        val registry = DirectRegistry(object : DirectPersistence {
            override val removalGate = DirectRemovalGate()
            override fun snapshot(): DirectRegistryState {
                if (first.getAndSet(false)) { loaded.countDown(); check(release.await(5, TimeUnit.SECONDS)) }
                return DirectRegistryState(records = listOf(record))
            }
            override fun change(transform: (DirectRegistryState) -> DirectRegistryState) = error("unexpected write")
        })
        val connections = mutableListOf<Pair<Network, FakeConnection>>()
        val supervisor = DirectPeerSupervisor(registry, DeviceKeyStore(), backgroundScope,
            maintain = { _, _ -> record }, workerDispatcher = StandardTestDispatcher(testScheduler),
            connectionFactory = { network, _, listener, _ -> FakeConnection(listener).also { connections.add(network to it) } })
        fun network() = Network::class.java.getDeclaredConstructor().apply { isAccessible = true }.newInstance()
        val original = network()
        val replacement = network()
        val executor = Executors.newSingleThreadExecutor()
        try {
            supervisor.selectNetwork(original)
            val old = executor.submit { supervisor.reconcile() }
            assertTrue(loaded.await(5, TimeUnit.SECONDS))
            supervisor.stop()
            supervisor.selectNetwork(replacement)
            supervisor.reconcile()
            runCurrent()
            release.countDown(); old.get(5, TimeUnit.SECONDS); runCurrent()
            assertEquals(1, connections.size)
            assertSame(replacement, connections.single().first)
            assertFalse(connections.single().second.closed)
        } finally { release.countDown(); supervisor.stop(); executor.shutdownNow(); runCurrent() }
    }
    @Test fun stopWhileReconcileLoadsRegistryCannotRecreatePeer() = runTest {
        val loaded = CountDownLatch(1)
        val release = CountDownLatch(1)
        val record = DirectRecord("a", "a", "https://client.example", "key", "csr", "ca")
        val state = DirectRegistryState(records = listOf(record))
        val registry = DirectRegistry(object : DirectPersistence {
            override val removalGate = DirectRemovalGate()
            override fun snapshot(): DirectRegistryState {
                loaded.countDown()
                check(release.await(5, TimeUnit.SECONDS))
                return state
            }
            override fun change(transform: (DirectRegistryState) -> DirectRegistryState) = error("unexpected write")
        })
        val connections = mutableListOf<FakeConnection>()
        val supervisor = DirectPeerSupervisor(registry, DeviceKeyStore(), backgroundScope,
            maintain = { _, _ -> record }, workerDispatcher = StandardTestDispatcher(testScheduler),
            connectionFactory = { _, _, listener, _ -> FakeConnection(listener).also(connections::add) })
        val network = Network::class.java.getDeclaredConstructor().apply { isAccessible = true }.newInstance()
        val executor = Executors.newSingleThreadExecutor()
        try {
            supervisor.selectNetwork(network)
            val reconcile = executor.submit { supervisor.reconcile() }
            assertTrue(loaded.await(5, TimeUnit.SECONDS))
            supervisor.stop()
            release.countDown()
            reconcile.get(5, TimeUnit.SECONDS)
            runCurrent()
            assertTrue("Stop must invalidate the registry snapshot still being loaded", connections.isEmpty())
        } finally { release.countDown(); supervisor.stop(); executor.shutdownNow(); runCurrent() }
    }
    @Test fun failingPeerRetriesWithoutClosingOtherPeersAndRemovedCallbacksAreIgnored() = runTest {
        val records = listOf("a", "b").map { id ->
            DirectRecord(id, id, "https://client.example", id, "csr", "ca",
                identity = AgentIdentity("https://client.example", "agent", "AB", id, "cert", "ca"),
                pairingId = id, generation = 1, stage = DirectStage.Paired)
        }
        val registry = DirectRegistry(MemoryDirectPersistence(DirectRegistryState(records = records)))
        val connections = mutableMapOf<String, MutableList<FakeConnection>>()
        val supervisor = DirectPeerSupervisor(registry, DeviceKeyStore(), backgroundScope,
            maintain = { id, _ -> registry.get(id) },
            workerDispatcher = StandardTestDispatcher(testScheduler),
            connectionFactory = { _, record, listener, _ ->
                FakeConnection(listener).also { connections.getOrPut(record.clientId) { mutableListOf() }.add(it) }
            })
        val network = Network::class.java.getDeclaredConstructor().apply { isAccessible = true }.newInstance()
        supervisor.selectNetwork(network)
        supervisor.reconcile()
        runCurrent()
        assertEquals(2, DirectRuntimeBus.peers.value.count { it.connected })
        val firstA = connections.getValue("a").single()
        val firstB = connections.getValue("b").single()
        firstA.listener.onTerminated(ErrorClass.RelayUnavailable)
        runCurrent()
        assertFalse(firstB.closed)
        assertTrue(DirectRuntimeBus.peers.value.single { it.clientId == "b" }.connected)
        advanceTimeBy(2001); runCurrent()
        assertEquals(2, connections.getValue("a").size)
        assertEquals(1, connections.getValue("b").size)
        registry.remove("a")
        supervisor.reconcile()
        runCurrent()
        firstA.listener.onConnected()
        assertEquals(listOf("b"), DirectRuntimeBus.peers.value.map { it.clientId })
        supervisor.stop()
        runCurrent()
        assertTrue(firstB.closed)
        assertTrue(DirectRuntimeBus.peers.value.isEmpty())
    }
    private class FakeConnection(val listener: AgentSessionListener) : DirectPeerConnection {
        var closed = false
        override fun connect() = listener.onConnected()
        override fun close() { closed = true }
    }
}
