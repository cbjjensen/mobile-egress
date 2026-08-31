package com.mobileegress.agent.network

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class CellularIpRotationControllerTest {
    @Test
    fun `rotation request closes streams and captures the cellular address first`() {
        val controller = CellularIpRotationController()

        val transition = controller.reduce(
            RotationEvent.Requested(attemptId = 41, networkToken = "cell-1", holdSeconds = 10),
        )

        assertEquals(RotationState.Preparing(41, "cell-1", 10), transition.state)
        assertEquals(
            listOf(
                RotationEffect.CloseSessionAndStreams,
                RotationEffect.ProbeBefore("cell-1"),
            ),
            transition.effects,
        )
    }

    @Test
    fun `a second request is ignored while rotation is active`() {
        val controller = CellularIpRotationController()
        controller.reduce(RotationEvent.Requested(41, "cell-1", 10))

        val transition = controller.reduce(RotationEvent.Requested(42, "cell-1", 30))

        assertEquals(RotationState.Preparing(41, "cell-1", 10), transition.state)
        assertTrue(transition.effects.isEmpty())
    }

    @Test
    fun `captured address opens settings and cellular loss starts the hold countdown`() {
        val controller = preparedController()
        val before = PublicIpSnapshot(ipv4 = "198.51.100.10")

        val ready = controller.reduce(RotationEvent.BeforeProbeCompleted(before))
        val lost = controller.reduce(RotationEvent.CellularLost)

        assertEquals(RotationState.AwaitingAirplaneOn(41, "cell-1", 10, before), ready.state)
        assertEquals(
            listOf(RotationEffect.OpenAirplaneSettings(41), RotationEffect.ScheduleLossTimeout),
            ready.effects,
        )
        assertEquals(RotationState.Detaching(41, 10, before, null), lost.state)
        assertEquals(
            listOf(RotationEffect.CancelLossTimeout, RotationEffect.StartHoldCountdown(10)),
            lost.effects,
        )
    }

    @Test
    fun `cellular loss during the initial probe is retained until the probe completes`() {
        val controller = preparedController()

        val lost = controller.reduce(RotationEvent.CellularLost)
        val captured = controller.reduce(
            RotationEvent.BeforeProbeCompleted(PublicIpSnapshot(ipv4 = "198.51.100.10")),
        )

        assertEquals(RotationState.Preparing(41, "cell-1", 10, cellularLost = true), lost.state)
        assertEquals(
            RotationState.Detaching(41, 10, PublicIpSnapshot(ipv4 = "198.51.100.10"), null),
            captured.state,
        )
        assertEquals(listOf(RotationEffect.StartHoldCountdown(10)), captured.effects)
    }

    @Test
    fun `cellular return during the initial probe is retained for later verification`() {
        val controller = preparedController()
        controller.reduce(RotationEvent.CellularLost)

        val returned = controller.reduce(RotationEvent.CellularAvailable("cell-2"))
        val captured = controller.reduce(
            RotationEvent.BeforeProbeCompleted(PublicIpSnapshot(ipv4 = "198.51.100.10")),
        )

        assertEquals(RotationState.Preparing(41, "cell-1", 10, true, "cell-2"), returned.state)
        assertEquals(
            RotationState.Detaching(41, 10, PublicIpSnapshot(ipv4 = "198.51.100.10"), "cell-2"),
            captured.state,
        )
    }

    @Test
    fun `early cellular return waits for the hold countdown before verification`() {
        val controller = awaitingLossController()
        controller.reduce(RotationEvent.CellularLost)

        val earlyReturn = controller.reduce(RotationEvent.CellularAvailable("cell-2"))
        val countdownFinished = controller.reduce(RotationEvent.HoldCountdownFinished)

        assertEquals(
            RotationState.Detaching(41, 10, PublicIpSnapshot(ipv4 = "198.51.100.10"), "cell-2"),
            earlyReturn.state,
        )
        assertTrue(earlyReturn.effects.isEmpty())
        assertEquals(
            RotationState.Verifying(41, PublicIpSnapshot(ipv4 = "198.51.100.10"), "cell-2"),
            countdownFinished.state,
        )
        assertEquals(listOf(RotationEffect.ProbeAfter("cell-2")), countdownFinished.effects)
    }

    @Test
    fun `countdown ticks update the visible remaining hold time`() {
        val controller = awaitingLossController()
        controller.reduce(RotationEvent.CellularLost)

        val transition = controller.reduce(RotationEvent.HoldCountdownTick(7))

        assertEquals(
            RotationState.Detaching(41, 7, PublicIpSnapshot(ipv4 = "198.51.100.10"), null),
            transition.state,
        )
        assertTrue(transition.effects.isEmpty())
    }

    @Test
    fun `cellular return after the hold verifies and reconnects with a changed result`() {
        val controller = awaitingLossController()
        controller.reduce(RotationEvent.CellularLost)
        val waiting = controller.reduce(RotationEvent.HoldCountdownFinished)
        val available = controller.reduce(RotationEvent.CellularAvailable("cell-2"))
        val completed = controller.reduce(
            RotationEvent.AfterProbeCompleted(PublicIpSnapshot(ipv4 = "198.51.100.11")),
        )

        assertEquals(
            RotationState.AwaitingCellularReturn(41, PublicIpSnapshot(ipv4 = "198.51.100.10")),
            waiting.state,
        )
        assertEquals(listOf(RotationEffect.ScheduleReturnTimeout), waiting.effects)
        assertEquals(
            listOf(RotationEffect.CancelReturnTimeout, RotationEffect.ProbeAfter("cell-2")),
            available.effects,
        )
        assertEquals(
            RotationState.Completed(
                attemptId = 41,
                before = PublicIpSnapshot(ipv4 = "198.51.100.10"),
                after = PublicIpSnapshot(ipv4 = "198.51.100.11"),
                result = RotationResult.Changed,
            ),
            completed.state,
        )
        assertEquals(listOf(RotationEffect.ResumeRelay("cell-2")), completed.effects)
    }

    @Test
    fun `only address families measured both times determine the result`() {
        assertEquals(
            RotationResult.Unchanged,
            comparePublicIps(
                PublicIpSnapshot(ipv4 = "198.51.100.10", ipv6 = "2001:db8::1"),
                PublicIpSnapshot(ipv4 = "198.51.100.10"),
            ),
        )
        assertEquals(
            RotationResult.Changed,
            comparePublicIps(
                PublicIpSnapshot(ipv4 = "198.51.100.10", ipv6 = "2001:db8::1"),
                PublicIpSnapshot(ipv4 = "198.51.100.10", ipv6 = "2001:db8::2"),
            ),
        )
        assertEquals(
            RotationResult.Unverified,
            comparePublicIps(
                PublicIpSnapshot(ipv4 = "198.51.100.10"),
                PublicIpSnapshot(ipv6 = "2001:db8::1"),
            ),
        )
    }

    @Test
    fun `loss timeout resumes the original cellular relay`() {
        val controller = awaitingLossController()

        val transition = controller.reduce(RotationEvent.LossTimedOut)

        assertEquals(RotationState.Failed(41, RotationFailure.CellularDidNotDisconnect), transition.state)
        assertEquals(listOf(RotationEffect.ResumeRelay("cell-1")), transition.effects)
    }

    @Test
    fun `return timeout leaves the agent waiting for cellular`() {
        val controller = awaitingLossController()
        controller.reduce(RotationEvent.CellularLost)
        controller.reduce(RotationEvent.HoldCountdownFinished)

        val transition = controller.reduce(RotationEvent.ReturnTimedOut)

        assertEquals(RotationState.Failed(41, RotationFailure.CellularDidNotReturn), transition.state)
        assertTrue(transition.effects.isEmpty())
    }

    @Test
    fun `cancelling before cellular loss resumes the original relay`() {
        val controller = awaitingLossController()

        val transition = controller.reduce(RotationEvent.Cancelled)

        assertEquals(RotationState.Failed(41, RotationFailure.Cancelled), transition.state)
        assertEquals(
            listOf(RotationEffect.CancelLossTimeout, RotationEffect.ResumeRelay("cell-1")),
            transition.effects,
        )
    }

    @Test
    fun `runtime reset clears completed or failed rotation state`() {
        val controller = awaitingLossController()
        controller.reduce(RotationEvent.LossTimedOut)

        val transition = controller.reduce(RotationEvent.Reset)

        assertEquals(RotationState.Idle, transition.state)
        assertTrue(transition.effects.isEmpty())
    }

    private fun preparedController(): CellularIpRotationController =
        CellularIpRotationController().apply {
            reduce(RotationEvent.Requested(41, "cell-1", 10))
        }

    private fun awaitingLossController(): CellularIpRotationController =
        preparedController().apply {
            reduce(RotationEvent.BeforeProbeCompleted(PublicIpSnapshot(ipv4 = "198.51.100.10")))
        }
}
