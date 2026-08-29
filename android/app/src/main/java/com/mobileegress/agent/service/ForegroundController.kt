package com.mobileegress.agent.service

enum class ForegroundState { Stopped, Starting, Running, Stopping }

sealed interface ForegroundEvent {
    data object ServiceCreated : ForegroundEvent
    data object UiStartRequested : ForegroundEvent
    data object RuntimeStarted : ForegroundEvent
    data object UiStopRequested : ForegroundEvent
    data object RuntimeStopped : ForegroundEvent
}

sealed interface ForegroundEffect {
    data object CreateNotificationChannel : ForegroundEffect
    data object EnterForeground : ForegroundEffect
    data object StartCellularRuntime : ForegroundEffect
    data object StopCellularRuntime : ForegroundEffect
    data object ExitForegroundAndStopService : ForegroundEffect
}

data class ForegroundTransition(
    val state: ForegroundState,
    val effects: List<ForegroundEffect> = emptyList(),
)

class ForegroundController {
    var state: ForegroundState = ForegroundState.Stopped
        private set

    @Synchronized
    fun reduce(event: ForegroundEvent): ForegroundTransition {
        val transition = when (event) {
            ForegroundEvent.ServiceCreated -> ForegroundTransition(state)
            ForegroundEvent.UiStartRequested -> if (state == ForegroundState.Stopped) {
                ForegroundTransition(
                    ForegroundState.Starting,
                    listOf(
                        ForegroundEffect.CreateNotificationChannel,
                        ForegroundEffect.EnterForeground,
                        ForegroundEffect.StartCellularRuntime,
                    ),
                )
            } else ForegroundTransition(state)
            ForegroundEvent.RuntimeStarted -> if (state == ForegroundState.Starting) {
                ForegroundTransition(ForegroundState.Running)
            } else ForegroundTransition(state)
            ForegroundEvent.UiStopRequested -> if (
                state == ForegroundState.Starting || state == ForegroundState.Running
            ) {
                ForegroundTransition(ForegroundState.Stopping, listOf(ForegroundEffect.StopCellularRuntime))
            } else ForegroundTransition(state)
            ForegroundEvent.RuntimeStopped -> if (state == ForegroundState.Stopping) {
                ForegroundTransition(
                    ForegroundState.Stopped,
                    listOf(ForegroundEffect.ExitForegroundAndStopService),
                )
            } else ForegroundTransition(state)
        }
        state = transition.state
        return transition
    }
}
