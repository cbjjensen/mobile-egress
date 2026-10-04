package com.mobileegress.agent.direct

import android.net.Network
import com.mobileegress.agent.security.AgentIdentity
import com.mobileegress.agent.security.DeviceKeyStore
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.*
import org.junit.Assert.*
import org.junit.Test

@OptIn(ExperimentalCoroutinesApi::class)
class DirectRemovalGateTest {
    @Test fun failedLatchCleanupDoesNotLoseRemovedKeyAndFreshPairingCanRecover() {
        val old = DirectRecord("a", "A", "https://client.example", "old-key", "csr", "ca")
        val memory = MemoryDirectPersistence(DirectRegistryState(records = listOf(old)))
        var rejectClear = true
        val registry = DirectRegistry(object : DirectPersistence {
            override val removalGate = DirectRemovalGate { if (rejectClear && it.isEmpty()) error("disk unavailable") }
            override fun snapshot() = memory.snapshot()
            override fun change(transform: (DirectRegistryState) -> DirectRegistryState) = memory.change(transform)
        })
        assertEquals(old, registry.remove("a")) // Caller can retire exactly this durably unreferenced key.
        assertTrue(memory.snapshot().records.isEmpty())
        assertTrue(registry.removals.isBlocked("a"))
        val invitation = DirectInvitation(2, "mobile-egress-direct-invitation", "a", "A", old.endpoint,
            "new-ca", "new-invite", "cap", "2030-01-01T00:00:00Z", "agent")
        assertThrows(DirectException::class.java) { registry.reserve(invitation) { error("must not create a key") } }
        rejectClear = false
        assertEquals("new-key", registry.reserve(invitation) { PendingKey("new-key", "csr") }.keyAlias)
        assertFalse(registry.removals.isBlocked("a"))
    }

    @Test fun pendingRemovalIsNotSilentlyPrunedOrDiscardedByLateRejection() {
        val invitation = DirectInvitation(2, "mobile-egress-direct-invitation", "a", "A", "https://client.example",
            "ca", "invite", "cap", "2000-01-01T00:00:00Z", "agent")
        val old = DirectRecord("a", "A", invitation.endpoint, "key", "csr", "ca", invitation = invitation, enrollmentAttempted = false)
        val memory = MemoryDirectPersistence(DirectRegistryState(records = listOf(old)))
        var fail = true
        val registry = DirectRegistry(object : DirectPersistence {
            override val removalGate = memory.removalGate
            override fun snapshot() = memory.snapshot()
            override fun change(transform: (DirectRegistryState) -> DirectRegistryState): DirectRegistryState {
                if (fail) error("storage unavailable")
                return memory.change(transform)
            }
        })
        assertThrows(IllegalStateException::class.java) { registry.remove("a") }
        fail = false
        assertTrue(registry.pruneUnsentExpired().isEmpty())
        assertNull(registry.removeRejectedPending(old))
        assertTrue(registry.get("a").removalPending)
        assertEquals("key", registry.remove("a")!!.keyAlias)
        assertTrue(registry.snapshot().records.isEmpty())
    }

    @Test fun persistedRemovalIntentSurvivesRelaunchUntilExplicitEnable() = runTest {
        var savedStops = emptySet<String>()
        val record = DirectRecord("a", "A", "https://client.example", "key", "csr", "ca")
        val memory = MemoryDirectPersistence(DirectRegistryState(records = listOf(record)))
        var writesFail = true
        fun store(gate: DirectRemovalGate) = object : DirectPersistence {
            override val removalGate = gate
            override fun snapshot() = memory.snapshot()
            override fun change(transform: (DirectRegistryState) -> DirectRegistryState): DirectRegistryState {
                if (writesFail) throw DirectException("storage_failed")
                return memory.change(transform)
            }
        }
        val registry = DirectRegistry(store(DirectRemovalGate(savedStops) { savedStops = it.toSet() }))
        assertThrows(DirectException::class.java) { registry.remove("a") }
        assertEquals(setOf("a"), savedStops)

        // New gate, registry and supervisor model a new process reading only the durable stop IDs.
        val restarted = DirectRegistry(store(DirectRemovalGate(savedStops) { savedStops = it.toSet() }))
        var connections = 0
        val supervisor = DirectPeerSupervisor(restarted, DeviceKeyStore(), backgroundScope,
            maintain = { id, _ -> restarted.get(id) }, workerDispatcher = StandardTestDispatcher(testScheduler),
            connectionFactory = { _, _, listener, _ ->
                connections++
                object : DirectPeerConnection {
                    override fun connect() = listener.onConnected()
                    override fun close() = Unit
                }
            })
        val network = Network::class.java.getDeclaredConstructor().apply { isAccessible = true }.newInstance()
        supervisor.selectNetwork(network); supervisor.reconcile(); runCurrent()
        assertEquals(0, connections)
        assertTrue(restarted.get("a").removalPending)
        assertFalse(restarted.get("a").enabled)
        assertThrows(DirectException::class.java) { restarted.setEnabled("a", true) }
        assertTrue(restarted.removals.isBlocked("a"))
        writesFail = false
        restarted.setEnabled("a", true)
        supervisor.reconcile(); runCurrent()
        assertEquals(1, connections)
        assertTrue(savedStops.isEmpty())
        assertFalse(restarted.get("a").removalPending)
        supervisor.close(); runCurrent()
    }

    @Test fun failedStopLatchSaveStillStopsTrafficBeforeReportingUncertainty() = runTest {
        val record = DirectRecord("a", "A", "https://client.example", "key", "csr", "ca")
        var closed = false
        var writes = 0
        val gate = DirectRemovalGate { throw IllegalStateException("disk unavailable") }
        val registry = DirectRegistry(object : DirectPersistence {
            override val removalGate = gate
            override fun snapshot() = DirectRegistryState(records = listOf(record))
            override fun change(transform: (DirectRegistryState) -> DirectRegistryState): DirectRegistryState {
                writes++; error("removal must not continue after the latch failed")
            }
        })
        val supervisor = DirectPeerSupervisor(registry, DeviceKeyStore(), backgroundScope,
            maintain = { _, _ -> record }, workerDispatcher = StandardTestDispatcher(testScheduler),
            connectionFactory = { _, _, listener, _ -> object : DirectPeerConnection {
                override fun connect() = listener.onConnected()
                override fun close() { closed = true }
            } })
        val network = Network::class.java.getDeclaredConstructor().apply { isAccessible = true }.newInstance()
        supervisor.selectNetwork(network); supervisor.reconcile(); runCurrent()
        val error = assertThrows(DirectException::class.java) { registry.remove("a") }
        assertEquals("removal_stop_not_saved", error.code)
        assertTrue(closed)
        assertEquals(0, writes)
        assertTrue(gate.isBlocked("a"))
        assertTrue(registry.get("a").removalPending)
        assertEquals("key", registry.get("a").keyAlias)
        supervisor.close(); runCurrent()
    }

    @Test fun retryRemovalClearsLatchOnlyAfterDurableRemovalAndLateIdentityCannotReturn() {
        val memory = MemoryDirectPersistence(DirectRegistryState(records = listOf(
            DirectRecord("a", "A", "https://client.example", "old-key", "csr", "ca"),
        )))
        var fail = true
        val registry = DirectRegistry(object : DirectPersistence {
            override val removalGate = memory.removalGate
            override fun snapshot() = memory.snapshot()
            override fun change(transform: (DirectRegistryState) -> DirectRegistryState): DirectRegistryState {
                if (fail) throw DirectException("storage_failed")
                return memory.change(transform)
            }
        })
        val pending = registry.get("a")
        assertThrows(DirectException::class.java) { registry.remove("a") }
        fail = false
        val identity = AgentIdentity(pending.endpoint, "agent", "AB", pending.keyAlias, "cert", "ca")
        assertThrows(DirectException::class.java) { registry.issued(pending, identity, "pair", 1) }
        assertEquals("old-key", registry.remove("a")!!.keyAlias)
        assertFalse(registry.removals.isBlocked("a"))
        assertTrue(registry.snapshot().records.isEmpty())
        assertThrows(DirectException::class.java) { registry.issued(pending, identity, "pair", 1) }
        val invitation = DirectInvitation(2, "mobile-egress-direct-invitation", "a", "A", pending.endpoint,
            "new-ca", "new-invite", "cap", "2030-01-01T00:00:00Z", "agent")
        val replacement = registry.reserve(invitation) { PendingKey("new-key", "new-csr") }
        assertEquals("new-key", replacement.keyAlias)
        assertTrue(registry.get("a").enabled)
    }
}
