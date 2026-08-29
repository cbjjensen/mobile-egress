package com.mobileegress.agent.ui

import com.mobileegress.agent.pairing.AgentIdentityPersistence
import com.mobileegress.agent.pairing.EnrollmentCredentialKeys
import com.mobileegress.agent.pairing.EnrollmentPerformer
import com.mobileegress.agent.pairing.EnrollmentRepository
import com.mobileegress.agent.pairing.PairingBundleParser
import com.mobileegress.agent.pairing.testCaPem
import com.mobileegress.agent.security.AgentIdentity
import com.mobileegress.agent.security.DeviceKey
import java.security.KeyPairGenerator
import java.security.spec.ECGenParameterSpec
import java.time.Instant
import java.util.Base64
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.async
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class ScanPairingCoordinatorTest {
    @Test
    fun `decoded scan enters redacted pairing state before direct repository enrollment`() = runTest {
        val enrollmentEntered = CompletableDeferred<Unit>()
        val completeEnrollment = CompletableDeferred<Unit>()
        val identities = FakeIdentityPersistence()
        val repository = EnrollmentRepository(
            decoder = PairingBundleParser { Instant.parse("2026-08-29T18:00:00Z") },
            credentialKeys = FakeCredentialKeys(),
            identityPersistence = identities,
            enrollmentPerformer = EnrollmentPerformer { _, deviceKey, _ ->
                enrollmentEntered.complete(Unit)
                completeEnrollment.await()
                AgentIdentity(
                    relayOrigin = "https://relay.example:8443",
                    role = "agent",
                    serial = "A1",
                    keyAlias = deviceKey.alias,
                    certificatePem = "certificate",
                    caCertificatePem = "ca",
                )
            },
        )
        val coordinator = ScanPairingCoordinator(repository, initiallyPaired = false)
        val scannedBundle = validBundle()

        coordinator.requestScan(cameraPermissionGranted = true)
        assertTrue(coordinator.beginDecoded(scannedBundle))
        assertEquals(
            PairingUiState(
                pairingInProgress = true,
                pairingStatus = "Pairing",
                pairingScanState = PairingScanState.Pairing,
            ),
            coordinator.state,
        )

        val enrollment = async { coordinator.enrollDecoded(scannedBundle) }
        enrollmentEntered.await()
        assertEquals(
            PairingUiState(
                pairingInProgress = true,
                pairingStatus = "Pairing",
                pairingScanState = PairingScanState.Pairing,
            ),
            coordinator.state,
        )

        completeEnrollment.complete(Unit)
        enrollment.await()

        assertEquals("Paired", coordinator.state.pairingStatus)
        assertTrue(coordinator.state.paired)
        assertEquals("A1", identities.current?.serial)
    }

    private fun validBundle(): String = buildJsonObject {
        put("version", 1)
        put("relayUrl", "https://relay.example:8443")
        put("caCertificatePem", testCaPem())
        put("capability", "one-use-high-entropy-capability")
        put("role", "agent")
        put("expiresAt", "2026-08-29T18:10:00Z")
    }.toString().encodeBase64Url()

    private fun String.encodeBase64Url(): String =
        Base64.getUrlEncoder().withoutPadding().encodeToString(toByteArray(Charsets.UTF_8))

    private class FakeIdentityPersistence : AgentIdentityPersistence {
        var current: AgentIdentity? = null

        override fun load(): AgentIdentity? = current

        override fun save(identity: AgentIdentity) {
            current = identity
        }
    }

    private class FakeCredentialKeys : EnrollmentCredentialKeys {
        override fun create(): DeviceKey {
            val keys = KeyPairGenerator.getInstance("EC").apply {
                initialize(ECGenParameterSpec("secp256r1"))
            }.generateKeyPair()
            return DeviceKey("new-alias", keys.private, keys.public)
        }

        override fun createCsrPem(deviceKey: DeviceKey): String = "test-csr"

        override fun delete(alias: String) = Unit
    }
}
