package com.mobileegress.agent.direct

import android.net.Network
import com.mobileegress.agent.network.CellularNetworkAcquirer
import com.mobileegress.agent.network.readBoundedResponseBody
import com.mobileegress.agent.pairing.PairingBundleParser
import com.mobileegress.agent.security.AgentIdentity
import com.mobileegress.agent.security.DeviceKeyStore
import com.mobileegress.agent.pairing.EnrollmentCredentialKeys
import com.mobileegress.agent.security.PinnedTls
import java.io.StringReader
import java.time.Instant
import java.util.concurrent.TimeUnit
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.serialization.Serializable
import okhttp3.ConnectionSpec
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.TlsVersion
import org.bouncycastle.openssl.PEMParser
import org.bouncycastle.pkcs.PKCS10CertificationRequest

@Serializable data class DirectIssued(
    val certificatePem: String, val caCertificatePem: String, val serial: String, val role: String,
    val clientId: String, val pairingId: String, val generation: Long,
)
@Serializable private data class EnrollmentRequest(val clientId: String, val invitationId: String, val code: String, val role: String, val csrPem: String)
@Serializable private data class AckRequest(val clientId: String, val pairingId: String, val generation: Long)
@Serializable private data class RenewRequest(val clientId: String, val pairingId: String, val csrPem: String)
@Serializable private data class AckResponse(val status: String)
@Serializable private data class ConfigResponse(val update: String)
@Serializable private data class RejectionResponse(val error: String)

internal class DefinitiveEnrollmentRejection(val code: String) : Exception(code)
internal fun definitiveEnrollmentRejection(status: Int, raw: String): DefinitiveEnrollmentRejection? {
    val response = runCatching { DirectBundles.json.decodeFromString<RejectionResponse>(DirectBundles.strict(raw)) }.getOrNull()
        ?: return null
    return if ((status == 401 && response.error == "invitation_invalid") ||
        (status == 410 && response.error == "invitation_expired")) DefinitiveEnrollmentRejection(response.error) else null
}

/** Only call for a response received over this Client's CA-pinned, hostname-verified connection. */
internal fun directControlFailure(status: Int, contentType: String?, raw: String, authenticated: Boolean): DirectException {
    val response = if (contentType?.substringBefore(';')?.trim().equals("application/json", ignoreCase = true))
        runCatching { DirectBundles.json.decodeFromString<RejectionResponse>(DirectBundles.strict(raw)) }.getOrNull() else null
    return DirectException(when {
        authenticated && status == 401 && response?.error == "unauthorized" -> "client_repair_required"
        status == 503 && response?.error == "storage_unavailable" -> "client_storage_unavailable"
        else -> "client_request_rejected"
    })
}

interface DirectControl {
    fun enroll(record: DirectRecord): DirectIssued
    fun ack(record: DirectRecord)
    fun config(record: DirectRecord): String
    fun renew(record: DirectRecord): DirectIssued
}

class DirectHttpControl(private val network: Network, private val keys: DeviceKeyStore) : DirectControl {
    override fun enroll(record: DirectRecord): DirectIssued {
        val invitation = record.invitation ?: throw DirectException("invitation_unavailable")
        return issuedResponse(request(record, "enroll", DirectBundles.json.encodeToString(
            EnrollmentRequest(record.clientId, invitation.invitationId, invitation.capability, "agent", record.csrPem),
        ), authenticated = false, expectedStatus = 201))
    }
    override fun ack(record: DirectRecord) {
        val raw = request(record, "ack", DirectBundles.json.encodeToString(AckRequest(record.clientId, record.pairingId, record.generation)))
        val response = runCatching { DirectBundles.json.decodeFromString<AckResponse>(raw) }
            .getOrElse { throw DirectException("invalid_response") }
        if (response.status != "paired") throw DirectException("invalid_ack")
    }
    override fun config(record: DirectRecord): String = runCatching {
        DirectBundles.json.decodeFromString<ConfigResponse>(request(record, "config", null)).update
    }.getOrElse { if (it is DirectException) throw it else throw DirectException("invalid_response") }
    override fun renew(record: DirectRecord): DirectIssued = issuedResponse(request(record, "renew",
        DirectBundles.json.encodeToString(RenewRequest(record.clientId, record.pairingId, record.csrPem)), expectedStatus = 201))

    private fun issuedResponse(raw: String) = runCatching { DirectBundles.json.decodeFromString<DirectIssued>(raw) }
        .getOrElse { throw DirectException("invalid_response") }

    private fun request(record: DirectRecord, path: String, body: String?, authenticated: Boolean = true, expectedStatus: Int = 200): String {
        val ca = directCa(record)
        val manager = if (authenticated) {
            val identity = record.identity ?: throw DirectException("identity_unavailable")
            directLeaf(record)
            PinnedTls.deviceKeyManager(identity, directPrivateKey(record.keyAlias, keys::privateKey))
        } else null
        val client = PinnedTls.clientBuilder(network, PinnedTls.trustManager(ca), manager)
            .followRedirects(false).followSslRedirects(false)
            .connectionSpecs(listOf(ConnectionSpec.Builder(ConnectionSpec.RESTRICTED_TLS).tlsVersions(TlsVersion.TLS_1_3).build()))
            .callTimeout(30, TimeUnit.SECONDS).connectTimeout(10, TimeUnit.SECONDS).build()
        try {
            val builder = Request.Builder().url(DirectBundles.origin(record.endpoint) + "/v2/direct/" + path)
            if (body != null) {
                val bytes = body.encodeToByteArray()
                if (bytes.size > 65_536) throw DirectException("request_too_large")
                builder.post(bytes.toRequestBody("application/json".toMediaType()))
            }
            client.newCall(builder.build()).execute().use { response ->
                if (response.code != expectedStatus) {
                    val type = response.header("Content-Type")
                    val raw = if (type?.substringBefore(';')?.trim().equals("application/json", ignoreCase = true)) {
                        val source = response.body?.source() ?: throw DirectException("empty_response")
                        DirectBundles.text(readBoundedResponseBody(source, 65_536))
                    } else ""
                    if (path == "enroll") definitiveEnrollmentRejection(response.code, raw)?.let { throw it }
                    throw directControlFailure(response.code, type, raw, authenticated)
                }
                val source = response.body?.source() ?: throw DirectException("empty_response")
                return DirectBundles.strict(DirectBundles.text(readBoundedResponseBody(source, 256 * 1024)))
            }
        } catch (e: DefinitiveEnrollmentRejection) { throw e }
        catch (e: DirectException) { throw e }
        catch (_: Exception) { throw DirectException("client_connection_failed") }
        finally { client.connectionPool.evictAll(); client.dispatcher.executorService.shutdown() }
    }
}

fun verifyDirectIssued(record: DirectRecord, response: DirectIssued): AgentIdentity =
    verifyDirectIssued(record, response, Instant.now())

internal fun verifyDirectIssued(record: DirectRecord, response: DirectIssued, now: Instant): AgentIdentity {
    if (response.clientId != record.clientId || !DirectBundles.uuid(response.pairingId) ||
        response.role != "agent" || !Regex("^[0-9A-F]{1,64}$").matches(response.serial) ||
        response.generation <= 0 || (record.pairingId.isNotEmpty() &&
            (record.pairingId != response.pairingId || response.generation != record.generation))) {
        throw DirectException("identity_mismatch")
    }
    try {
        val ca = PairingBundleParser.parseCaCertificate(record.caCertificatePem, now)
        val returnedCa = PairingBundleParser.parseCaCertificate(response.caCertificatePem, now)
        require(ca.encoded.contentEquals(returnedCa.encoded))
        val chain = PinnedTls.parseCertificateChain(response.certificatePem)
        val leaf = chain.first()
        leaf.checkValidity(java.util.Date.from(now))
        leaf.verify(ca.publicKey)
        require(leaf.basicConstraints < 0 && leaf.extendedKeyUsage.orEmpty().contains("1.3.6.1.5.5.7.3.2"))
        require(leaf.serialNumber.toString(16).uppercase() == response.serial)
        require(chain.any { it.encoded.contentEquals(ca.encoded) })
        val csr = PEMParser(StringReader(record.csrPem)).use { it.readObject() as PKCS10CertificationRequest }
        require(leaf.publicKey.encoded.contentEquals(csr.subjectPublicKeyInfo.encoded))
    } catch (_: Exception) { throw DirectException("invalid_issued_identity") }
    return AgentIdentity(record.endpoint, "agent", response.serial, record.keyAlias, response.certificatePem, record.caCertificatePem)
}

/** Persists issued credentials before ACK; retries never generate a second key or identity. */
class DirectEnrollment(
    private val registry: DirectRegistry,
    private val retireKey: (String) -> Unit = {},
    private val now: () -> Instant = Instant::now,
    private val verify: (DirectRecord, DirectIssued) -> AgentIdentity = ::verifyDirectIssued,
) {
    fun recover(id: String, control: DirectControl): DirectRecord {
        registry.removals.requireAllowed(id)
        registry.pruneUnsentExpired(now()).forEach { retireKey(it.keyAlias) }
        var record = registry.get(id)
        if (record.stage == DirectStage.Pending) {
            record = registry.beginEnrollment(record)
            val issued = try { control.enroll(record) } catch (rejected: DefinitiveEnrollmentRejection) {
                registry.removeRejectedPending(record)?.let { retireKey(it.keyAlias) }
                throw rejected
            }
            record = registry.issued(record, verify(record, issued), issued.pairingId, issued.generation)
        }
        if (record.stage == DirectStage.AwaitingAck) {
            control.ack(record)
            record = registry.acknowledged(record)
        }
        return record
    }
}

class DirectRepository(
    val registry: DirectRegistry,
    private val keys: DeviceKeyStore,
    private val networks: CellularNetworkAcquirer,
) {
    suspend fun pair(encoded: String): DirectRecord = withContext(Dispatchers.IO) {
        val invitation = DirectBundles.invitation(encoded)
        val record = reserveDirectIdentity(registry, invitation, keys)
        networks.acquire().use { enrollment().recover(record.clientId, DirectHttpControl(it.network, keys)) }
    }
    suspend fun retry(id: String): DirectRecord = withContext(Dispatchers.IO) {
        networks.acquire().use { maintain(id, it.network) }
    }
    suspend fun maintain(id: String, network: Network): DirectRecord = withContext(Dispatchers.IO) {
        val control = DirectHttpControl(network, keys)
        var record = enrollment().recover(id, control)
        val leaf = directLeaf(record)
        if (leaf.notAfter.toInstant().isBefore(Instant.now().plusSeconds(7 * 86400))) {
            val issued = control.renew(record)
            record = registry.issued(record, verifyDirectIssued(record, issued), issued.pairingId, issued.generation)
            control.ack(record)
            record = registry.acknowledged(record)
        }
        val update = control.config(record)
        if (update.isNotEmpty()) {
            record = registry.endpoint(record, DirectBundles.endpoint(update, record))
            control.ack(record)
            record = registry.acknowledged(record)
        }
        record
    }
    suspend fun importEndpoint(encoded: String): DirectRecord = withContext(Dispatchers.IO) {
        val candidates = registry.snapshot().records.filter { it.identity != null }
        val match = candidates.firstNotNullOfOrNull { record ->
            runCatching { record to DirectBundles.endpoint(encoded, record) }.getOrNull()
        } ?: throw DirectException("invalid_endpoint_update")
        val updated = registry.endpoint(match.first, match.second)
        // Desired endpoint is durable even if the new address is temporarily unavailable.
        networks.acquire().use { enrollment().recover(updated.clientId, DirectHttpControl(it.network, keys)) }
    }
    private fun enrollment() = DirectEnrollment(registry, retireKey = ::retireKey)
    private fun retireKey(alias: String) { runCatching { keys.delete(alias) } }
    fun remove(id: String) { registry.remove(id)?.let { retireKey(it.keyAlias) } }
}

internal fun reserveDirectIdentity(registry: DirectRegistry, invitation: DirectInvitation, keys: EnrollmentCredentialKeys): DirectRecord {
    var newAlias: String? = null
    return try {
        registry.reserve(invitation, retireKey = { runCatching { keys.delete(it) } }) {
            val key = keys.create()
            newAlias = key.alias
            PendingKey(key.alias, keys.createCsrPem(key))
        }
    } catch (error: Exception) {
        newAlias?.let { alias ->
            // A failed rename/directory sync can have an unknown outcome. Never delete a key
            // until a successfully synchronized registry read proves it has no saved reference.
            val unreferenced = runCatching { registry.snapshot().records.none { it.keyAlias == alias } }.getOrDefault(false)
            if (unreferenced) runCatching { keys.delete(alias) }
        }
        throw error
    }
}
