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
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class ScanPairingCoordinatorTest {
    @Test
    fun `one submitted scan enters redacted pairing state and enrolls that value`() = runTest {
        val enrollmentEntered = CompletableDeferred<Unit>()
        val completeEnrollment = CompletableDeferred<Unit>()
        val identities = FakeIdentityPersistence()
        val repository = EnrollmentRepository(
            decoder = PairingBundleParser { Instant.parse("2026-08-29T18:00:00Z") },
            credentialKeys = FakeCredentialKeys(),
            identityPersistence = identities,
            enrollmentPerformer = EnrollmentPerformer { bundle, deviceKey, _ ->
                enrollmentEntered.complete(Unit)
                completeEnrollment.await()
                AgentIdentity(
                    relayOrigin = bundle.relayOrigin,
                    role = "agent",
                    serial = "serial-${bundle.capability}",
                    keyAlias = deviceKey.alias,
                    certificatePem = "certificate",
                    caCertificatePem = "ca",
                )
            },
        )
        val coordinator = ScanPairingCoordinator(repository, initiallyPaired = false)
        val scannedBundle = validBundle()

        coordinator.requestScan(cameraPermissionGranted = true)
        assertTrue(coordinator.submitDecoded(scannedBundle))
        assertEquals(
            PairingUiState(
                pairingInProgress = true,
                pairingStatus = "Pairing",
                pairingScanState = PairingScanState.Pairing,
            ),
            coordinator.state,
        )

        val enrollment = async { coordinator.enrollAcceptedScan() }
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
        assertEquals("serial-submitted-scan-capability", identities.current?.serial)
        assertEquals("https://submitted-scan.example:9443", identities.current?.relayOrigin)
    }

    @Test
    fun `wrong-role submitted scan is rejected through the coordinator with generic state`() = runTest {
        val identities = FakeIdentityPersistence()
        val repository = EnrollmentRepository(
            decoder = PairingBundleParser { Instant.parse("2026-08-29T18:00:00Z") },
            credentialKeys = FakeCredentialKeys(),
            identityPersistence = identities,
            enrollmentPerformer = EnrollmentPerformer { _, _, _ -> error("must not enroll a wrong role") },
        )
        val coordinator = ScanPairingCoordinator(repository, initiallyPaired = false)

        coordinator.requestScan(cameraPermissionGranted = true)
        assertTrue(coordinator.submitDecoded(wrongRoleBundle()))
        coordinator.enrollAcceptedScan()

        assertEquals(
            PairingUiState(
                pairingStatus = "Pairing bundle rejected",
                pairingScanState = PairingScanState.Pairing,
            ),
            coordinator.state,
        )
        assertEquals(null, identities.current)
    }

    @Test
    fun `cancelling an accepted scan cannot strand pairing in progress`() = runTest {
        var enrollmentAttempts = 0
        val repository = EnrollmentRepository(
            decoder = PairingBundleParser { Instant.parse("2026-08-29T18:00:00Z") },
            credentialKeys = FakeCredentialKeys(),
            identityPersistence = FakeIdentityPersistence(),
            enrollmentPerformer = EnrollmentPerformer { _, _, _ ->
                enrollmentAttempts++
                error("cancelled handoff must not enroll")
            },
        )
        val coordinator = ScanPairingCoordinator(repository, initiallyPaired = false)

        coordinator.requestScan(cameraPermissionGranted = true)
        assertTrue(coordinator.submitDecoded(validBundle()))
        coordinator.cancelScan()
        coordinator.enrollAcceptedScan()

        assertFalse(coordinator.state.pairingInProgress)
        assertEquals("Unpaired", coordinator.state.pairingStatus)
        assertEquals(PairingScanState.Idle, coordinator.state.pairingScanState)
        assertEquals(0, enrollmentAttempts)
    }

    private fun validBundle(): String = buildJsonObject {
        put("version", 1)
        put("relayUrl", "https://submitted-scan.example:9443")
        put("caCertificatePem", testCaPem())
        put("capability", "submitted-scan-capability")
        put("role", "agent")
        put("expiresAt", "2026-08-29T18:10:00Z")
    }.toString().encodeBase64Url()

    private fun wrongRoleBundle(): String = buildJsonObject {
        put("version", 1)
        put("relayUrl", "https://relay.example:8443")
        put("caCertificatePem", testCaPem())
        put("capability", "one-use-high-entropy-capability")
        put("role", "owner")
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
