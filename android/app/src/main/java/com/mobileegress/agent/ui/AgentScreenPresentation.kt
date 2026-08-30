package com.mobileegress.agent.ui

import com.mobileegress.agent.status.CellularHealth
import com.mobileegress.agent.status.ErrorClass
import com.mobileegress.agent.status.RelayHealth
import java.util.Locale

enum class ScreenTone {
    Neutral,
    Accent,
    Success,
    Warning,
    Error,
}

enum class AgentPrimaryAction {
    None,
    Start,
    Stop,
}

data class AgentScreenPresentation(
    val headline: String,
    val summary: String,
    val badge: String,
    val tone: ScreenTone,
    val pairingTone: ScreenTone,
    val scanLabel: String,
    val scanEnabled: Boolean,
    val agentPrimaryAction: AgentPrimaryAction,
    val inactiveAgentMessage: String,
)

fun presentAgentScreen(state: MainUiState): AgentScreenPresentation {
    val runtime = state.runtime
    val status = when {
        state.pairingInProgress -> ScreenStatus(
            headline = "Pairing phone",
            summary = "Creating a secure cellular identity for this device.",
            badge = "Pairing",
            tone = ScreenTone.Accent,
        )
        !state.paired -> ScreenStatus(
            headline = "Ready to pair",
            summary = "Scan the QR from your Windows controller to link this phone.",
            badge = "Phone setup",
            tone = ScreenTone.Accent,
        )
        !runtime.running -> ScreenStatus(
            headline = "Ready to connect",
            summary = "Your phone is paired. Start the Agent when cellular data is available.",
            badge = "Paired",
            tone = ScreenTone.Success,
        )
        runtime.cellular == CellularHealth.Unavailable -> ScreenStatus(
            headline = "Waiting for cellular",
            summary = "Wi-Fi will not be used as a fallback for proxied traffic.",
            badge = "Connecting",
            tone = ScreenTone.Warning,
        )
        runtime.relay != RelayHealth.Connected && runtime.errorClass in blockingRelayErrors -> ScreenStatus(
            headline = "Connection needs attention",
            summary = "The secure relay session could not connect. Review the Agent details below.",
            badge = "Connection issue",
            tone = ScreenTone.Error,
        )
        runtime.relay != RelayHealth.Connected -> ScreenStatus(
            headline = "Connecting to relay",
            summary = "Cellular is ready while the secure relay session comes online.",
            badge = "Connecting",
            tone = ScreenTone.Warning,
        )
        else -> ScreenStatus(
            headline = "Cellular relay active",
            summary = "Paired workloads can now use this phone's cellular connection.",
            badge = "Connected",
            tone = ScreenTone.Success,
        )
    }

    return AgentScreenPresentation(
        headline = status.headline,
        summary = status.summary,
        badge = status.badge,
        tone = status.tone,
        pairingTone = pairingToneFor(state),
        scanLabel = if (state.pairingInProgress) "Pairing…" else "Scan QR",
        scanEnabled = !state.pairingInProgress && !runtime.running,
        agentPrimaryAction = when {
            state.pairingInProgress || !state.paired -> AgentPrimaryAction.None
            runtime.running -> AgentPrimaryAction.Stop
            else -> AgentPrimaryAction.Start
        },
        inactiveAgentMessage = when {
            state.pairingInProgress && state.paired -> "Finish the endpoint update before starting the Agent."
            state.pairingInProgress -> "Finish secure phone pairing before starting the Agent."
            else -> "Pair this phone before starting the Agent."
        },
    )
}

fun formatByteCount(bytes: Long): String {
    val safeBytes = bytes.coerceAtLeast(0)
    return when {
        safeBytes < 1024 -> "$safeBytes B"
        safeBytes < 1024L * 1024 -> String.format(Locale.US, "%.1f KB", safeBytes / 1024.0)
        safeBytes < 1024L * 1024 * 1024 -> String.format(Locale.US, "%.1f MB", safeBytes / (1024.0 * 1024.0))
        else -> String.format(Locale.US, "%.1f GB", safeBytes / (1024.0 * 1024.0 * 1024.0))
    }
}

private data class ScreenStatus(
    val headline: String,
    val summary: String,
    val badge: String,
    val tone: ScreenTone,
)

private val blockingRelayErrors = setOf(
    ErrorClass.RelayTls,
    ErrorClass.RelayAuth,
    ErrorClass.Credential,
    ErrorClass.Protocol,
    ErrorClass.Internal,
)

private val pairingFailureStatuses = setOf(
    "Pairing bundle rejected",
    "Endpoint migration rejected",
    "Cellular unavailable",
    "Relay enrollment rejected",
    "Credential storage unavailable",
    "Pairing failed",
)

private fun pairingToneFor(state: MainUiState): ScreenTone = when {
    state.pairingInProgress -> ScreenTone.Accent
    state.pairingScanState in setOf(
        PairingScanState.CameraPermissionRequired,
        PairingScanState.ScannerUnavailable,
        PairingScanState.QrNotRecognized,
    ) -> ScreenTone.Error
    state.pairingStatus in pairingFailureStatuses -> ScreenTone.Error
    state.paired -> ScreenTone.Success
    else -> ScreenTone.Neutral
}
