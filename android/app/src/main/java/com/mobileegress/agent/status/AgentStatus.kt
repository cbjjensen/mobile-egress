package com.mobileegress.agent.status

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update

enum class CellularHealth { Unavailable, Available }
enum class RelayHealth { Disconnected, Connecting, Connected }
enum class ErrorClass {
    None,
    CellularUnavailable,
    RelayTls,
    RelayAuth,
    RelayUnavailable,
    Protocol,
    TargetPolicy,
    TargetConnect,
    Backpressure,
    Credential,
    Internal,
}

data class AgentRuntimeStatus(
    val running: Boolean = false,
    val cellular: CellularHealth = CellularHealth.Unavailable,
    val relay: RelayHealth = RelayHealth.Disconnected,
    val activeStreams: Int = 0,
    val bytesUp: Long = 0,
    val bytesDown: Long = 0,
    val errorClass: ErrorClass = ErrorClass.None,
) {
    fun copySafeText(paired: Boolean): String = listOf(
        "Mobile Egress Agent",
        "Paired: ${if (paired) "yes" else "no"}",
        "Running: ${if (running) "yes" else "no"}",
        "Cellular: ${cellular.name.lowercase()}",
        "Relay: ${relay.name.lowercase()}",
        "Active streams: $activeStreams",
        "Bytes up: $bytesUp",
        "Bytes down: $bytesDown",
        "Error class: ${errorClass.name.lowercase()}",
    ).joinToString("\n")
}

object AgentStatusBus {
    private val mutable = MutableStateFlow(AgentRuntimeStatus())
    val status: StateFlow<AgentRuntimeStatus> = mutable.asStateFlow()

    fun update(transform: (AgentRuntimeStatus) -> AgentRuntimeStatus) = mutable.update(transform)

    fun reset() {
        mutable.value = AgentRuntimeStatus()
    }
}
