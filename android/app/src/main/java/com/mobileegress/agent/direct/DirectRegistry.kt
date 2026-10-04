package com.mobileegress.agent.direct

import com.mobileegress.agent.security.AgentIdentity
import java.time.Instant
import kotlinx.serialization.Serializable
import kotlinx.serialization.SerialName

class DirectException(val code: String) : Exception(code)
@Serializable enum class DirectTransport {
    @SerialName("direct") Direct,
    @SerialName("hosted") Hosted,
}
@Serializable data class DirectInvitation(
    val version: Int, val type: String, val clientId: String, val displayName: String,
    val endpoint: String, val caCertificatePem: String, val invitationId: String,
    val capability: String, val expiresAt: String, val role: String,
    val transport: DirectTransport = DirectTransport.Direct,
)
@Serializable enum class DirectStage { Pending, AwaitingAck, Paired }
@Serializable data class DirectRecord(
    val clientId: String, val displayName: String, val endpoint: String,
    val keyAlias: String, val csrPem: String, val caCertificatePem: String,
    val invitation: DirectInvitation? = null, val identity: AgentIdentity? = null,
    val pairingId: String = "", val generation: Long = 0, val enabled: Boolean = true,
    val stage: DirectStage = DirectStage.Pending,
    // Missing metadata from an older build is an unknown outcome, never proof it was unsent.
    val enrollmentAttempted: Boolean = true,
    @kotlinx.serialization.Transient val removalPending: Boolean = false,
    val transport: DirectTransport = DirectTransport.Direct,
)
@Serializable data class DirectRegistryState(
    val version: Int = 2, val migrationRequired: Boolean = false, val records: List<DirectRecord> = emptyList(),
)
data class PendingKey(val alias: String, val csrPem: String)
interface DirectPersistence {
    val removalGate: DirectRemovalGate
    fun snapshot(): DirectRegistryState
    fun change(transform: (DirectRegistryState) -> DirectRegistryState): DirectRegistryState
}

class DirectRegistry(private val persistence: DirectPersistence) {
    val removals get() = persistence.removalGate
    fun snapshot() = persistence.snapshot().let { state -> state.copy(records = state.records.map {
        if (removals.isBlocked(it.clientId)) it.copy(enabled = false, removalPending = true) else it
    }) }
    fun get(id: String) = snapshot().records.find { it.clientId == id } ?: throw DirectException("client_removed")
    fun reserve(invitation: DirectInvitation, retireKey: (String) -> Unit = {}, create: () -> PendingKey): DirectRecord {
        removals.serialized {
            if (removals.isBlocked(invitation.clientId)) {
                // A completed Remove may leave a conservative latch if its cleanup write failed.
                // A fresh explicit pairing can clear it only after proving the old record is gone.
                if (persistence.snapshot().records.any { it.clientId == invitation.clientId }) throw DirectException("removal_pending")
                removals.clear(invitation.clientId)
            }
        }
        pruneUnsentExpired().forEach { retireKey(it.keyAlias) }
        val state = persistence.change { current ->
            removals.requireAllowed(invitation.clientId)
            val existing = current.records.find { it.clientId == invitation.clientId }
            if (existing != null) {
                if (existing.invitation != invitation) throw DirectException("client_already_paired")
                current
            } else {
                if (current.records.size >= 10) throw DirectException("ten_client_limit")
                val key = create()
                current.copy(records = current.records + DirectRecord(
                    invitation.clientId, invitation.displayName, invitation.endpoint, key.alias, key.csrPem,
                    invitation.caCertificatePem, invitation = invitation, enrollmentAttempted = false,
                    transport = invitation.transport,
                ))
            }
        }
        return state.records.single { it.clientId == invitation.clientId }
    }
    fun pruneUnsentExpired(now: Instant = Instant.now()): List<DirectRecord> {
        fun expired(record: DirectRecord) = !removals.isBlocked(record.clientId) && record.stage == DirectStage.Pending && record.identity == null &&
            !record.enrollmentAttempted &&
            runCatching { !Instant.parse(record.invitation?.expiresAt).isAfter(now) }.getOrDefault(false)
        if (persistence.snapshot().records.none(::expired)) return emptyList()
        var removed = emptyList<DirectRecord>()
        persistence.change { current ->
            removed = current.records.filter(::expired)
            current.copy(records = current.records - removed.toSet())
        }
        return removed
    }
    fun beginEnrollment(expected: DirectRecord): DirectRecord = edit(expected.clientId) {
        checkCurrent(it, expected)
        if (it.stage != DirectStage.Pending || it.identity != null || it.invitation != expected.invitation || it.csrPem != expected.csrPem) {
            throw DirectException("stale_operation")
        }
        it.copy(enrollmentAttempted = true)
    }
    /** Only a definitive pinned enrollment rejection may discard an attempted reservation. */
    fun removeRejectedPending(expected: DirectRecord): DirectRecord? {
        var removed: DirectRecord? = null
        persistence.change { current ->
            removed = current.records.find {
                !removals.isBlocked(it.clientId) && it.clientId == expected.clientId && it.keyAlias == expected.keyAlias && it.csrPem == expected.csrPem &&
                    it.invitation == expected.invitation && it.stage == DirectStage.Pending && it.identity == null &&
                    it.pairingId == expected.pairingId && it.generation == expected.generation
            }
            current.copy(records = current.records.filterNot { it === removed })
        }
        return removed
    }
    fun setEnabled(id: String, enabled: Boolean): DirectRecord = removals.serialized {
        val updated = edit(id, allowBlocked = true) { it.copy(enabled = enabled) }
        if (enabled) removals.clear(id)
        updated
    }
    fun remove(id: String): DirectRecord? = removals.serialized {
        removals.block(id)
        var removed: DirectRecord? = null
        persistence.change { current ->
            removed = current.records.find { it.clientId == id }
            current.copy(records = current.records.filterNot { it.clientId == id })
        }
        // The record is already durably gone; a failed latch cleanup must not prevent retiring
        // its now-unreferenced key. A future explicit pairing can retry that conservative latch.
        runCatching { removals.clear(id) }
        removed
    }
    fun issued(expected: DirectRecord, identity: AgentIdentity, pairingId: String, generation: Long): DirectRecord =
        edit(expected.clientId) { current ->
            checkCurrent(current, expected)
            if (identity.keyAlias != current.keyAlias || generation < current.generation || generation <= 0 ||
                (current.pairingId.isNotEmpty() && pairingId != current.pairingId)) throw DirectException("identity_mismatch")
            current.copy(identity = identity, pairingId = pairingId, generation = generation, stage = DirectStage.AwaitingAck)
        }
    fun acknowledged(expected: DirectRecord): DirectRecord = edit(expected.clientId) {
        checkCurrent(it, expected)
        it.copy(stage = DirectStage.Paired, invitation = null)
    }
    fun endpoint(expected: DirectRecord, update: DirectEndpointPayload): DirectRecord = edit(expected.clientId) {
        checkCurrent(it, expected)
        val endpoint = DirectBundles.origin(update.endpoint)
        if (update.clientId != it.clientId || update.pairingId != it.pairingId || update.generation < it.generation ||
            (update.generation == it.generation && (endpoint != DirectBundles.origin(it.endpoint) || update.transport != it.transport))) throw DirectException("stale_endpoint_update")
        it.copy(
            endpoint = endpoint, generation = update.generation, transport = update.transport,
            stage = if (update.generation == it.generation) it.stage else DirectStage.AwaitingAck,
            identity = it.identity?.copy(relayOrigin = endpoint),
        )
    }
    private fun edit(id: String, allowBlocked: Boolean = false, transform: (DirectRecord) -> DirectRecord): DirectRecord {
        val state = persistence.change { current ->
            if (!allowBlocked) removals.requireAllowed(id)
            if (current.records.none { it.clientId == id }) throw DirectException("client_removed")
            current.copy(records = current.records.map { if (it.clientId == id) transform(it) else it })
        }
        return state.records.single { it.clientId == id }
    }
    private fun checkCurrent(current: DirectRecord, expected: DirectRecord) {
        if (current.keyAlias != expected.keyAlias || current.pairingId != expected.pairingId ||
            current.generation != expected.generation || current.identity?.serial != expected.identity?.serial ||
            current.endpoint != expected.endpoint || current.transport != expected.transport) {
            throw DirectException("stale_operation")
        }
    }
}
@Serializable data class DirectEndpointPayload(val clientId: String, val pairingId: String, val generation: Long, val endpoint: String,
    val transport: DirectTransport = DirectTransport.Direct)
