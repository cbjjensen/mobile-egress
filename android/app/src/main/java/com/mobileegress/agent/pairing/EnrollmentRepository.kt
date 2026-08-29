package com.mobileegress.agent.pairing

import com.mobileegress.agent.network.CellularNetworkAcquirer
import com.mobileegress.agent.security.AgentIdentity
import com.mobileegress.agent.security.DeviceKey
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

interface EnrollmentCredentialKeys {
    fun create(): DeviceKey
    fun createCsrPem(deviceKey: DeviceKey): String
    fun delete(alias: String)
}

interface AgentIdentityPersistence {
    fun load(): AgentIdentity?
    fun save(identity: AgentIdentity)
}

fun interface EnrollmentPerformer {
    suspend fun enroll(bundle: PairingBundle, deviceKey: DeviceKey, csrPem: String): AgentIdentity
}

class CellularEnrollmentPerformer(
    private val networkAcquirer: CellularNetworkAcquirer,
    private val enrollmentClient: EnrollmentClient,
) : EnrollmentPerformer {
    override suspend fun enroll(
        bundle: PairingBundle,
        deviceKey: DeviceKey,
        csrPem: String,
    ): AgentIdentity = networkAcquirer.acquire().use { lease ->
        enrollmentClient.enroll(bundle, lease.network, deviceKey, csrPem)
    }
}

class EnrollmentRepository(
    private val decoder: PairingDecoder,
    private val credentialKeys: EnrollmentCredentialKeys,
    private val identityPersistence: AgentIdentityPersistence,
    private val enrollmentPerformer: EnrollmentPerformer,
) {
    suspend fun pair(encodedBundle: String): AgentIdentity = withContext(Dispatchers.IO) {
        val bundle = decoder.parse(encodedBundle)
        val previousIdentity = identityPersistence.load()
        val newDeviceKey = credentialKeys.create()
        val identity = try {
            val csr = credentialKeys.createCsrPem(newDeviceKey)
            enrollmentPerformer.enroll(bundle, newDeviceKey, csr).also { enrolled ->
                require(enrolled.keyAlias == newDeviceKey.alias) {
                    "Enrolled identity does not match the generated AndroidKeyStore key"
                }
                identityPersistence.save(enrolled)
            }
        } catch (error: Exception) {
            try {
                credentialKeys.delete(newDeviceKey.alias)
            } catch (_: Exception) {
                // Preserve the enrollment or persistence failure as the primary error.
            }
            throw error
        }
        if (previousIdentity != null && previousIdentity.keyAlias != identity.keyAlias) {
            try {
                credentialKeys.delete(previousIdentity.keyAlias)
            } catch (_: Exception) {
                // New credentials are already durable. Old-key cleanup is nonfatal.
            }
        }
        identity
    }
}
