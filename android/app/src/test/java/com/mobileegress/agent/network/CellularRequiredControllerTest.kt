package com.mobileegress.agent.network

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class CellularRequiredControllerTest {
    @Test
    fun `wifi availability never starts a relay connection`() {
        val controller = CellularRequiredController()

        controller.reduce(PathEvent.StartRequested)
        val transition = controller.reduce(PathEvent.NetworkAvailable("wifi-1", NetworkTransport.WIFI))

        assertEquals(PathState.AwaitingCellular, transition.state)
        assertTrue(transition.effects.isEmpty())
    }

    @Test
    fun `cellular availability connects relay and cellular loss closes every session`() {
        val controller = CellularRequiredController()

        assertEquals(PathState.AwaitingCellular, controller.reduce(PathEvent.StartRequested).state)
        val available = controller.reduce(PathEvent.NetworkAvailable("cell-1", NetworkTransport.CELLULAR))
        assertEquals(PathState.ConnectingRelay("cell-1"), available.state)
        assertEquals(listOf(PathEffect.ConnectRelay("cell-1")), available.effects)
        assertEquals(PathState.Online("cell-1"), controller.reduce(PathEvent.RelayConnected("cell-1")).state)

        val lost = controller.reduce(PathEvent.NetworkLost("cell-1"))
        assertEquals(PathState.AwaitingCellular, lost.state)
        assertEquals(listOf(PathEffect.CloseSessionAndStreams), lost.effects)
    }

    @Test
    fun `stale network callbacks cannot replace or close the selected cellular path`() {
        val controller = CellularRequiredController()
        controller.reduce(PathEvent.StartRequested)
        controller.reduce(PathEvent.NetworkAvailable("cell-1", NetworkTransport.CELLULAR))
        controller.reduce(PathEvent.RelayConnected("cell-1"))

        assertEquals(PathState.Online("cell-1"), controller.reduce(PathEvent.NetworkAvailable("cell-2", NetworkTransport.CELLULAR)).state)
        assertEquals(PathState.Online("cell-1"), controller.reduce(PathEvent.NetworkLost("cell-2")).state)
    }
}
