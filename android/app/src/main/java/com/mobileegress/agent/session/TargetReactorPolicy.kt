package com.mobileegress.agent.session

import com.mobileegress.agent.status.ErrorClass

internal data class TargetTerminalAction(
    val code: String?,
    val errorClass: ErrorClass,
    val drainOutboundData: Boolean = false,
    val rejectUnopenedStream: Boolean = false,
)

internal fun TargetTerminalReason.protocolAction(): TargetTerminalAction = when (this) {
    TargetTerminalReason.TargetClosed -> TargetTerminalAction(
        code = "target_closed",
        errorClass = ErrorClass.None,
        drainOutboundData = true,
    )
    TargetTerminalReason.TargetFailure -> TargetTerminalAction(
        code = "target_failure",
        errorClass = ErrorClass.TargetConnect,
    )
    TargetTerminalReason.OpenSetupFailure -> TargetTerminalAction(
        code = "target_failure",
        errorClass = ErrorClass.TargetConnect,
        rejectUnopenedStream = true,
    )
    TargetTerminalReason.IdleTimeout -> TargetTerminalAction(
        code = "idle_timeout",
        errorClass = ErrorClass.TargetConnect,
    )
    TargetTerminalReason.Backpressure -> TargetTerminalAction(
        code = "agent_unavailable",
        errorClass = ErrorClass.Backpressure,
    )
    TargetTerminalReason.Canceled,
    TargetTerminalReason.Shutdown -> TargetTerminalAction(
        code = null,
        errorClass = ErrorClass.None,
    )
}
