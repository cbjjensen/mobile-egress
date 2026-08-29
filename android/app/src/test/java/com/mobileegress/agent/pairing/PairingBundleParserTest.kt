package com.mobileegress.agent.pairing

import java.time.Instant
import java.util.Base64
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import org.junit.Assert.assertEquals
import org.junit.Assert.assertThrows
import org.junit.Test

class PairingBundleParserTest {
    private val now = Instant.parse("2026-08-29T18:00:00Z")
    private val parser = PairingBundleParser { now }

    @Test
    fun `parses an owner-issued versioned agent bundle`() {
        val encoded = bundle()

        val result = parser.parse(encoded)

        assertEquals(1, result.version)
        assertEquals("https://relay.example:8443", result.relayOrigin)
        assertEquals("agent", result.role)
        assertEquals("one-use-high-entropy-capability", result.capability)
        assertEquals(Instant.parse("2026-08-29T18:10:00Z"), result.expiresAt)
    }

    @Test
    fun `rejects insecure relay origins and non-agent roles`() {
        listOf(
            bundle(relayUrl = "http://relay.example:8443"),
            bundle(relayUrl = "https://relay.example:8443/path"),
            bundle(relayUrl = "https://relay.example:8443?query=yes"),
            bundle(role = "client"),
            bundle(role = "Agent"),
        ).forEach { encoded ->
            assertThrows(PairingBundleException::class.java) { parser.parse(encoded) }
        }
    }

    @Test
    fun `rejects missing trust capability or expiry`() {
        listOf(
            bundle(caPem = ""),
            bundle(caPem = "not-a-certificate"),
            bundle(capability = "   "),
            bundle(expiresAt = ""),
            bundle(expiresAt = "not-a-time"),
            bundle(expiresAt = "2026-08-29T17:59:59Z"),
            objectBundle(includeExpiry = false),
        ).forEach { encoded ->
            assertThrows(PairingBundleException::class.java) { parser.parse(encoded) }
        }
    }

    @Test
    fun `rejects unknown versions fields and padded base64`() {
        val unknownField = buildJsonObject {
            put("version", 1)
            put("relayUrl", "https://relay.example:8443")
            put("caCertificatePem", testCaPem())
            put("capability", "one-use-high-entropy-capability")
            put("role", "agent")
            put("expiresAt", "2026-08-29T18:10:00Z")
            put("unexpected", true)
        }.toString().encodeBase64Url()

        listOf(bundle(version = 2), unknownField, bundle() + "=").forEach { encoded ->
            assertThrows(PairingBundleException::class.java) { parser.parse(encoded) }
        }
    }

    private fun bundle(
        version: Int = 1,
        relayUrl: String = "https://relay.example:8443",
        caPem: String = testCaPem(),
        capability: String = "one-use-high-entropy-capability",
        role: String = "agent",
        expiresAt: String = "2026-08-29T18:10:00Z",
    ): String = buildJsonObject {
        put("version", version)
        put("relayUrl", relayUrl)
        put("caCertificatePem", caPem)
        put("capability", capability)
        put("role", role)
        put("expiresAt", expiresAt)
    }.toString().encodeBase64Url()

    private fun objectBundle(includeExpiry: Boolean): String = buildJsonObject {
        put("version", 1)
        put("relayUrl", "https://relay.example:8443")
        put("caCertificatePem", testCaPem())
        put("capability", "one-use-high-entropy-capability")
        put("role", "agent")
        if (includeExpiry) put("expiresAt", "2026-08-29T18:10:00Z")
    }.toString().encodeBase64Url()

    private fun String.encodeBase64Url(): String =
        Base64.getUrlEncoder().withoutPadding().encodeToString(toByteArray(Charsets.UTF_8))
}
