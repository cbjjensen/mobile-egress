package com.mobileegress.agent.service

import com.mobileegress.agent.network.PublicIpSnapshot
import com.mobileegress.agent.network.RotationState
import com.mobileegress.agent.status.AgentRuntimeStatus
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class AgentNotificationPresentationTest {
    @Test
    fun `notification presentation coalesces bytes and transient errors with an unchanged summary`() {
        val coalescer = AgentNotificationPresentationCoalescer()
        val displayed = AgentRuntimeStatus(running = true)

        assertTrue(coalescer.shouldNotify(displayed))
        assertFalse(
            coalescer.shouldNotify(
                displayed.copy(
                    bytesDown = 4_096,
                    bytesUp = 2_048,
                    errorClass = com.mobileegress.agent.status.ErrorClass.Internal,
                ),
            ),
        )
    }

    @Test
    fun `notification presentation changes for cellular relay stream and rotation summaries`() {
        val coalescer = AgentNotificationPresentationCoalescer()
        val displayed = AgentRuntimeStatus(running = true)
        assertTrue(coalescer.shouldNotify(displayed))

        assertTrue(coalescer.shouldNotify(displayed.copy(activeStreams = 1)))
        assertTrue(coalescer.shouldNotify(displayed.copy(relay = com.mobileegress.agent.status.RelayHealth.Connected)))
        assertTrue(coalescer.shouldNotify(displayed.copy(cellular = com.mobileegress.agent.status.CellularHealth.Available)))
        assertTrue(
            coalescer.shouldNotify(
                displayed.copy(rotation = RotationState.Preparing(1, "cell-1", 10)),
            ),
        )
    }

    @Test
    fun `rotation notification guides the two manual airplane mode toggles`() {
        assertEquals(
            "Turn Airplane Mode on",
            agentNotificationSummary(
                AgentRuntimeStatus(
                    running = true,
                    rotation = RotationState.AwaitingAirplaneOn(3, "cell-1", 10, PublicIpSnapshot()),
                ),
            ),
        )
        assertEquals(
            "Keep Airplane Mode on for 7 seconds",
            agentNotificationSummary(
                AgentRuntimeStatus(
                    running = true,
                    rotation = RotationState.Detaching(3, 7, PublicIpSnapshot(), null),
                ),
            ),
        )
        assertEquals(
            "Turn Airplane Mode off · Waiting for cellular",
            agentNotificationSummary(
                AgentRuntimeStatus(
                    running = true,
                    rotation = RotationState.AwaitingCellularReturn(3, PublicIpSnapshot()),
                ),
            ),
        )
    }
}
