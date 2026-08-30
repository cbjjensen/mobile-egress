package com.mobileegress.agent.migration

import com.mobileegress.agent.pairing.AgentIdentityPersistence
import com.mobileegress.agent.pairing.testCaPem
import com.mobileegress.agent.security.AgentIdentity
import java.time.Instant
import java.util.Base64
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import org.junit.Assert.assertEquals
import org.junit.Assert.assertThrows
import org.junit.Test

class EndpointMigrationTest {
    private val now = Instant.parse("2026-08-29T18:00:00Z")
    private val caPem = testCaPem()
    private val parser = EndpointMigrationParser { now }

    @Test
    fun `parses the distinct one-use endpoint migration payload`() {
        val migration = parser.parse(bundle())

        assertEquals("agent-endpoint-migration", migration.type)
        assertEquals("https://new-name.ts.net:8443", migration.relayOrigin)
        assertEquals("one-use-migration-capability", migration.capability)
    }

    @Test
    fun `rejects enrollment payloads unknown fields expiry and insecure origins`() {
        listOf(
            bundle(type = "agent-enrollment"),
            bundle(expiresAt = "2026-08-29T18:00:00Z"),
            bundle(relayUrl = "http://new-name.ts.net:8443"),
            rawBundle(extra = true),
        ).forEach { encoded ->
            assertThrows(EndpointMigrationException::class.java) { parser.parse(encoded) }
        }
    }

    @Test
    fun `migration retains device identity and changes only verified endpoint`() = runTest {
        val original = identity()
        val identities = FakeIdentities(original)
        val repository = EndpointMigrationRepository(
            decoder = parser,
            identityPersistence = identities,
            performer = EndpointMigrationPerformer { migration, identity ->
                assertEquals(original.keyAlias, identity.keyAlias)
                migration.relayOrigin
            },
        )

        val migrated = repository.migrate(bundle())

        assertEquals(original.copy(relayOrigin = "https://new-name.ts.net:8443"), migrated)
        assertEquals(original.keyAlias, migrated.keyAlias)
        assertEquals(original.certificatePem, migrated.certificatePem)
    }

    @Test
    fun `migration refuses a different relay CA before consuming capability`() = runTest {
        var calls = 0
        val repository = EndpointMigrationRepository(
            decoder = parser,
            identityPersistence = FakeIdentities(identity().copy(caCertificatePem = testCaPem())),
            performer = EndpointMigrationPerformer { _, _ -> calls++; "https://new-name.ts.net:8443" },
        )

        val error = runCatching { repository.migrate(bundle()) }.exceptionOrNull()
        assertEquals(EndpointMigrationException::class.java, error?.javaClass)
        assertEquals(0, calls)
    }

    private fun identity() = AgentIdentity(
        relayOrigin = "https://old-name.ts.net:8443",
        role = "agent",
        serial = "A1",
        keyAlias = "mobile_egress_device_existing",
        certificatePem = "existing-certificate",
        caCertificatePem = caPem,
    )

    private fun bundle(
        type: String = "agent-endpoint-migration",
        relayUrl: String = "https://new-name.ts.net:8443",
        expiresAt: String = "2026-08-29T18:10:00Z",
    ): String = rawBundle(type, relayUrl, expiresAt).encode()

    private fun rawBundle(
        type: String = "agent-endpoint-migration",
        relayUrl: String = "https://new-name.ts.net:8443",
        expiresAt: String = "2026-08-29T18:10:00Z",
        extra: Boolean = false,
    ): String = buildJsonObject {
        put("version", 1)
        put("type", type)
        put("relayUrl", relayUrl)
        put("caCertificatePem", caPem)
        put("capability", "one-use-migration-capability")
        put("expiresAt", expiresAt)
        if (extra) put("unexpected", true)
    }.toString()

    private fun String.encode(): String = Base64.getUrlEncoder().withoutPadding().encodeToString(toByteArray())

    private class FakeIdentities(var identity: AgentIdentity?) : AgentIdentityPersistence {
        override fun load(): AgentIdentity? = identity
        override fun save(identity: AgentIdentity) { this.identity = identity }
    }
}
