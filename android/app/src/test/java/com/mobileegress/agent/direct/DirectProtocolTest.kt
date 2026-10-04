package com.mobileegress.agent.direct

import com.mobileegress.agent.pairing.testCaPem
import com.mobileegress.agent.security.AgentIdentity
import java.math.BigInteger
import java.security.KeyPairGenerator
import java.security.Signature
import java.time.Instant
import java.util.Base64
import java.util.Date
import javax.security.auth.x500.X500Principal
import kotlinx.serialization.json.*
import org.bouncycastle.asn1.x509.BasicConstraints
import org.bouncycastle.asn1.x509.Extension
import org.bouncycastle.asn1.x509.KeyUsage
import org.bouncycastle.cert.jcajce.JcaX509CertificateConverter
import org.bouncycastle.cert.jcajce.JcaX509v3CertificateBuilder
import org.bouncycastle.operator.jcajce.JcaContentSignerBuilder
import org.junit.Assert.*
import org.junit.Test

class DirectProtocolTest {
    private val clientId = "00000000-0000-4000-8000-000000000001"
    private val pairingId = "00000000-0000-4000-8000-000000000002"
    private val now = Instant.parse("2026-10-03T00:00:00Z")
    @Test fun strictInvitationRejectsLegacyDuplicatesUnknownFieldsAndNoncanonicalEncoding() {
        val value = invitation()
        val encoded = encode(value.toByteArray())
        assertEquals("https://client.example", DirectBundles.invitation(encoded, now).endpoint)
        val invalid = listOf(
            value.replace("\"version\":2", "\"version\":2,\"version\":2"),
            value.replace("\"role\":\"agent\"", "\"role\":\"agent\",\"extra\":true"),
            value.replace("mobile-egress-direct-invitation", "agent-endpoint-migration"),
            value.replace("\"version\":2", "\"version\":1"),
            value.replace("2030-01-01T00:00:00Z", "2020-01-01T00:00:00Z"),
            value.replace("https://client.example/", "http://client.example/"),
        )
        invalid.forEach { raw -> assertThrows(DirectException::class.java) { DirectBundles.invitation(encode(raw.toByteArray()), now) } }
        assertThrows(DirectException::class.java) { DirectBundles.invitation(encoded + "=", now) }
        assertThrows(DirectException::class.java) { DirectBundles.invitation(encode("{\"version\":1}".toByteArray()), now) }
    }
    @Test fun signedEndpointSupportsSkippedGenerationsAndRejectsWrongPeerReplayAndTamper() {
        val fixture = authority()
        val registry = DirectRegistry(MemoryDirectPersistence())
        val invite = DirectBundles.invitation(encode(invitation(fixture.second).toByteArray()), now)
        val pending = registry.reserve(invite) { PendingKey("key", "csr") }
        val issued = registry.issued(pending, AgentIdentity(pending.endpoint, "agent", "AB", "key", "cert", fixture.second), pairingId, 1)
        registry.acknowledged(issued)
        val before = registry.get(clientId)
        fun bundle(generation: Long, id: String = clientId, endpoint: String = "https://new.example"): String {
            val payload = DirectBundles.json.encodeToString(DirectEndpointPayload(id, pairingId, generation, endpoint)).toByteArray()
            val signature = Signature.getInstance("SHA256withECDSA").run {
                initSign(fixture.first.private); update("MobileEgress-Direct-Endpoint-v2\n".toByteArray()); update(payload); sign()
            }
            return encode(buildJsonObject {
                put("version", 2); put("type", "mobile-egress-direct-endpoint-update")
                put("payload", encode(payload)); put("signature", encode(signature))
            }.toString().toByteArray())
        }
        val updated = registry.endpoint(before, DirectBundles.endpoint(bundle(8), before))
        assertEquals(8, updated.generation)
        assertEquals(before.keyAlias, updated.keyAlias)
        assertEquals(before.identity?.serial, updated.identity?.serial)
        assertEquals(DirectStage.AwaitingAck, updated.stage)
        assertEquals(8, DirectBundles.endpoint(bundle(8), updated).generation)
        assertThrows(DirectException::class.java) { DirectBundles.endpoint(bundle(7), updated) }
        assertThrows(DirectException::class.java) { DirectBundles.endpoint(bundle(8, endpoint = "https://conflict.example"), updated) }
        assertThrows(DirectException::class.java) { DirectBundles.endpoint(bundle(9, pairingId), updated) }
        val tampered = DirectBundles.text(DirectBundles.decode(bundle(9))).replace("\"signature\":\"", "\"signature\":\"AAAA")
        assertThrows(DirectException::class.java) { DirectBundles.endpoint(encode(tampered.toByteArray()), updated) }
    }
    @Test fun lostAckRetriesWithoutReissuingCredentials() {
        val registry = DirectRegistry(MemoryDirectPersistence())
        val pending = registry.reserve(DirectBundles.invitation(encode(invitation().toByteArray()), now)) { PendingKey("key", "csr") }
        var enrollments = 0
        var acks = 0
        val control = object : DirectControl {
            override fun enroll(record: DirectRecord): DirectIssued {
                enrollments++
                return DirectIssued("cert", record.caCertificatePem, "AB", "agent", clientId, pairingId, 1)
            }
            override fun ack(record: DirectRecord) { if (++acks == 1) throw DirectException("connection_lost") }
            override fun config(record: DirectRecord) = ""
            override fun renew(record: DirectRecord) = error("unexpected")
        }
        val enrollment = DirectEnrollment(registry) { r, issued -> AgentIdentity(r.endpoint, "agent", issued.serial, r.keyAlias, "cert", r.caCertificatePem) }
        assertThrows(DirectException::class.java) { enrollment.recover(clientId, control) }
        assertEquals(DirectStage.AwaitingAck, registry.get(clientId).stage)
        assertEquals(pending.keyAlias, registry.get(clientId).keyAlias)
        enrollment.recover(clientId, control)
        assertEquals(1, enrollments)
        assertEquals(2, acks)
        assertEquals(DirectStage.Paired, registry.get(clientId).stage)
    }
    @Test fun signedRetainedEndpointSpellingsNormalizeAfterVerification() {
        val authority = authority()
        val before = endpointRecord(authority.second)
        endpointSpellings.forEach { (signed, canonical) ->
            val payload = DirectEndpointPayload(clientId, pairingId, 2, signed)
            val bundle = signedEndpoint(authority.first, payload)
            val parsed = DirectBundles.endpoint(bundle, before, now)
            assertEquals(canonical, parsed.endpoint)
            val registry = DirectRegistry(MemoryDirectPersistence(DirectRegistryState(records = listOf(before))))
            val updated = registry.endpoint(before, parsed)
            assertEquals(canonical, updated.endpoint)
            assertEquals(canonical, updated.identity!!.relayOrigin)
            assertEquals(DirectStage.AwaitingAck, updated.stage)
            assertEquals(before.keyAlias, updated.keyAlias)

            val wrapper = DirectBundles.json.parseToJsonElement(DirectBundles.text(DirectBundles.decode(bundle))).jsonObject
            val changedBytes = DirectBundles.json.encodeToString(payload.copy(endpoint = canonical)).toByteArray()
            val tampered = encode(JsonObject(wrapper + ("payload" to JsonPrimitive(encode(changedBytes)))).toString().toByteArray())
            assertEquals("invalid_update_signature", assertThrows(DirectException::class.java) {
                DirectBundles.endpoint(tampered, before, now)
            }.code)
        }
    }
    @Test fun equalGenerationNormalizesEquivalentSavedAuthorityWithoutChangingIdentityOrStage() {
        val authority = authority()
        endpointSpellings.forEach { (saved, canonical) ->
            val before = endpointRecord(authority.second, saved)
            val registry = DirectRegistry(MemoryDirectPersistence(DirectRegistryState(records = listOf(before))))
            val bundle = signedEndpoint(authority.first, DirectEndpointPayload(clientId, pairingId, 1, canonical))
            val updated = registry.endpoint(before, DirectBundles.endpoint(bundle, before, now))
            assertEquals(before.copy(endpoint = canonical, identity = before.identity!!.copy(relayOrigin = canonical)), updated)
            assertEquals(updated, registry.endpoint(updated, DirectBundles.endpoint(bundle, updated, now)))
            listOf(
                DirectEndpointPayload(clientId, pairingId, 1, "https://other.example:8443"),
                DirectEndpointPayload(clientId, pairingId, 1, "https://client.example:9443"),
                DirectEndpointPayload(clientId, pairingId, 0, canonical),
                DirectEndpointPayload(pairingId, pairingId, 2, canonical),
                DirectEndpointPayload(clientId, clientId, 2, canonical),
            ).forEach { invalid -> assertThrows(DirectException::class.java) {
                    DirectBundles.endpoint(signedEndpoint(authority.first, invalid), updated, now)
                } }
        }
    }
    @Test fun renewalCannotAcknowledgeAnUnseenEndpointGeneration() {
        val record = DirectRecord(clientId, "name", "https://client.example", "key", "csr", "ca",
            pairingId = pairingId, generation = 1)
        val error = assertThrows(DirectException::class.java) {
            verifyDirectIssued(record, DirectIssued("cert", "ca", "AB", "agent", clientId, pairingId, 2))
        }
        assertEquals("identity_mismatch", error.code)
    }
    private fun invitation(ca: String = testCaPem()) = buildJsonObject {
        put("version", 2); put("type", "mobile-egress-direct-invitation")
        put("clientId", clientId); put("displayName", "Workload"); put("endpoint", "https://client.example/")
        put("caCertificatePem", ca); put("invitationId", pairingId)
        put("capability", encode(ByteArray(32) { 7 })); put("expiresAt", "2030-01-01T00:00:00Z"); put("role", "agent")
    }.toString()
    private fun authority(): Pair<java.security.KeyPair, String> {
        val keys = KeyPairGenerator.getInstance("EC").apply { initialize(256) }.generateKeyPair()
        val name = X500Principal("CN=test-direct-ca")
        val builder = JcaX509v3CertificateBuilder(name, BigInteger.ONE, Date.from(now.minusSeconds(86400)),
            Date.from(now.plusSeconds(365 * 86400)), name, keys.public)
            .addExtension(Extension.basicConstraints, true, BasicConstraints(true))
            .addExtension(Extension.keyUsage, true, KeyUsage(KeyUsage.keyCertSign or KeyUsage.digitalSignature))
        val cert = JcaX509CertificateConverter().getCertificate(builder.build(JcaContentSignerBuilder("SHA256withECDSA").build(keys.private)))
        return keys to ("-----BEGIN CERTIFICATE-----\n" + Base64.getMimeEncoder(64, "\n".toByteArray()).encodeToString(cert.encoded) + "\n-----END CERTIFICATE-----\n")
    }
    private fun endpointRecord(ca: String, endpoint: String = "https://old.example:8443") =
        DirectRecord(clientId, "Client", endpoint, "key", "csr", ca,
            identity = AgentIdentity(endpoint, "agent", "AB", "key", "cert", ca),
            pairingId = pairingId, generation = 1, stage = DirectStage.Paired)
    private fun signedEndpoint(keys: java.security.KeyPair, value: DirectEndpointPayload): String {
        val payload = DirectBundles.json.encodeToString(value).toByteArray()
        val signature = Signature.getInstance("SHA256withECDSA").run {
            initSign(keys.private); update("MobileEgress-Direct-Endpoint-v2\n".toByteArray()); update(payload); sign()
        }
        return encode(buildJsonObject {
            put("version", 2); put("type", "mobile-egress-direct-endpoint-update")
            put("payload", encode(payload)); put("signature", encode(signature))
        }.toString().toByteArray())
    }
    private val endpointSpellings = listOf(
        "https://client.example:08443" to "https://client.example:8443",
        "https://CLIENT.Example:8443" to "https://client.example:8443",
        "https://client.example:00443" to "https://client.example",
        "https://[2001:0db8:0000:0000:0000:0000:0000:0001]:8443" to "https://[2001:db8::1]:8443",
        "https://[2001:0db8:0:0:0:0:0:1]:443" to "https://[2001:db8::1]",
    )
    private fun encode(value: ByteArray) = Base64.getUrlEncoder().withoutPadding().encodeToString(value)
}
