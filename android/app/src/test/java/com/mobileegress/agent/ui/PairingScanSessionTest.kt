package com.mobileegress.agent.ui

import com.mobileegress.agent.pairing.PairingBundleParser
import com.mobileegress.agent.pairing.testCaPem
import java.time.Instant
import java.util.Base64
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertTrue
import org.junit.Test

class PairingScanSessionTest {
    @Test
    fun `camera permission is requested only when scanning starts`() {
        val session = PairingScanSession()

        assertEquals(ScanRequest.RequestCameraPermission, session.requestScan(cameraPermissionGranted = false))
        assertEquals(PairingScanState.AwaitingCameraPermission, session.state)

        assertEquals(ScanRequest.StartScanner, session.onCameraPermissionResult(granted = true))
        assertEquals(PairingScanState.Scanning, session.state)
    }

    @Test
    fun `permission denial reports a generic camera requirement`() {
        val session = PairingScanSession()

        session.requestScan(cameraPermissionGranted = false)
        assertEquals(ScanRequest.None, session.onCameraPermissionResult(granted = false))
        assertEquals(PairingScanState.CameraPermissionRequired, session.state)
        assertEquals("Camera permission required", session.status)
    }

    @Test
    fun `only the first decoded code is accepted per scan session`() {
        val session = PairingScanSession()
        val invitation = "secret-agent-invitation"

        session.requestScan(cameraPermissionGranted = true)

        assertTrue(session.acceptDecoded(invitation))
        assertFalse(session.acceptDecoded("another-secret"))
        assertEquals(PairingScanState.Pairing, session.state)
    }

    @Test
    fun `cancelling a scan returns to the pairing screen`() {
        val session = PairingScanSession()

        session.requestScan(cameraPermissionGranted = true)
        session.cancel()

        assertEquals(PairingScanState.Idle, session.state)
        assertEquals("Unpaired", session.status)
    }

    @Test
    fun `empty QR payload is rejected without consuming the scan session`() {
        val session = PairingScanSession()

        session.requestScan(cameraPermissionGranted = true)

        assertFalse(session.acceptDecoded(""))
        assertEquals(PairingScanState.QrNotRecognized, session.state)
        assertEquals("QR not recognized", session.status)
    }

    @Test
    fun `invalid and expired scanned bundles have the same generic rejection`() {
        val parser = PairingBundleParser { Instant.parse("2026-08-29T18:00:00Z") }
        val expiredBundle = buildJsonObject {
            put("version", 1)
            put("relayUrl", "https://relay.example:8443")
            put("caCertificatePem", testCaPem())
            put("capability", "one-use-high-entropy-capability")
            put("role", "agent")
            put("expiresAt", "2026-08-29T17:59:59Z")
        }.toString().encodeBase64Url()

        listOf("not-a-pairing-bundle", expiredBundle).forEach { scannedValue ->
            val error = runCatching { parser.parse(scannedValue) }.exceptionOrNull()

            assertNotNull(error)
            assertEquals("Pairing bundle rejected", PairingFailureStatus.forError(requireNotNull(error)))
        }
    }

    private fun String.encodeBase64Url(): String =
        Base64.getUrlEncoder().withoutPadding().encodeToString(toByteArray(Charsets.UTF_8))
}
