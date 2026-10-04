package com.mobileegress.agent.direct

import com.mobileegress.agent.security.AgentIdentity
import com.mobileegress.agent.security.CredentialStoreException
import com.mobileegress.agent.security.PinnedTls
import java.io.File
import java.security.KeyStoreException
import java.time.Instant
import javax.net.ssl.SSLHandshakeException
import kotlinx.serialization.json.*
import org.junit.Assert.*
import org.junit.Test

class DirectRecoveryTest {
    @Test fun onlyStrictPinnedAuthenticatedDenialRequiresRePairing() {
        assertEquals(DirectRecovery.RePairRequired, directRecovery(directControlFailure(401, "application/json; charset=utf-8", "{\"error\":\"unauthorized\"}", true)))
        val ambiguous = listOf(
            Triple(401, "application/json", "{\"error\":\"unauthorized\",\"extra\":true}"),
            Triple(401, "application/json", "{\"error\":\"unauthorized\",\"error\":\"unauthorized\"}"),
            Triple(401, "text/plain", "{\"error\":\"unauthorized\"}"),
            Triple(401, "application/json", "{\"error\":\"storage_unavailable\"}"),
            Triple(403, "application/json", "{\"error\":\"unauthorized\"}"),
            Triple(503, "application/json", "{\"error\":\"unauthorized\"}"),
            Triple(401, "application/json", "not JSON"),
        )
        ambiguous.forEach { (status, type, body) ->
            val recovery = directRecovery(directControlFailure(status, type, body, true))
            assertEquals(DirectRecovery.RetryConnection, recovery)
            assertTrue(recovery.automaticRetry)
        }
        assertEquals(DirectRecovery.RetryConnection, directRecovery(directControlFailure(401, "application/json", "{\"error\":\"unauthorized\"}", false)))
        assertEquals(DirectRecovery.ClientStorageUnavailable,
            directRecovery(directControlFailure(503, "application/json", "{\"error\":\"storage_unavailable\"}", true)))
    }

    @Test fun missingKeyAndLockedStorageHaveDifferentRecoveryWithoutDeletingTrust() {
        val missing = assertThrows(DirectException::class.java) { directPrivateKey("saved-alias") { null } }
        assertEquals(DirectRecovery.RePairRequired, directRecovery(missing))
        val locked = assertThrows(DirectException::class.java) { directPrivateKey("saved-alias") { throw KeyStoreException("private details") } }
        assertEquals(DirectRecovery.UnlockStorage, directRecovery(locked))
        assertFalse(directRecovery(locked).automaticRetry)
        assertEquals(DirectRecovery.UnlockStorage, directRecovery(CredentialStoreException("private details")))
        val genericTls = directRecovery(SSLHandshakeException("private address or arbitrary remote alert"))
        assertEquals(DirectRecovery.RetryConnection, genericTls)
        assertTrue(genericTls.automaticRetry)
        assertFalse(genericTls.message.contains("private address"))
    }

    @Test fun localLeafAndCaExpiryRequirePairingButFutureValidityRequestsClockRepair() {
        val fixture = generateSequence(File(requireNotNull(System.getProperty("user.dir")))) { it.parentFile }
            .map { File(it, "testdata/direct-v2-wire.json") }.first { it.isFile }.readText()
            .let { DirectBundles.json.parseToJsonElement(it).jsonObject }
        val now = Instant.parse(fixture.getValue("invitationExpiresAt").jsonPrimitive.content).minusSeconds(60)
        val invitation = DirectBundles.invitation(fixture.getValue("invitation").jsonPrimitive.content, now)
        val issued = DirectBundles.json.decodeFromString<DirectIssued>(fixture.getValue("identity").toString())
        val identity = AgentIdentity(invitation.endpoint, "agent", issued.serial, "key", issued.certificatePem, issued.caCertificatePem)
        val record = DirectRecord(invitation.clientId, "Client", invitation.endpoint, "key", "csr", invitation.caCertificatePem, identity = identity)
        val leaf = directLeaf(record, now)
        val ca = directCa(record, now)
        assertEquals(DirectRecovery.RePairRequired, directRecovery(assertThrows(DirectException::class.java) { directLeaf(record, leaf.notAfter.toInstant().plusSeconds(1)) }))
        assertEquals(DirectRecovery.CheckClock, directRecovery(assertThrows(DirectException::class.java) { directLeaf(record, leaf.notBefore.toInstant().minusSeconds(1)) }))
        assertEquals(DirectRecovery.RePairRequired, directRecovery(assertThrows(DirectException::class.java) { directCa(record, ca.notAfter.toInstant().plusSeconds(1)) }))
        assertEquals(DirectRecovery.CheckClock, directRecovery(assertThrows(DirectException::class.java) { directCa(record, ca.notBefore.toInstant().minusSeconds(1)) }))
        assertEquals(DirectRecovery.RePairRequired, directRecovery(assertThrows(DirectException::class.java) { directCa(record.copy(caCertificatePem = "invalid local trust"), now) }))
        assertEquals(issued.serial, PinnedTls.parseCertificateChain(identity.certificatePem).first().serialNumber.toString(16).uppercase())
    }
}
