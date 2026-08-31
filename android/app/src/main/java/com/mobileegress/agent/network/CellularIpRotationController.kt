package com.mobileegress.agent.network

data class PublicIpSnapshot(
    val ipv4: String? = null,
    val ipv6: String? = null,
)

enum class RotationResult { Changed, Unchanged, Unverified }

enum class RotationFailure { CellularDidNotDisconnect, CellularDidNotReturn, Cancelled }

sealed interface RotationState {
    data object Idle : RotationState
    data class Preparing(
        val attemptId: Long,
        val originalNetworkToken: String,
        val holdSeconds: Int,
        val cellularLost: Boolean = false,
        val returnedNetworkToken: String? = null,
    ) : RotationState
    data class AwaitingAirplaneOn(
        val attemptId: Long,
        val originalNetworkToken: String,
        val holdSeconds: Int,
        val before: PublicIpSnapshot,
    ) : RotationState
    data class Detaching(
        val attemptId: Long,
        val holdSeconds: Int,
        val before: PublicIpSnapshot,
        val returnedNetworkToken: String?,
    ) : RotationState
    data class AwaitingCellularReturn(
        val attemptId: Long,
        val before: PublicIpSnapshot,
    ) : RotationState
    data class Verifying(
        val attemptId: Long,
        val before: PublicIpSnapshot,
        val returnedNetworkToken: String,
    ) : RotationState
    data class Completed(
        val attemptId: Long,
        val before: PublicIpSnapshot,
        val after: PublicIpSnapshot,
        val result: RotationResult,
    ) : RotationState
    data class Failed(val attemptId: Long, val failure: RotationFailure) : RotationState
}

sealed interface RotationEvent {
    data class Requested(
        val attemptId: Long,
        val networkToken: String,
        val holdSeconds: Int,
    ) : RotationEvent
    data class BeforeProbeCompleted(val snapshot: PublicIpSnapshot) : RotationEvent
    data object CellularLost : RotationEvent
    data class CellularAvailable(val networkToken: String) : RotationEvent
    data class HoldCountdownTick(val remainingSeconds: Int) : RotationEvent
    data object HoldCountdownFinished : RotationEvent
    data class AfterProbeCompleted(val snapshot: PublicIpSnapshot) : RotationEvent
    data object LossTimedOut : RotationEvent
    data object ReturnTimedOut : RotationEvent
    data object Cancelled : RotationEvent
    data object Reset : RotationEvent
}

sealed interface RotationEffect {
    data object CloseSessionAndStreams : RotationEffect
    data class ProbeBefore(val networkToken: String) : RotationEffect
    data class OpenAirplaneSettings(val attemptId: Long) : RotationEffect
    data object ScheduleLossTimeout : RotationEffect
    data object CancelLossTimeout : RotationEffect
    data class StartHoldCountdown(val seconds: Int) : RotationEffect
    data object ScheduleReturnTimeout : RotationEffect
    data object CancelReturnTimeout : RotationEffect
    data class ProbeAfter(val networkToken: String) : RotationEffect
    data class ResumeRelay(val networkToken: String) : RotationEffect
}

data class RotationTransition(
    val state: RotationState,
    val effects: List<RotationEffect> = emptyList(),
)

class CellularIpRotationController {
    var state: RotationState = RotationState.Idle
        private set

    @Synchronized
    fun reduce(event: RotationEvent): RotationTransition {
        val transition = when (event) {
            is RotationEvent.Requested -> if (state.isActive()) {
                RotationTransition(state)
            } else {
                RotationTransition(
                    RotationState.Preparing(event.attemptId, event.networkToken, event.holdSeconds),
                    listOf(
                        RotationEffect.CloseSessionAndStreams,
                        RotationEffect.ProbeBefore(event.networkToken),
                    ),
                )
            }
            is RotationEvent.BeforeProbeCompleted -> when (val current = state) {
                is RotationState.Preparing -> if (current.cellularLost) {
                    RotationTransition(
                        RotationState.Detaching(
                            current.attemptId,
                            current.holdSeconds,
                            event.snapshot,
                            current.returnedNetworkToken,
                        ),
                        listOf(RotationEffect.StartHoldCountdown(current.holdSeconds)),
                    )
                } else {
                    RotationTransition(
                        RotationState.AwaitingAirplaneOn(
                            current.attemptId,
                            current.originalNetworkToken,
                            current.holdSeconds,
                            event.snapshot,
                        ),
                        listOf(
                            RotationEffect.OpenAirplaneSettings(current.attemptId),
                            RotationEffect.ScheduleLossTimeout,
                        ),
                    )
                }
                else -> RotationTransition(state)
            }
            RotationEvent.CellularLost -> when (val current = state) {
                is RotationState.Preparing -> RotationTransition(current.copy(cellularLost = true))
                is RotationState.AwaitingAirplaneOn -> RotationTransition(
                    RotationState.Detaching(
                        current.attemptId,
                        current.holdSeconds,
                        current.before,
                        null,
                    ),
                    listOf(
                        RotationEffect.CancelLossTimeout,
                        RotationEffect.StartHoldCountdown(current.holdSeconds),
                    ),
                )
                else -> RotationTransition(state)
            }
            is RotationEvent.CellularAvailable -> when (val current = state) {
                is RotationState.Preparing -> if (current.cellularLost) {
                    RotationTransition(current.copy(returnedNetworkToken = event.networkToken))
                } else {
                    RotationTransition(state)
                }
                is RotationState.Detaching -> RotationTransition(
                    current.copy(returnedNetworkToken = event.networkToken),
                )
                is RotationState.AwaitingCellularReturn -> RotationTransition(
                    RotationState.Verifying(current.attemptId, current.before, event.networkToken),
                    listOf(
                        RotationEffect.CancelReturnTimeout,
                        RotationEffect.ProbeAfter(event.networkToken),
                    ),
                )
                else -> RotationTransition(state)
            }
            is RotationEvent.HoldCountdownTick -> when (val current = state) {
                is RotationState.Detaching -> RotationTransition(
                    current.copy(holdSeconds = event.remainingSeconds.coerceAtLeast(0)),
                )
                else -> RotationTransition(state)
            }
            RotationEvent.HoldCountdownFinished -> when (val current = state) {
                is RotationState.Detaching -> if (current.returnedNetworkToken != null) {
                    RotationTransition(
                        RotationState.Verifying(
                            current.attemptId,
                            current.before,
                            current.returnedNetworkToken,
                        ),
                        listOf(RotationEffect.ProbeAfter(current.returnedNetworkToken)),
                    )
                } else {
                    RotationTransition(
                        RotationState.AwaitingCellularReturn(current.attemptId, current.before),
                        listOf(RotationEffect.ScheduleReturnTimeout),
                    )
                }
                else -> RotationTransition(state)
            }
            is RotationEvent.AfterProbeCompleted -> when (val current = state) {
                is RotationState.Verifying -> RotationTransition(
                    RotationState.Completed(
                        current.attemptId,
                        current.before,
                        event.snapshot,
                        comparePublicIps(current.before, event.snapshot),
                    ),
                    listOf(RotationEffect.ResumeRelay(current.returnedNetworkToken)),
                )
                else -> RotationTransition(state)
            }
            RotationEvent.LossTimedOut -> when (val current = state) {
                is RotationState.AwaitingAirplaneOn -> RotationTransition(
                    RotationState.Failed(current.attemptId, RotationFailure.CellularDidNotDisconnect),
                    listOf(RotationEffect.ResumeRelay(current.originalNetworkToken)),
                )
                else -> RotationTransition(state)
            }
            RotationEvent.ReturnTimedOut -> when (val current = state) {
                is RotationState.AwaitingCellularReturn -> RotationTransition(
                    RotationState.Failed(current.attemptId, RotationFailure.CellularDidNotReturn),
                )
                else -> RotationTransition(state)
            }
            RotationEvent.Cancelled -> cancelTransition()
            RotationEvent.Reset -> RotationTransition(RotationState.Idle)
        }
        state = transition.state
        return transition
    }

    private fun cancelTransition(): RotationTransition = when (val current = state) {
        is RotationState.Preparing -> RotationTransition(
            RotationState.Failed(current.attemptId, RotationFailure.Cancelled),
            listOf(RotationEffect.ResumeRelay(current.originalNetworkToken)),
        )
        is RotationState.AwaitingAirplaneOn -> RotationTransition(
            RotationState.Failed(current.attemptId, RotationFailure.Cancelled),
            listOf(
                RotationEffect.CancelLossTimeout,
                RotationEffect.ResumeRelay(current.originalNetworkToken),
            ),
        )
        is RotationState.Detaching -> RotationTransition(
            RotationState.Failed(current.attemptId, RotationFailure.Cancelled),
            current.returnedNetworkToken?.let { listOf(RotationEffect.ResumeRelay(it)) }.orEmpty(),
        )
        is RotationState.AwaitingCellularReturn -> RotationTransition(
            RotationState.Failed(current.attemptId, RotationFailure.Cancelled),
            listOf(RotationEffect.CancelReturnTimeout),
        )
        is RotationState.Verifying -> RotationTransition(
            RotationState.Failed(current.attemptId, RotationFailure.Cancelled),
            listOf(RotationEffect.ResumeRelay(current.returnedNetworkToken)),
        )
        else -> RotationTransition(state)
    }
}

fun comparePublicIps(before: PublicIpSnapshot, after: PublicIpSnapshot): RotationResult {
    val comparable = listOfNotNull(
        before.ipv4?.let { old -> after.ipv4?.let { new -> old to new } },
        before.ipv6?.let { old -> after.ipv6?.let { new -> old to new } },
    )
    return when {
        comparable.isEmpty() -> RotationResult.Unverified
        comparable.any { (old, new) -> old != new } -> RotationResult.Changed
        else -> RotationResult.Unchanged
    }
}

fun RotationState.isActive(): Boolean = when (this) {
    is RotationState.Preparing,
    is RotationState.AwaitingAirplaneOn,
    is RotationState.Detaching,
    is RotationState.AwaitingCellularReturn,
    is RotationState.Verifying,
    -> true
    RotationState.Idle,
    is RotationState.Completed,
    is RotationState.Failed,
    -> false
}
