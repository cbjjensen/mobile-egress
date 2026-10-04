package com.mobileegress.agent.direct

import com.mobileegress.agent.security.AgentIdentity
import java.time.Instant
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import org.junit.Assert.*
import org.junit.Test

class DirectPendingCleanupTest {
    private val now = Instant.parse("2027-01-01T00:00:00Z")
    private val invitation = DirectInvitation(2, "mobile-egress-direct-invitation", "client", "Client",
        "https://client.example", "ca", "invitation", "capability", "2030-01-01T00:00:00Z", "agent")
    private fun reserve(registry: DirectRegistry, alias: String = "key") = registry.reserve(invitation) { PendingKey(alias, "csr-$alias") }
    private fun issued(record: DirectRecord) = DirectIssued("certificate", "ca", "AB", "agent", record.clientId, "pair", 1)
    private fun identity(record: DirectRecord, issued: DirectIssued) = AgentIdentity(record.endpoint, "agent", issued.serial, record.keyAlias, issued.certificatePem, record.caCertificatePem)
    private fun control(enroll: (DirectRecord) -> DirectIssued) = object : DirectControl {
        override fun enroll(record: DirectRecord) = enroll(record)
        override fun ack(record: DirectRecord) = Unit
        override fun config(record: DirectRecord) = ""
        override fun renew(record: DirectRecord) = error("unexpected renewal")
    }

    @Test fun definitivePinnedRejectionsReleaseSlotAndRetireOnlyOldKey() {
        for ((status, code) in listOf(401 to "invitation_invalid", 410 to "invitation_expired")) {
            val registry = DirectRegistry(MemoryDirectPersistence())
            val pending = reserve(registry)
            val retired = mutableListOf<String>()
            val enrollment = DirectEnrollment(registry, retired::add, { now }, ::identity)
            assertThrows(DefinitiveEnrollmentRejection::class.java) {
                enrollment.recover(pending.clientId, control { request ->
                    assertTrue(registry.get(request.clientId).enrollmentAttempted)
                    throw requireNotNull(definitiveEnrollmentRejection(status, "{\"error\":\"$code\"}"))
                })
            }
            assertTrue(registry.snapshot().records.isEmpty())
            assertEquals(listOf("key"), retired)
            val replacement = registry.reserve(invitation.copy(invitationId = "fresh")) { PendingKey("replacement", "fresh-csr") }
            assertEquals("replacement", replacement.keyAlias)
        }
    }

    @Test fun unknownIssuedResponseOutcomeSurvivesExpiryAndReopensWithSameCsr() {
        val store = MemoryDirectPersistence()
        val registry = DirectRegistry(store)
        val pending = reserve(registry)
        assertThrows(DirectException::class.java) {
            DirectEnrollment(registry, now = { now }, verify = ::identity).recover(pending.clientId, control {
                throw DirectException("client_connection_failed") // Server may have committed issuance.
            })
        }
        store.change { it.copy(records = it.records.map { record -> record.copy(invitation = invitation.copy(expiresAt = "2000-01-01T00:00:00Z")) }) }
        val restarted = DirectRegistry(store)
        assertTrue(restarted.pruneUnsentExpired(now).isEmpty())
        val recovered = DirectEnrollment(restarted, now = { now }, verify = ::identity).recover(pending.clientId, control {
            assertEquals(pending.keyAlias, it.keyAlias)
            assertEquals(pending.csrPem, it.csrPem)
            issued(it)
        })
        assertEquals(DirectStage.Paired, recovered.stage)
        assertEquals(pending.keyAlias, recovered.keyAlias)
    }

    @Test fun attemptedMarkerWinsConcurrentExpiryWhileResponseIsInFlight() {
        val store = MemoryDirectPersistence()
        val registry = DirectRegistry(store)
        val pending = reserve(registry)
        val entered = CountDownLatch(1)
        val release = CountDownLatch(1)
        val executor = Executors.newSingleThreadExecutor()
        try {
            val recovery = executor.submit<DirectRecord> {
                DirectEnrollment(registry, now = { now }, verify = ::identity).recover(pending.clientId, control {
                    entered.countDown(); check(release.await(5, TimeUnit.SECONDS)); issued(it)
                })
            }
            assertTrue(entered.await(5, TimeUnit.SECONDS))
            assertTrue(registry.get(pending.clientId).enrollmentAttempted)
            assertTrue(registry.pruneUnsentExpired(Instant.parse("2040-01-01T00:00:00Z")).isEmpty())
            release.countDown()
            assertEquals(DirectStage.Paired, recovery.get(5, TimeUnit.SECONDS).stage)
        } finally { release.countDown(); executor.shutdownNow() }
    }

    @Test fun lateRejectionCannotDeleteIdentityThatAnotherAttemptAlreadyPersisted() {
        val registry = DirectRegistry(MemoryDirectPersistence())
        val pending = reserve(registry)
        val retired = mutableListOf<String>()
        assertThrows(DefinitiveEnrollmentRejection::class.java) {
            DirectEnrollment(registry, retired::add, { now }, ::identity).recover(pending.clientId, control { request ->
                registry.issued(request, identity(request, issued(request)), "pair", 1)
                throw requireNotNull(definitiveEnrollmentRejection(401, "{\"error\":\"invitation_invalid\"}"))
            })
        }
        assertEquals(DirectStage.AwaitingAck, registry.get(pending.clientId).stage)
        assertTrue(retired.isEmpty())
    }

    @Test fun lateIssuedResponseAndRejectionCannotModifyReplacementReservation() {
        val registry = DirectRegistry(MemoryDirectPersistence())
        val pending = registry.beginEnrollment(reserve(registry))
        assertEquals(pending, registry.removeRejectedPending(pending))
        val replacement = reserve(registry, "replacement")
        assertNull(registry.removeRejectedPending(pending))
        assertThrows(DirectException::class.java) { registry.issued(pending, identity(pending, issued(pending)), "pair", 1) }
        assertEquals(replacement, registry.get(pending.clientId))
    }

    @Test fun failedDurableRemovalDoesNotDeletePendingKey() {
        val memory = MemoryDirectPersistence()
        var rejectRemoval = false
        val registry = DirectRegistry(object : DirectPersistence {
            override val removalGate = memory.removalGate
            override fun snapshot() = memory.snapshot()
            override fun change(transform: (DirectRegistryState) -> DirectRegistryState) = memory.change {
                val next = transform(it)
                if (rejectRemoval && next.records.isEmpty()) throw DirectException("storage_failed")
                next
            }
        })
        val pending = reserve(registry)
        val retired = mutableListOf<String>()
        assertThrows(DirectException::class.java) {
            DirectEnrollment(registry, retired::add, { now }, ::identity).recover(pending.clientId, control {
                rejectRemoval = true
                throw requireNotNull(definitiveEnrollmentRejection(410, "{\"error\":\"invitation_expired\"}"))
            })
        }
        assertTrue(retired.isEmpty())
        assertEquals("key", registry.get(pending.clientId).keyAlias)
    }

    @Test fun failureToPersistAttemptMarkerPreventsNetworkEnrollment() {
        val memory = MemoryDirectPersistence()
        var reject = false
        val registry = DirectRegistry(object : DirectPersistence {
            override val removalGate = memory.removalGate
            override fun snapshot() = memory.snapshot()
            override fun change(transform: (DirectRegistryState) -> DirectRegistryState): DirectRegistryState {
                if (reject) throw DirectException("storage_failed")
                return memory.change(transform)
            }
        })
        val pending = reserve(registry)
        reject = true
        var requests = 0
        assertThrows(DirectException::class.java) {
            DirectEnrollment(registry, now = { now }, verify = ::identity).recover(pending.clientId, control { requests++; issued(it) })
        }
        assertEquals(0, requests)
        assertFalse(memory.snapshot().records.single().enrollmentAttempted)
    }

    @Test fun oldMissingAttemptMarkerAndAmbiguousRejectionsNeverAuthorizeCleanup() {
        val record = DirectRecord("client", "Client", "https://client.example", "key", "csr", "ca", invitation = invitation.copy(expiresAt = "2000-01-01T00:00:00Z"))
        val raw = DirectBundles.json.encodeToString(record)
        val old = DirectBundles.json.decodeFromString<DirectRecord>(raw)
        assertTrue(old.enrollmentAttempted)
        assertTrue(DirectRegistry(MemoryDirectPersistence(DirectRegistryState(records = listOf(old)))).pruneUnsentExpired(now).isEmpty())
        for ((status, body) in listOf(403 to "{\"error\":\"invitation_invalid\"}", 401 to "{\"error\":\"unauthorized\"}",
            410 to "{\"error\":\"invitation_invalid\"}", 401 to "{\"error\":\"invitation_invalid\",\"extra\":true}",
            401 to "{\"error\":\"invitation_invalid\",\"error\":\"invitation_invalid\"}")) {
            assertNull(definitiveEnrollmentRejection(status, body))
        }
    }
}
