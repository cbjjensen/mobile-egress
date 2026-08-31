package com.mobileegress.agent.ui

import com.mobileegress.agent.status.CellularHealth
import com.mobileegress.agent.status.ErrorClass
import com.mobileegress.agent.status.RelayHealth
import com.mobileegress.agent.network.RotationFailure
import com.mobileegress.agent.network.RotationResult
import com.mobileegress.agent.network.RotationState
import com.mobileegress.agent.network.isActive
import java.util.Locale

enum class ScreenTone {
    Neutral,
    Accent,
    Info,
    Success,
    Warning,
    Error,
}

enum class AgentPrimaryAction {
    None,
    Start,
    Stop,
}

enum class RotationAction { None, Rotate, Retry }

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
    val rotationAction: RotationAction,
    val rotationLabel: String,
    val rotationEnabled: Boolean,
)

data class RotationAddressRow(val label: String, val value: String)

fun rotationAddressRows(rotation: RotationState): List<RotationAddressRow> {
    if (rotation !is RotationState.Completed) return emptyList()
    return listOf(
        RotationAddressRow("IPv4 before", rotation.before.ipv4 ?: "Not verified"),
        RotationAddressRow("IPv4 after", rotation.after.ipv4 ?: "Not verified"),
        RotationAddressRow("IPv6 before", rotation.before.ipv6 ?: "Not verified"),
        RotationAddressRow("IPv6 after", rotation.after.ipv6 ?: "Not verified"),
    )
}

fun presentAgentScreen(state: MainUiState): AgentScreenPresentation {
    val runtime = state.runtime
    val rotationStatus = rotationScreenStatus(runtime.rotation)
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
        rotationStatus != null -> rotationStatus
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
        rotationAction = when {
            !state.paired || !runtime.running -> RotationAction.None
            runtime.rotation is RotationState.Completed &&
                runtime.rotation.result == RotationResult.Unchanged -> RotationAction.Retry
            else -> RotationAction.Rotate
        },
        rotationLabel = rotationLabel(runtime.rotation),
        rotationEnabled = state.paired &&
            runtime.running &&
            runtime.cellular == CellularHealth.Available &&
            !runtime.rotation.isActive(),
    )
}

private fun rotationLabel(rotation: RotationState): String = when (rotation) {
    is RotationState.Preparing -> "Preparing rotation…"
    is RotationState.AwaitingAirplaneOn -> "Waiting for Airplane Mode"
    is RotationState.Detaching -> "Keep Airplane Mode on"
    is RotationState.AwaitingCellularReturn -> "Waiting for cellular"
    is RotationState.Verifying -> "Checking public IP…"
    is RotationState.Completed -> if (rotation.result == RotationResult.Unchanged) {
        "Retry with 30-second reset"
    } else {
        "Rotate cellular IP"
    }
    RotationState.Idle,
    is RotationState.Failed,
    -> "Rotate cellular IP"
}

private fun rotationScreenStatus(rotation: RotationState): ScreenStatus? = when (rotation) {
    RotationState.Idle -> null
    is RotationState.Preparing -> ScreenStatus(
        "Preparing IP rotation",
        "Disconnecting proxy streams and checking the current cellular address.",
        "Preparing",
        ScreenTone.Info,
    )
    is RotationState.AwaitingAirplaneOn -> ScreenStatus(
        "Turn Airplane Mode on",
        "Use Android Settings, keep it on until the notification countdown finishes, then turn it off.",
        "Your action",
        ScreenTone.Warning,
    )
    is RotationState.Detaching -> ScreenStatus(
        "Keep Airplane Mode on",
        "The cellular connection is detached. Wait for the notification before turning Airplane Mode off.",
        "Resetting",
        ScreenTone.Warning,
    )
    is RotationState.AwaitingCellularReturn -> ScreenStatus(
        "Waiting for cellular",
        "Turn Airplane Mode off. The Agent will reconnect and verify the public address automatically.",
        "Reconnecting",
        ScreenTone.Warning,
    )
    is RotationState.Verifying -> ScreenStatus(
        "Checking your new IP",
        "Cellular is back. The Agent is comparing public addresses before restoring proxy traffic.",
        "Verifying",
        ScreenTone.Info,
    )
    is RotationState.Completed -> when (rotation.result) {
        RotationResult.Changed -> ScreenStatus(
            "Cellular IP changed",
            "The cellular relay is ready with a different public address.",
            "Changed",
            ScreenTone.Success,
        )
        RotationResult.Unchanged -> ScreenStatus(
            "Carrier reused the IP",
            "The reset completed, but the carrier returned the same address. A longer retry may help.",
            "Unchanged",
            ScreenTone.Warning,
        )
        RotationResult.Unverified -> ScreenStatus(
            "Cellular reconnected",
            "The Agent restored the relay but could not compare a public address before and after.",
            "Unverified",
            ScreenTone.Warning,
        )
    }
    is RotationState.Failed -> when (rotation.failure) {
        RotationFailure.CellularDidNotDisconnect -> ScreenStatus(
            "IP rotation cancelled",
            "Cellular never disconnected, so the original relay connection was restored.",
            "Not rotated",
            ScreenTone.Warning,
        )
        RotationFailure.CellularDidNotReturn -> ScreenStatus(
            "Cellular did not return",
            "Turn Airplane Mode off and restore cellular data. The Agent will reconnect when it becomes available.",
            "No cellular",
            ScreenTone.Error,
        )
        RotationFailure.Cancelled -> ScreenStatus(
            "IP rotation cancelled",
            "The Agent restored the cellular relay where possible.",
            "Cancelled",
            ScreenTone.Neutral,
        )
    }
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
