package com.mobileegress.agent.direct

import android.net.Network
import com.mobileegress.agent.security.DeviceKeyStore
import com.mobileegress.agent.session.AgentSessionListener
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.*
import org.junit.Assert.*
import org.junit.Test

@OptIn(ExperimentalCoroutinesApi::class)
class DirectRetryActionTest {
    @Test fun rowRetryResumesPausedStorageOrClockPeerEvenWhenNextMaintenanceIsUnavailable() = runTest {
        for (paused in listOf("credential_storage_unavailable", "client_clock_invalid")) {
            val records = listOf("a", "b").map(::record)
            val registry = DirectRegistry(MemoryDirectPersistence(DirectRegistryState(records = records)))
            var attempts = 0
            val connections = mutableMapOf<String, MutableList<FakeConnection>>()
            val supervisor = DirectPeerSupervisor(registry, DeviceKeyStore(), backgroundScope,
                maintain = { id, _ ->
                    if (id == "a") when (++attempts) {
                        1 -> throw DirectException(paused)
                        2 -> throw DirectException("client_connection_failed")
                    }
                    registry.get(id)
                }, workerDispatcher = StandardTestDispatcher(testScheduler),
                connectionFactory = { _, saved, listener, _ ->
                    FakeConnection(listener).also { connections.getOrPut(saved.clientId) { mutableListOf() }.add(it) }
                })
            val removeServiceRetries = DirectRetrySignals.register(supervisor::retry)
            try {
                supervisor.selectNetwork(network()); supervisor.reconcile(); runCurrent()
                assertFalse(DirectRuntimeBus.peers.value.single { it.clientId == "a" }.recovery.automaticRetry)
                advanceTimeBy(65_000); runCurrent()
                assertEquals(1, attempts)
                var standaloneAttempts = 0
                // Exercise the production row action, including its service handoff, not supervisor.retry directly.
                runCatching { retryDirectClient("a", registry) {
                    standaloneAttempts++
                    throw DirectException("client_connection_failed")
                } }
                runCurrent(); advanceTimeBy(2_001); runCurrent()
                assertEquals(3, attempts)
                assertTrue(DirectRuntimeBus.peers.value.single { it.clientId == "a" }.connected)
                assertEquals(0, standaloneAttempts)
                assertFalse(connections.getValue("b").single().closed)
                assertEquals(records, registry.snapshot().records)
            } finally { removeServiceRetries(); supervisor.close(); runCurrent() }
        }
    }

    @Test fun rowRetryKeepsDisabledAndRemovalPendingRecordsStopped() = runTest {
        val disabled = record("disabled").copy(enabled = false)
        val removing = record("removing")
        val registry = DirectRegistry(MemoryDirectPersistence(DirectRegistryState(records = listOf(disabled, removing))))
        registry.removals.block(removing.clientId)
        for (id in listOf(disabled.clientId, removing.clientId, "removed")) {
            var networkAttempts = 0
            runCatching { retryDirectClient(id, registry) { networkAttempts++; record(id) } }
            assertEquals(0, networkAttempts)
        }
        assertFalse(registry.get(disabled.clientId).enabled)
        assertTrue(registry.get(removing.clientId).removalPending)
    }

    @Test fun rowRetryWithoutAServiceDoesNotPromiseAutomaticNetworkRecovery() = runTest {
        val registry = DirectRegistry(MemoryDirectPersistence(DirectRegistryState(records = listOf(record("a")))))
        DirectRuntimeBus.publish(listOf(PeerRuntime("a", 1, recovery = DirectRecovery.UnlockStorage)))
        try {
            for (code in listOf("client_connection_failed", "client_storage_unavailable", "invalid_response")) {
                val error = runCatching { retryDirectClient("a", registry) { throw DirectException(code) } }
                    .exceptionOrNull() as Exception
                val recovery = directRecovery(error)
                assertFalse("No service owns retries for $code", recovery.automaticRetry)
                assertTrue(recovery.message.contains("Retry"))
            }
        } finally { DirectRuntimeBus.publish(emptyList()) }
    }

    @Test fun stoppedSupervisorDeclinesHandoffAndRunsOnlyOneStandaloneAttempt() = runTest {
        val saved = record("a")
        val registry = DirectRegistry(MemoryDirectPersistence(DirectRegistryState(records = listOf(saved))))
        var attempts = 0
        val supervisor = DirectPeerSupervisor(registry, DeviceKeyStore(), backgroundScope,
            maintain = { _, _ -> attempts++; throw DirectException("credential_storage_unavailable") },
            workerDispatcher = StandardTestDispatcher(testScheduler))
        val remove = DirectRetrySignals.register { id -> supervisor.stop(); supervisor.retry(id) }
        try {
            supervisor.selectNetwork(network()); supervisor.reconcile(); runCurrent()
            var standaloneAttempts = 0
            assertEquals(saved, retryDirectClient("a", registry) { standaloneAttempts++; saved })
            runCurrent(); advanceTimeBy(65_000); runCurrent()
            assertEquals(1, standaloneAttempts)
            assertEquals(1, attempts)
            assertTrue(DirectRuntimeBus.peers.value.isEmpty())
        } finally { remove(); supervisor.close(); runCurrent() }
    }

    @Test fun disablingDuringServiceHandoffCannotFallBackToNetworkMaintenance() = runTest {
        val registry = DirectRegistry(MemoryDirectPersistence(DirectRegistryState(records = listOf(record("a")))))
        val remove = DirectRetrySignals.register { id -> registry.setEnabled(id, false); false }
        try {
            var networkAttempts = 0
            retryDirectClient("a", registry) { networkAttempts++; registry.get(it) }
            assertEquals(0, networkAttempts)
            assertFalse(registry.get("a").enabled)
        } finally { remove() }
    }

    @Test fun rowRetryPausesAgainForTerminalIdentityFailureWithoutReplacingPairing() = runTest {
        val saved = record("a")
        val registry = DirectRegistry(MemoryDirectPersistence(DirectRegistryState(records = listOf(saved))))
        var attempts = 0
        val supervisor = DirectPeerSupervisor(registry, DeviceKeyStore(), backgroundScope,
            maintain = { _, _ ->
                throw DirectException(if (++attempts == 1) "credential_storage_unavailable" else "client_repair_required")
            }, workerDispatcher = StandardTestDispatcher(testScheduler))
        val remove = DirectRetrySignals.register(supervisor::retry)
        try {
            supervisor.selectNetwork(network()); supervisor.reconcile(); runCurrent()
            retryDirectClient("a", registry) { error("Live service must own maintenance") }
            runCurrent(); advanceTimeBy(65_000); runCurrent()
            assertEquals(2, attempts)
            assertEquals(DirectRecovery.RePairRequired, DirectRuntimeBus.peers.value.single().recovery)
            assertEquals(saved, registry.get("a"))
        } finally { remove(); supervisor.close(); runCurrent() }
    }

    @Test fun standaloneRetryPreservesCancellationAndTrustFailures() = runTest {
        val registry = DirectRegistry(MemoryDirectPersistence(DirectRegistryState(records = listOf(record("a")))))
        for (failure in listOf(CancellationException(), DirectException("client_trust_invalid"), DirectException("client_clock_invalid"),
            DirectException("removal_pending"), DirectException("removal_stop_not_saved"), DirectException("client_removed"))) {
            assertSame(failure, runCatching { retryDirectClient("a", registry) { throw failure } }.exceptionOrNull())
        }
    }

    private fun record(id: String) = DirectRecord(id, id, "https://client.example", id, "csr", "ca")
    private fun network() = Network::class.java.getDeclaredConstructor().apply { isAccessible = true }.newInstance()
    private class FakeConnection(val listener: AgentSessionListener) : DirectPeerConnection {
        var closed = false
        override fun connect() = listener.onConnected()
        override fun close() { closed = true }
    }
}
