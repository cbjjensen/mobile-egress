package com.mobileegress.agent.ui

import com.mobileegress.agent.status.AgentRuntimeStatus
import com.mobileegress.agent.status.CellularHealth
import com.mobileegress.agent.status.ErrorClass
import com.mobileegress.agent.status.RelayHealth
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class AgentScreenPresentationTest {
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
        assertFalse(presentation.scanEnabled)
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
