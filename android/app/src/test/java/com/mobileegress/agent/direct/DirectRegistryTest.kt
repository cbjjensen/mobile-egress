package com.mobileegress.agent.direct

import com.mobileegress.agent.security.AgentIdentity
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import org.junit.Assert.*
import org.junit.Test

class DirectRegistryTest {
    @Test fun independentPhonesKeepDistinctPairingsForTheSameClient() {
        val firstStore = MemoryDirectPersistence()
        val secondStore = MemoryDirectPersistence()
        val first = DirectRegistry(firstStore)
        val second = DirectRegistry(secondStore)
        val firstPending = first.reserve(invitation(1)) { PendingKey("phone-a", "csr-a") }
        val secondPending = second.reserve(invitation(1).copy(invitationId = "second-invitation")) { PendingKey("phone-b", "csr-b") }
        val firstIssued = first.issued(firstPending,
            AgentIdentity("https://client.example", "agent", "AA", "phone-a", "cert-a", "ca"), "pair-a", 1)
        val secondIssued = second.issued(secondPending,
            AgentIdentity("https://client.example", "agent", "BB", "phone-b", "cert-b", "ca"), "pair-b", 1)
        first.acknowledged(firstIssued)
        val secondPaired = second.acknowledged(secondIssued)
        val firstPaired = DirectRegistry(firstStore).get(firstPending.clientId)
        val update = DirectEndpointPayload(firstPaired.clientId, "pair-a", 3, "https://updated.example", DirectTransport.Hosted)
        first.endpoint(firstPaired, update)
        assertEquals("stale_endpoint_update", assertThrows(DirectException::class.java) {
            second.endpoint(secondPaired, update)
        }.code)
        first.remove(firstPaired.clientId)
        assertTrue(first.snapshot().records.isEmpty())
        val surviving = DirectRegistry(secondStore).get(secondPending.clientId)
        assertEquals(secondPaired, surviving)
        assertEquals("pair-b", surviving.pairingId)
        assertEquals(1L, surviving.generation)
        assertEquals(DirectStage.Paired, surviving.stage)
    }

    @Test fun expiredUnsentReservationsReleaseSlotsForFreshPairing() {
        val store = MemoryDirectPersistence()
        val registry = DirectRegistry(store)
        repeat(10) { n -> registry.reserve(invitation(n)) { PendingKey("old$n", "csr$n") } }
        store.change { state -> state.copy(records = state.records.map { it.copy(invitation = it.invitation!!.copy(expiresAt = "2000-01-01T00:00:00Z")) }) }
        val retired = mutableListOf<String>()
        registry.reserve(invitation(11), retireKey = retired::add) { PendingKey("fresh", "csr") }
        assertEquals(listOf("fresh"), registry.snapshot().records.map { it.keyAlias })
        assertEquals((0..9).map { "old$it" }.toSet(), retired.toSet())
    }
    @Test fun concurrentReservationsNeverExceedTenIncludingDisabledAndPending() {
        val registry = DirectRegistry(MemoryDirectPersistence())
        val start = CountDownLatch(1)
        val pool = Executors.newFixedThreadPool(11)
        try {
            val attempts = (1..11).map { n -> pool.submit<Boolean> {
                start.await()
                runCatching { registry.reserve(invitation(n)) { PendingKey("key$n", "csr$n") } }.isSuccess
            } }
            start.countDown()
            assertEquals(10, attempts.count { it.get() })
            registry.snapshot().records.forEach { registry.setEnabled(it.clientId, false) }
            assertThrows(DirectException::class.java) { registry.reserve(invitation(12)) { PendingKey("k", "c") } }
            registry.remove(registry.snapshot().records.first().clientId)
            registry.reserve(invitation(12)) { PendingKey("k", "c") }
            assertEquals(10, registry.snapshot().records.size)
        } finally { pool.shutdownNow() }
    }
    @Test fun lostEnrollmentAndAcknowledgementReuseDurableKeyAndIdentity() {
        val store = MemoryDirectPersistence()
        val first = DirectRegistry(store).reserve(invitation(1)) { PendingKey("key", "csr") }
        val restarted = DirectRegistry(store)
        val retry = restarted.reserve(invitation(1)) { error("must not create another key") }
        assertEquals(first, retry)
        val issued = restarted.issued(retry, AgentIdentity("https://client.example", "agent", "AB", "key", "cert", "ca"), "pair", 1)
        assertEquals(DirectStage.AwaitingAck, DirectRegistry(store).get(first.clientId).stage)
        assertEquals(issued.identity, DirectRegistry(store).get(first.clientId).identity)
        restarted.acknowledged(issued)
        restarted.acknowledged(issued)
        assertEquals(DirectStage.Paired, restarted.get(first.clientId).stage)
        assertNull(restarted.get(first.clientId).invitation)
    }
    @Test fun removedPairCannotBeResurrectedByDelayedEnrollmentOrAck() {
        val registry = DirectRegistry(MemoryDirectPersistence())
        val pending = registry.reserve(invitation(1)) { PendingKey("key", "csr") }
        registry.remove(pending.clientId)
        assertThrows(DirectException::class.java) {
            registry.issued(pending, AgentIdentity("https://c", "agent", "AB", "key", "cert", "ca"), "pair", 1)
        }
        assertTrue(registry.snapshot().records.isEmpty())
    }
    private fun invitation(n: Int) = DirectInvitation(
        2, "mobile-egress-direct-invitation",
        "00000000-0000-0000-0000-" + n.toString().padStart(12, '0'), "Client $n",
        "https://client.example", "ca",
        "10000000-0000-0000-0000-" + n.toString().padStart(12, '0'), "cap", "2030-01-01T00:00:00Z", "agent",
    )
}
internal class MemoryDirectPersistence(initial: DirectRegistryState = DirectRegistryState()) : DirectPersistence {
    override val removalGate = DirectRemovalGate()
    private var state = initial
    @Synchronized override fun snapshot() = state
    @Synchronized override fun change(transform: (DirectRegistryState) -> DirectRegistryState): DirectRegistryState {
        state = transform(state)
        return state
    }
}
