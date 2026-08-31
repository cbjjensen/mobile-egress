package com.mobileegress.agent.status

import com.mobileegress.agent.network.PublicIpSnapshot
import com.mobileegress.agent.network.RotationResult
import com.mobileegress.agent.network.RotationState
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class AgentStatusTest {
    @Test
    fun `copied diagnostics report rotation outcome without public addresses`() {
        val status = AgentRuntimeStatus(
            running = true,
            cellular = CellularHealth.Available,
            relay = RelayHealth.Connected,
            rotation = RotationState.Completed(
                attemptId = 7,
                before = PublicIpSnapshot(ipv4 = "198.51.100.50", ipv6 = "2001:db8::50"),
                after = PublicIpSnapshot(ipv4 = "198.51.100.51", ipv6 = "2001:db8::51"),
                result = RotationResult.Changed,
            ),
        )

        val copied = status.copySafeText(paired = true)

        assertTrue(copied.contains("ZFNF Mobile Egress Agent"))
        assertTrue(copied.contains("IP rotation: changed"))
        assertFalse(copied.contains("198.51.100"))
        assertFalse(copied.contains("2001:db8"))
    }

    @Test
    fun `copied diagnostics omit the private cellular network token`() {
        val copied = AgentRuntimeStatus(
            running = true,
            rotation = RotationState.AwaitingAirplaneOn(
                attemptId = 7,
                originalNetworkToken = "private-cellular-token",
                holdSeconds = 10,
                before = PublicIpSnapshot(ipv4 = "198.51.100.50"),
            ),
        ).copySafeText(paired = true)

        assertTrue(copied.contains("IP rotation: waiting for airplane mode"))
        assertFalse(copied.contains("private-cellular-token"))
        assertFalse(copied.contains("198.51.100.50"))
    }
}
