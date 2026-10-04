package com.mobileegress.agent.direct

import com.mobileegress.agent.pairing.testCaPem
import java.io.File
import java.security.KeyPairGenerator
import java.time.Instant
import java.util.Base64
import kotlinx.serialization.SerializationException
import kotlinx.serialization.json.*
import org.bouncycastle.asn1.x500.X500Name
import org.bouncycastle.operator.jcajce.JcaContentSignerBuilder
import org.bouncycastle.pkcs.jcajce.JcaPKCS10CertificationRequestBuilder
import org.junit.Assert.*
import org.junit.Test

/** Consumes the shared fixture produced by the actual Go Direct manager. No private keys exist in it. */
class DirectGoInteropTest {
    private val fixture = generateSequence(File(requireNotNull(System.getProperty("user.dir")))) { it.parentFile }
        .map { File(it, "testdata/direct-v2-wire.json") }.first { it.isFile }
        .readText().let { DirectBundles.json.parseToJsonElement(it).jsonObject }
    private fun value(name: String) = fixture.getValue(name).jsonPrimitive.content
    private val now = Instant.parse(value("invitationExpiresAt")).minusSeconds(60)
    private val invitation = DirectBundles.invitation(value("invitation"), now)
    private val issued = DirectBundles.json.decodeFromString<DirectIssued>(DirectBundles.strict(fixture.getValue("identity").toString()))
    private val pending = DirectRecord(invitation.clientId, invitation.displayName, invitation.endpoint,
        "fixture-key", value("csrPem"), invitation.caCertificatePem, invitation = invitation)

    @Test fun goInvitationIdentityAndSkippedEndpointGenerationVerifyWithRealCaAndCsr() {
        assertEquals(value("clientId"), invitation.clientId)
        assertEquals(value("caCertificatePem"), invitation.caCertificatePem)
        val identity = verifyDirectIssued(pending, issued, now)
        assertEquals(issued.serial, identity.serial)
        assertEquals("fixture-key", identity.keyAlias)
        val record = pending.copy(identity = identity, pairingId = issued.pairingId, generation = issued.generation)
        assertEquals(1L, record.generation)
        val update = DirectBundles.endpoint(value("update"), record, now)
        assertEquals(DirectBundles.json.decodeFromString<DirectEndpointPayload>(fixture.getValue("updatePayload").toString()), update)
        assertEquals(3L, update.generation)
        assertEquals(value("pairingId"), update.pairingId)
    }

    @Test fun goArtifactsRejectDuplicateAndUnknownFieldsExpiredInvitationAndWrongIdentityBinding() {
        val invitationRaw = DirectBundles.text(DirectBundles.decode(value("invitation")))
        assertThrows(DirectException::class.java) {
            DirectBundles.invitation(encode(invitationRaw.replace("\"version\":2", "\"version\":2,\"version\":2").toByteArray()), now)
        }
        assertThrows(DirectException::class.java) {
            DirectBundles.invitation(value("invitation"), Instant.parse(value("invitationExpiresAt")))
        }
        val identityRaw = fixture.getValue("identity").toString()
        assertThrows(DirectException::class.java) { DirectBundles.strict(identityRaw.dropLast(1) + ",\"generation\":1}") }
        assertThrows(SerializationException::class.java) {
            DirectBundles.json.decodeFromString<DirectIssued>(DirectBundles.strict(identityRaw.dropLast(1) + ",\"unknown\":true}"))
        }
        val otherKeys = KeyPairGenerator.getInstance("EC").apply { initialize(256) }.generateKeyPair()
        val otherCsr = JcaPKCS10CertificationRequestBuilder(X500Name("CN=another-phone"), otherKeys.public)
            .build(JcaContentSignerBuilder("SHA256withECDSA").build(otherKeys.private))
        val otherCsrPem = "-----BEGIN CERTIFICATE REQUEST-----\n" +
            Base64.getMimeEncoder(64, "\n".toByteArray()).encodeToString(otherCsr.encoded) + "\n-----END CERTIFICATE REQUEST-----\n"
        assertThrows(DirectException::class.java) { verifyDirectIssued(pending.copy(csrPem = otherCsrPem), issued, now) }
        assertThrows(DirectException::class.java) { verifyDirectIssued(pending, issued.copy(caCertificatePem = testCaPem()), now) }
    }
    @Test fun goRetainedSignedSpellingsApplyAndAcknowledgeWithCanonicalPhoneAuthority() {
        val identity = verifyDirectIssued(pending, issued, now)
        fixture.getValue("retainedUpdates").jsonArray.forEach { element ->
            val item = element.jsonObject
            val saved = item.getValue("endpoint").jsonPrimitive.content
            val canonical = item.getValue("canonicalEndpoint").jsonPrimitive.content
            val generation = item.getValue("generation").jsonPrimitive.long
            val bundle = item.getValue("update").jsonPrimitive.content
            for (currentGeneration in listOf(issued.generation, generation)) {
                val before = pending.copy(endpoint = saved, identity = identity.copy(relayOrigin = saved),
                    pairingId = issued.pairingId, generation = currentGeneration, stage = DirectStage.Paired)
                val registry = DirectRegistry(MemoryDirectPersistence(DirectRegistryState(records = listOf(before))))
                val updated = registry.endpoint(before, DirectBundles.endpoint(bundle, before, now))
                assertEquals(canonical, updated.endpoint)
                assertEquals(identity.copy(relayOrigin = canonical), updated.identity)
                assertEquals(generation, updated.generation)
                val acknowledged = registry.acknowledged(updated)
                assertEquals(DirectStage.Paired, acknowledged.stage)
                assertEquals(acknowledged, registry.endpoint(acknowledged, DirectBundles.endpoint(bundle, acknowledged, now)))
            }
        }
    }

    @Test fun goDerSignatureRejectsTamperedPayloadSignatureWrongAuthorityAndPairing() {
        val record = pending.copy(pairingId = issued.pairingId, generation = issued.generation)
        val wrapper = DirectBundles.json.parseToJsonElement(DirectBundles.text(DirectBundles.decode(value("update")))).jsonObject
        fun changed(field: String, bytes: ByteArray) = encode(JsonObject(wrapper + (field to JsonPrimitive(encode(bytes)))).toString().toByteArray())
        val payload = DirectBundles.decode(wrapper.getValue("payload").jsonPrimitive.content)
        val tamperedPayload = DirectBundles.text(payload).replace("third.example", "other.example").toByteArray()
        val signature = DirectBundles.decode(wrapper.getValue("signature").jsonPrimitive.content)
        signature[signature.lastIndex] = (signature.last().toInt() xor 1).toByte()
        for (bundle in listOf(changed("payload", tamperedPayload), changed("signature", signature))) {
            assertEquals("invalid_update_signature", assertThrows(DirectException::class.java) {
                DirectBundles.endpoint(bundle, record, now)
            }.code)
        }
        assertEquals("invalid_update_signature", assertThrows(DirectException::class.java) {
            DirectBundles.endpoint(value("update"), record.copy(caCertificatePem = testCaPem()), now)
        }.code)
        assertEquals("stale_endpoint_update", assertThrows(DirectException::class.java) {
            DirectBundles.endpoint(value("update"), record.copy(pairingId = invitation.invitationId), now)
        }.code)
    }

    private fun encode(bytes: ByteArray) = Base64.getUrlEncoder().withoutPadding().encodeToString(bytes)
}
