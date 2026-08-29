package com.mobileegress.agent.network

enum class NetworkTransport { CELLULAR, WIFI }

sealed interface PathState {
    data object Stopped : PathState
    data object AwaitingCellular : PathState
    data class ConnectingRelay(val networkToken: String) : PathState
    data class Online(val networkToken: String) : PathState
}

sealed interface PathEvent {
    data object StartRequested : PathEvent
    data object StopRequested : PathEvent
    data class NetworkAvailable(val token: String, val transport: NetworkTransport) : PathEvent
    data class NetworkLost(val token: String) : PathEvent
    data class RelayConnected(val token: String) : PathEvent
    data class RelayDisconnected(val token: String) : PathEvent
}

sealed interface PathEffect {
    data class ConnectRelay(val networkToken: String) : PathEffect
    data object CloseSessionAndStreams : PathEffect
}

data class PathTransition(val state: PathState, val effects: List<PathEffect> = emptyList())

class CellularRequiredController {
    var state: PathState = PathState.Stopped
        private set

    @Synchronized
    fun reduce(event: PathEvent): PathTransition {
        val transition = when (event) {
            PathEvent.StartRequested -> when (state) {
                PathState.Stopped -> PathTransition(PathState.AwaitingCellular)
                else -> PathTransition(state)
            }
            PathEvent.StopRequested -> when (state) {
                PathState.Stopped -> PathTransition(state)
                else -> PathTransition(PathState.Stopped, listOf(PathEffect.CloseSessionAndStreams))
            }
            is PathEvent.NetworkAvailable -> when {
                state != PathState.AwaitingCellular -> PathTransition(state)
                event.transport != NetworkTransport.CELLULAR -> PathTransition(state)
                else -> PathTransition(
                    PathState.ConnectingRelay(event.token),
                    listOf(PathEffect.ConnectRelay(event.token)),
                )
            }
            is PathEvent.NetworkLost -> when (val current = state) {
                is PathState.ConnectingRelay -> if (current.networkToken == event.token) {
                    PathTransition(PathState.AwaitingCellular, listOf(PathEffect.CloseSessionAndStreams))
                } else PathTransition(state)
                is PathState.Online -> if (current.networkToken == event.token) {
                    PathTransition(PathState.AwaitingCellular, listOf(PathEffect.CloseSessionAndStreams))
                } else PathTransition(state)
                else -> PathTransition(state)
            }
            is PathEvent.RelayConnected -> when (val current = state) {
                is PathState.ConnectingRelay -> if (current.networkToken == event.token) {
                    PathTransition(PathState.Online(event.token))
                } else PathTransition(state)
                else -> PathTransition(state)
            }
            is PathEvent.RelayDisconnected -> when (val current = state) {
                is PathState.ConnectingRelay -> if (current.networkToken == event.token) {
                    PathTransition(PathState.ConnectingRelay(event.token))
                } else PathTransition(state)
                is PathState.Online -> if (current.networkToken == event.token) {
                    PathTransition(PathState.ConnectingRelay(event.token), listOf(PathEffect.CloseSessionAndStreams))
                } else PathTransition(state)
                else -> PathTransition(state)
            }
        }
        state = transition.state
        return transition
    }
}
