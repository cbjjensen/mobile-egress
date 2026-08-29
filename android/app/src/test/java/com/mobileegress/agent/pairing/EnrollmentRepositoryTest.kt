package com.mobileegress.agent.pairing

import com.mobileegress.agent.security.AgentIdentity
import com.mobileegress.agent.security.DeviceKey
import java.security.KeyPairGenerator
import java.security.spec.ECGenParameterSpec
import java.time.Instant
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertThrows
import org.junit.Assert.assertTrue
import org.junit.Test

class EnrollmentRepositoryTest {
    @Test
    fun `old key cleanup failure cannot delete newly committed identity key`() = runTest {
        val oldIdentity = identity("old-alias")
        val newIdentity = identity("new-alias")
        val identities = FakeIdentityPersistence(oldIdentity)
        val keys = FakeCredentialKeys("new-alias", setOf("old-alias"), failDeleteAlias = "old-alias")
        val repository = repository(identities, keys, newIdentity)

        val result = repository.pair("owner-bundle")

        assertEquals(newIdentity, result)
        assertEquals(newIdentity, identities.current)
        assertTrue(keys.isUsable("new-alias"))
        assertEquals(listOf("old-alias"), keys.deleteAttempts)
    }

    @Test
    fun `new identity write failure preserves previous identity and deletes only new key`() {
        val oldIdentity = identity("old-alias")
        val identities = FakeIdentityPersistence(oldIdentity, failSave = true)
        val keys = FakeCredentialKeys("new-alias", setOf("old-alias"))
        val repository = repository(identities, keys, identity("new-alias"))

        assertThrows(IllegalStateException::class.java) {
            runTest { repository.pair("owner-bundle") }
        }

        assertEquals(oldIdentity, identities.current)
        assertTrue("old-alias" in keys.availableAliases)
        assertTrue("new-alias" !in keys.availableAliases)
        assertEquals(listOf("new-alias"), keys.deleteAttempts)
    }

    private fun repository(
        identities: FakeIdentityPersistence,
        keys: FakeCredentialKeys,
        enrolled: AgentIdentity,
    ) = EnrollmentRepository(
        decoder = PairingDecoder { _ -> testBundle() },
        credentialKeys = keys,
        identityPersistence = identities,
        enrollmentPerformer = EnrollmentPerformer { _, _, _ -> enrolled },
    )

    private fun testBundle(): PairingBundle {
        val caPem = testCaPem()
        return PairingBundle(
            version = 1,
            relayOrigin = "https://relay.example:8443",
            caCertificatePem = caPem,
            caCertificate = PairingBundleParser.parseCaCertificate(
                caPem,
                Instant.parse("2026-08-29T18:00:00Z"),
            ),
            capability = "one-use-high-entropy-capability",
            role = "agent",
            expiresAt = Instant.parse("2026-08-29T18:10:00Z"),
        )
    }

    private fun identity(alias: String) = AgentIdentity(
        relayOrigin = "https://relay.example:8443",
        role = "agent",
        serial = "A1",
        keyAlias = alias,
        certificatePem = "certificate",
        caCertificatePem = "ca",
    )

    private class FakeIdentityPersistence(
        var current: AgentIdentity?,
        private val failSave: Boolean = false,
    ) : AgentIdentityPersistence {
        override fun load(): AgentIdentity? = current

        override fun save(identity: AgentIdentity) {
            if (failSave) throw IllegalStateException("injected write failure")
            current = identity
        }
    }

    private class FakeCredentialKeys(
        private val createdAlias: String,
        initialAliases: Set<String>,
        private val failDeleteAlias: String? = null,
    ) : EnrollmentCredentialKeys {
        val availableAliases = initialAliases.toMutableSet()
        val deleteAttempts = mutableListOf<String>()

        override fun create(): DeviceKey {
            val keys = KeyPairGenerator.getInstance("EC").apply {
                initialize(ECGenParameterSpec("secp256r1"))
            }.generateKeyPair()
            availableAliases += createdAlias
            return DeviceKey(createdAlias, keys.private, keys.public)
        }

        override fun createCsrPem(deviceKey: DeviceKey): String = "test-csr"

        fun isUsable(alias: String): Boolean = alias in availableAliases

        override fun delete(alias: String) {
            deleteAttempts += alias
            if (alias == failDeleteAlias) throw IllegalStateException("injected delete failure")
            availableAliases -= alias
        }
    }
}
