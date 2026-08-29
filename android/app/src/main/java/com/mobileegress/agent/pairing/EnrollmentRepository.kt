package com.mobileegress.agent.pairing

import com.mobileegress.agent.network.CellularNetworkAcquirer
import com.mobileegress.agent.security.AgentIdentity
import com.mobileegress.agent.security.DeviceKeyStore
import com.mobileegress.agent.security.SecureIdentityStore
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

class EnrollmentRepository(
    private val parser: PairingBundleParser,
    private val networkAcquirer: CellularNetworkAcquirer,
    private val deviceKeyStore: DeviceKeyStore,
    private val identityStore: SecureIdentityStore,
    private val enrollmentClient: EnrollmentClient,
) {
    suspend fun pair(encodedBundle: String): AgentIdentity = withContext(Dispatchers.IO) {
        val bundle = parser.parse(encodedBundle)
        val oldIdentity = identityStore.load()
        val deviceKey = deviceKeyStore.create()
        try {
            val csr = deviceKeyStore.createCsrPem(deviceKey)
            val identity = networkAcquirer.acquire().use { lease ->
                enrollmentClient.enroll(bundle, lease.network, deviceKey, csr)
            }
            identityStore.save(identity)
            if (oldIdentity != null && oldIdentity.keyAlias != identity.keyAlias) {
                deviceKeyStore.delete(oldIdentity.keyAlias)
            }
            identity
        } catch (error: Exception) {
            deviceKeyStore.delete(deviceKey.alias)
            throw error
        }
    }
}
