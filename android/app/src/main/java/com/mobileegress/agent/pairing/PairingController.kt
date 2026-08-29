package com.mobileegress.agent.pairing

enum class PairingState { Unpaired, Pairing, Paired }

sealed interface PairingEvent {
    data object PairRequested : PairingEvent
    data object PairSucceeded : PairingEvent
    data object PairFailed : PairingEvent
}

class PairingController(initiallyPaired: Boolean) {
    var state: PairingState = if (initiallyPaired) PairingState.Paired else PairingState.Unpaired
        private set
    private var stateBeforeAttempt = state

    @Synchronized
    fun reduce(event: PairingEvent): PairingState {
        state = when (event) {
            PairingEvent.PairRequested -> {
                if (state != PairingState.Pairing) stateBeforeAttempt = state
                PairingState.Pairing
            }
            PairingEvent.PairSucceeded -> if (state == PairingState.Pairing) PairingState.Paired else state
            PairingEvent.PairFailed -> if (state == PairingState.Pairing) stateBeforeAttempt else state
        }
        return state
    }
}
