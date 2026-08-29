package com.mobileegress.agent.service

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class ForegroundControllerTest {
    @Test
    fun `service creation alone cannot start the agent`() {
        val controller = ForegroundController()

        val transition = controller.reduce(ForegroundEvent.ServiceCreated)

        assertEquals(ForegroundState.Stopped, transition.state)
        assertTrue(transition.effects.isEmpty())
    }

    @Test
    fun `explicit UI start enters foreground before starting runtime`() {
        val controller = ForegroundController()

        val transition = controller.reduce(ForegroundEvent.UiStartRequested)

        assertEquals(ForegroundState.Starting, transition.state)
        assertEquals(
            listOf(
                ForegroundEffect.CreateNotificationChannel,
                ForegroundEffect.EnterForeground,
                ForegroundEffect.StartCellularRuntime,
            ),
            transition.effects,
        )
        assertEquals(ForegroundState.Running, controller.reduce(ForegroundEvent.RuntimeStarted).state)
    }

    @Test
    fun `stop tears down runtime before foreground notification`() {
        val controller = ForegroundController()
        controller.reduce(ForegroundEvent.UiStartRequested)
        controller.reduce(ForegroundEvent.RuntimeStarted)

        val stopping = controller.reduce(ForegroundEvent.UiStopRequested)
        assertEquals(ForegroundState.Stopping, stopping.state)
        assertEquals(listOf(ForegroundEffect.StopCellularRuntime), stopping.effects)

        val stopped = controller.reduce(ForegroundEvent.RuntimeStopped)
        assertEquals(ForegroundState.Stopped, stopped.state)
        assertEquals(listOf(ForegroundEffect.ExitForegroundAndStopService), stopped.effects)
    }
}
