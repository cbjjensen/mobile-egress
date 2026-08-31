package com.mobileegress.agent.service

import com.mobileegress.agent.network.RotationState
import com.mobileegress.agent.status.AgentRuntimeStatus

fun agentNotificationSummary(status: AgentRuntimeStatus): String = when (val rotation = status.rotation) {
    is RotationState.Preparing -> "Preparing cellular IP rotation"
    is RotationState.AwaitingAirplaneOn -> "Turn Airplane Mode on"
    is RotationState.Detaching -> "Keep Airplane Mode on for ${rotation.holdSeconds} seconds"
    is RotationState.AwaitingCellularReturn -> "Turn Airplane Mode off · Waiting for cellular"
    is RotationState.Verifying -> "Cellular returned · Checking public IP"
    else -> "Cellular ${status.cellular.name.lowercase()} · Relay ${status.relay.name.lowercase()} · ${status.activeStreams} streams"
}
