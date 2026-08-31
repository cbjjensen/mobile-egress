package com.mobileegress.agent.service

import com.mobileegress.agent.network.PublicIpSnapshot
import com.mobileegress.agent.network.RotationState
import com.mobileegress.agent.status.AgentRuntimeStatus
import org.junit.Assert.assertEquals
import org.junit.Test

class AgentNotificationPresentationTest {
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
