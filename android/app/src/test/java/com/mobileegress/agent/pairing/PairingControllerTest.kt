package com.mobileegress.agent.pairing

import org.junit.Assert.assertEquals
import org.junit.Test

class PairingControllerTest {
    @Test
    fun `pairing succeeds only after explicit request`() {
        val controller = PairingController(initiallyPaired = false)

        assertEquals(PairingState.Unpaired, controller.state)
        assertEquals(PairingState.Pairing, controller.reduce(PairingEvent.PairRequested))
        assertEquals(PairingState.Paired, controller.reduce(PairingEvent.PairSucceeded))
    }

    @Test
    fun `pairing failure is retryable without losing an existing pairing`() {
        val unpaired = PairingController(initiallyPaired = false)
        unpaired.reduce(PairingEvent.PairRequested)
        assertEquals(PairingState.Unpaired, unpaired.reduce(PairingEvent.PairFailed))
        assertEquals(PairingState.Pairing, unpaired.reduce(PairingEvent.PairRequested))

        val paired = PairingController(initiallyPaired = true)
        paired.reduce(PairingEvent.PairRequested)
        assertEquals(PairingState.Paired, paired.reduce(PairingEvent.PairFailed))
    }
}
