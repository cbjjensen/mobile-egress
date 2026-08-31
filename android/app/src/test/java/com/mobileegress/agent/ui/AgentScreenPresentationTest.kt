package com.mobileegress.agent.ui

import com.mobileegress.agent.status.AgentRuntimeStatus
import com.mobileegress.agent.status.CellularHealth
import com.mobileegress.agent.status.ErrorClass
import com.mobileegress.agent.status.RelayHealth
import com.mobileegress.agent.network.PublicIpSnapshot
import com.mobileegress.agent.network.RotationResult
import com.mobileegress.agent.network.RotationState
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class AgentScreenPresentationTest {
    @Test
    fun `screen header uses ZFNF mobile branding`() {
        val presentation = presentAgentScreen(MainUiState())

        assertEquals("ZF", presentation.appMark)
        assertEquals("ZFNF MOBILE EGRESS", presentation.appTitle)
        assertEquals("Cellular Agent", presentation.appSubtitle)
    }

    @Test
    fun `unpaired phone makes enrollment the next action`() {
        val presentation = presentAgentScreen(MainUiState())

        assertEquals("Ready to pair", presentation.headline)
        assertEquals("Scan QR", presentation.scanLabel)
        assertTrue(presentation.scanEnabled)
        assertEquals(AgentPrimaryAction.None, presentation.agentPrimaryAction)
    }

    @Test
    fun `paired idle phone makes start the next action`() {
        val presentation = presentAgentScreen(
            MainUiState(
                paired = true,
                pairingStatus = "Paired",
            ),
        )

        assertEquals("Ready to connect", presentation.headline)
        assertEquals(AgentPrimaryAction.Start, presentation.agentPrimaryAction)
        assertTrue(presentation.scanEnabled)
    }

    @Test
    fun `connected running phone presents an active relay and stop action`() {
        val presentation = presentAgentScreen(
            MainUiState(
                paired = true,
                pairingStatus = "Paired",
                runtime = AgentRuntimeStatus(
                    running = true,
                    cellular = CellularHealth.Available,
                    relay = RelayHealth.Connected,
                ),
            ),
        )

        assertEquals("Cellular relay active", presentation.headline)
        assertEquals(ScreenTone.Success, presentation.tone)
        assertEquals(AgentPrimaryAction.Stop, presentation.agentPrimaryAction)
        assertEquals(RotationAction.Rotate, presentation.rotationAction)
        assertEquals("Rotate cellular IP", presentation.rotationLabel)
        assertTrue(presentation.rotationEnabled)
        assertFalse(presentation.scanEnabled)
    }

    @Test
    fun `active rotation replaces the normal headline and disables another attempt`() {
        val presentation = presentAgentScreen(
            MainUiState(
                paired = true,
                pairingStatus = "Paired",
                runtime = AgentRuntimeStatus(
                    running = true,
                    cellular = CellularHealth.Available,
                    relay = RelayHealth.Disconnected,
                    rotation = RotationState.AwaitingAirplaneOn(
                        attemptId = 9,
                        originalNetworkToken = "private-token",
                        holdSeconds = 10,
                        before = PublicIpSnapshot(ipv4 = "198.51.100.40"),
                    ),
                ),
            ),
        )

        assertEquals("Turn Airplane Mode on", presentation.headline)
        assertEquals("Waiting for Airplane Mode", presentation.rotationLabel)
        assertFalse(presentation.rotationEnabled)
    }

    @Test
    fun `unchanged result offers a thirty second retry`() {
        val presentation = presentAgentScreen(
            MainUiState(
                paired = true,
                pairingStatus = "Paired",
                runtime = AgentRuntimeStatus(
                    running = true,
                    cellular = CellularHealth.Available,
                    relay = RelayHealth.Connected,
                    rotation = RotationState.Completed(
                        attemptId = 9,
                        before = PublicIpSnapshot(ipv4 = "198.51.100.40"),
                        after = PublicIpSnapshot(ipv4 = "198.51.100.40"),
                        result = RotationResult.Unchanged,
                    ),
                ),
            ),
        )

        assertEquals("Carrier reused the IP", presentation.headline)
        assertEquals(RotationAction.Retry, presentation.rotationAction)
        assertEquals("Retry with 30-second reset", presentation.rotationLabel)
        assertTrue(presentation.rotationEnabled)
    }

    @Test
    fun `completed rotation exposes transient before and after address rows`() {
        val rows = rotationAddressRows(
            RotationState.Completed(
                attemptId = 9,
                before = PublicIpSnapshot(ipv4 = "198.51.100.40", ipv6 = "2001:db8::40"),
                after = PublicIpSnapshot(ipv4 = "198.51.100.41"),
                result = RotationResult.Changed,
            ),
        )

        assertEquals(
            listOf(
                RotationAddressRow("IPv4 before", "198.51.100.40"),
                RotationAddressRow("IPv4 after", "198.51.100.41"),
                RotationAddressRow("IPv6 before", "2001:db8::40"),
                RotationAddressRow("IPv6 after", "Not verified"),
            ),
            rows,
        )
        assertTrue(rotationAddressRows(RotationState.Idle).isEmpty())
    }

    @Test
    fun `target scoped error does not replace connected relay headline`() {
        val presentation = presentAgentScreen(
            MainUiState(
                paired = true,
                pairingStatus = "Paired",
                runtime = AgentRuntimeStatus(
                    running = true,
                    cellular = CellularHealth.Available,
                    relay = RelayHealth.Connected,
                    errorClass = ErrorClass.TargetPolicy,
                ),
            ),
        )

        assertEquals("Cellular relay active", presentation.headline)
        assertEquals(ScreenTone.Success, presentation.tone)
    }

    @Test
    fun `cellular loss remains the headline when a stream error is retained`() {
        val presentation = presentAgentScreen(
            MainUiState(
                paired = true,
                pairingStatus = "Paired",
                runtime = AgentRuntimeStatus(
                    running = true,
                    cellular = CellularHealth.Unavailable,
                    relay = RelayHealth.Disconnected,
                    errorClass = ErrorClass.TargetConnect,
                ),
            ),
        )

        assertEquals("Waiting for cellular", presentation.headline)
        assertEquals(ScreenTone.Warning, presentation.tone)
    }

    @Test
    fun `blocking relay error asks for attention while disconnected`() {
        val presentation = presentAgentScreen(
            MainUiState(
                paired = true,
                pairingStatus = "Paired",
                runtime = AgentRuntimeStatus(
                    running = true,
                    cellular = CellularHealth.Available,
                    relay = RelayHealth.Disconnected,
                    errorClass = ErrorClass.RelayTls,
                ),
            ),
        )

        assertEquals("Connection needs attention", presentation.headline)
        assertEquals(ScreenTone.Error, presentation.tone)
    }

    @Test
    fun `paired migration shows update guidance instead of unpaired guidance`() {
        val presentation = presentAgentScreen(
            MainUiState(
                pairingInProgress = true,
                paired = true,
                pairingStatus = "Pairing",
                pairingScanState = PairingScanState.Pairing,
            ),
        )

        assertEquals(AgentPrimaryAction.None, presentation.agentPrimaryAction)
        assertEquals("Finish the endpoint update before starting the Agent.", presentation.inactiveAgentMessage)
    }

    @Test
    fun `scanner failure receives error pairing tone`() {
        val presentation = presentAgentScreen(
            MainUiState(
                pairingStatus = "QR not recognized",
                pairingScanState = PairingScanState.QrNotRecognized,
            ),
        )

        assertEquals(ScreenTone.Error, presentation.pairingTone)
    }

    @Test
    fun `byte totals use compact human readable units`() {
        assertEquals("0 B", formatByteCount(-1))
        assertEquals("0 B", formatByteCount(0))
        assertEquals("768 B", formatByteCount(768))
        assertEquals("1023 B", formatByteCount(1023))
        assertEquals("1.0 KB", formatByteCount(1024))
        assertEquals("1.5 KB", formatByteCount(1536))
        assertEquals("2.0 MB", formatByteCount(2L * 1024 * 1024))
        assertEquals("1.0 GB", formatByteCount(1024L * 1024 * 1024))
    }
}
