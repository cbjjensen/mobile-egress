import Foundation
import XCTest
@testable import MobileEgressCore

final class CellularIPRotationTests: XCTestCase {
    func testAvailabilityRequiresEnrollmentRunningAgentCellularAndNoActiveAttempt() {
        let eligible = availability()

        XCTAssertTrue(eligible.isEligible(for: .idle))
        XCTAssertFalse(availability(isEnrolled: false).isEligible(for: .idle))
        XCTAssertFalse(availability(isAgentRunning: false).isEligible(for: .idle))
        XCTAssertFalse(availability(isCellularAvailable: false).isEligible(for: .idle))
        XCTAssertFalse(
            eligible.isEligible(
                for: .preparing(
                    attemptID: 41,
                    originalNetworkToken: "cell-1",
                    holdSeconds: 10,
                    cellularLost: false,
                    returnedNetworkToken: nil
                )
            )
        )
    }

    func testActiveStreamsRequireAnAffirmativeDecisionBeforeRotationStarts() {
        var reducer = CellularIPRotationReducer()
        let requested = reducer.reduce(
            .requested(
                attemptID: 41,
                networkToken: "cell-1",
                availability: availability(activeStreamCount: 3)
            )
        )

        XCTAssertEqual(
            requested.state,
            .awaitingConfirmation(
                attemptID: 41,
                originalNetworkToken: "cell-1",
                holdSeconds: 10,
                activeStreamCount: 3
            )
        )
        XCTAssertTrue(requested.effects.isEmpty)

        let declined = reducer.reduce(.confirmationDecided(attemptID: 41, proceed: false))
        XCTAssertEqual(declined, CellularIPRotationTransition(state: .idle))

        _ = reducer.reduce(
            .requested(
                attemptID: 42,
                networkToken: "cell-2",
                availability: availability(activeStreamCount: 3)
            )
        )
        let approved = reducer.reduce(.confirmationDecided(attemptID: 42, proceed: true))
        XCTAssertEqual(
            approved.state,
            .preparing(
                attemptID: 42,
                originalNetworkToken: "cell-2",
                holdSeconds: 10,
                cellularLost: false,
                returnedNetworkToken: nil
            )
        )
        XCTAssertEqual(
            approved.effects,
            [.pauseAgentAndStreams(attemptID: 42), .probeBefore(attemptID: 42, networkToken: "cell-2")]
        )
    }

    func testNormalRequestUsesTenSecondHoldAndTwoMinuteLossTimeout() {
        var reducer = CellularIPRotationReducer()

        let requested = reducer.reduce(
            .requested(attemptID: 41, networkToken: "cell-1", availability: availability())
        )
        XCTAssertEqual(
            requested.effects,
            [.pauseAgentAndStreams(attemptID: 41), .probeBefore(attemptID: 41, networkToken: "cell-1")]
        )

        let probed = reducer.reduce(
            .beforeProbeCompleted(
                attemptID: 41,
                snapshot: PublicIPSnapshot(ipv4: "198.51.100.10", ipv6: nil)
            )
        )
        XCTAssertEqual(
            probed.effects,
            [
                .presentAirplaneModeGuidance(attemptID: 41),
                .scheduleCellularLossTimeout(attemptID: 41, seconds: 120),
            ]
        )

        let lost = reducer.reduce(.cellularLost(attemptID: 41))
        XCTAssertEqual(
            lost.effects,
            [
                .cancelCellularLossTimeout(attemptID: 41),
                .startHoldCountdown(attemptID: 41, seconds: 10),
            ]
        )
    }

    func testUnchangedResultMakesTheNextAttemptAThirtySecondRetry() {
        let before = PublicIPSnapshot(ipv4: "198.51.100.10", ipv6: nil)
        var reducer = CellularIPRotationReducer(
            initialState: .completed(
                attemptID: 40,
                before: before,
                after: before,
                result: .unchanged
            )
        )

        let transition = reducer.reduce(
            .requested(attemptID: 41, networkToken: "cell-1", availability: availability())
        )

        XCTAssertEqual(
            transition.state,
            .preparing(
                attemptID: 41,
                originalNetworkToken: "cell-1",
                holdSeconds: 30,
                cellularLost: false,
                returnedNetworkToken: nil
            )
        )
    }

    func testEarlyCellularReturnWaitsForHoldBeforeVerification() {
        var reducer = awaitingAirplaneModeReducer()
        _ = reducer.reduce(.cellularLost(attemptID: 41))

        let returned = reducer.reduce(
            .cellularAvailable(attemptID: 41, networkToken: "cell-2")
        )
        XCTAssertEqual(
            returned.state,
            .holding(
                attemptID: 41,
                remainingSeconds: 10,
                before: beforeSnapshot,
                returnedNetworkToken: "cell-2"
            )
        )
        XCTAssertTrue(returned.effects.isEmpty)

        let finished = reducer.reduce(.holdCountdownFinished(attemptID: 41))
        XCTAssertEqual(
            finished.state,
            .verifying(attemptID: 41, before: beforeSnapshot, returnedNetworkToken: "cell-2")
        )
        XCTAssertEqual(
            finished.effects,
            [.probeAfter(attemptID: 41, networkToken: "cell-2")]
        )
    }

    func testCellularReturnAfterHoldUsesThreeMinuteTimeoutAndResumesAfterComparison() {
        var reducer = awaitingAirplaneModeReducer()
        _ = reducer.reduce(.cellularLost(attemptID: 41))

        let waiting = reducer.reduce(.holdCountdownFinished(attemptID: 41))
        XCTAssertEqual(waiting.state, .awaitingCellularReturn(attemptID: 41, before: beforeSnapshot))
        XCTAssertEqual(
            waiting.effects,
            [.scheduleCellularReturnTimeout(attemptID: 41, seconds: 180)]
        )

        let returned = reducer.reduce(
            .cellularAvailable(attemptID: 41, networkToken: "cell-2")
        )
        XCTAssertEqual(
            returned.effects,
            [
                .cancelCellularReturnTimeout(attemptID: 41),
                .probeAfter(attemptID: 41, networkToken: "cell-2"),
            ]
        )

        let after = PublicIPSnapshot(ipv4: "198.51.100.11", ipv6: nil)
        let completed = reducer.reduce(.afterProbeCompleted(attemptID: 41, snapshot: after))
        XCTAssertEqual(
            completed.state,
            .completed(attemptID: 41, before: beforeSnapshot, after: after, result: .changed)
        )
        XCTAssertEqual(completed.effects, [.resumeAgent(attemptID: 41)])
    }

    func testComparisonUsesOnlyFamiliesPresentBeforeAndAfter() {
        XCTAssertEqual(
            CellularIPRotationResult.compare(
                before: PublicIPSnapshot(ipv4: "198.51.100.10", ipv6: "2001:db8::1"),
                after: PublicIPSnapshot(ipv4: "198.51.100.10", ipv6: nil)
            ),
            .unchanged
        )
        XCTAssertEqual(
            CellularIPRotationResult.compare(
                before: PublicIPSnapshot(ipv4: "198.51.100.10", ipv6: "2001:db8::1"),
                after: PublicIPSnapshot(ipv4: "198.51.100.10", ipv6: "2001:db8::2")
            ),
            .changed
        )
        XCTAssertEqual(
            CellularIPRotationResult.compare(
                before: PublicIPSnapshot(ipv4: "198.51.100.10", ipv6: nil),
                after: PublicIPSnapshot(ipv4: nil, ipv6: "2001:db8::1")
            ),
            .unverified
        )
    }

    func testStaleAndDuplicateEventsDoNotAdvanceTheCurrentAttempt() {
        var reducer = awaitingAirplaneModeReducer()

        let stale = reducer.reduce(.cellularLost(attemptID: 40))
        XCTAssertEqual(stale, CellularIPRotationTransition(state: reducer.state))

        let accepted = reducer.reduce(.cellularLost(attemptID: 41))
        let duplicate = reducer.reduce(.cellularLost(attemptID: 41))
        XCTAssertEqual(duplicate, CellularIPRotationTransition(state: accepted.state))

        let duplicateProbe = reducer.reduce(
            .beforeProbeCompleted(attemptID: 41, snapshot: beforeSnapshot)
        )
        XCTAssertEqual(duplicateProbe, CellularIPRotationTransition(state: accepted.state))
    }

    func testAttemptIDsRemainMonotonicAfterTerminalReset() {
        var reducer = awaitingAirplaneModeReducer()
        _ = reducer.reduce(.lossTimedOut(attemptID: 41))
        _ = reducer.reduce(.reset)

        let stale = reducer.reduce(
            .requested(attemptID: 40, networkToken: "stale-cell", availability: availability())
        )
        let duplicate = reducer.reduce(
            .requested(attemptID: 41, networkToken: "duplicate-cell", availability: availability())
        )
        let newer = reducer.reduce(
            .requested(attemptID: 42, networkToken: "cell-2", availability: availability())
        )

        XCTAssertEqual(stale, CellularIPRotationTransition(state: .idle))
        XCTAssertEqual(duplicate, CellularIPRotationTransition(state: .idle))
        XCTAssertEqual(
            newer.state,
            .preparing(
                attemptID: 42,
                originalNetworkToken: "cell-2",
                holdSeconds: 10,
                cellularLost: false,
                returnedNetworkToken: nil
            )
        )
    }

    func testDeclinedConfirmationStillRejectsDelayedAndDuplicateRequests() {
        var reducer = CellularIPRotationReducer()
        _ = reducer.reduce(
            .requested(
                attemptID: 50,
                networkToken: "cell-1",
                availability: availability(activeStreamCount: 2)
            )
        )
        _ = reducer.reduce(.confirmationDecided(attemptID: 50, proceed: false))

        XCTAssertEqual(
            reducer.reduce(
                .requested(
                    attemptID: 49,
                    networkToken: "stale-cell",
                    availability: availability(activeStreamCount: 2)
                )
            ),
            CellularIPRotationTransition(state: .idle)
        )
        XCTAssertEqual(
            reducer.reduce(
                .requested(
                    attemptID: 50,
                    networkToken: "duplicate-cell",
                    availability: availability(activeStreamCount: 2)
                )
            ),
            CellularIPRotationTransition(state: .idle)
        )
        XCTAssertEqual(
            reducer.reduce(
                .requested(
                    attemptID: 51,
                    networkToken: "cell-2",
                    availability: availability(activeStreamCount: 2)
                )
            ).state,
            .awaitingConfirmation(
                attemptID: 51,
                originalNetworkToken: "cell-2",
                holdSeconds: 10,
                activeStreamCount: 2
            )
        )
    }

    func testRequestObservedDuringActiveAttemptCannotReplayAfterReset() {
        var reducer = awaitingAirplaneModeReducer()

        XCTAssertEqual(
            reducer.reduce(
                .requested(attemptID: 42, networkToken: "cell-2", availability: availability())
            ),
            CellularIPRotationTransition(state: reducer.state)
        )
        _ = reducer.reduce(.lossTimedOut(attemptID: 41))
        _ = reducer.reduce(.reset)

        XCTAssertEqual(
            reducer.reduce(
                .requested(attemptID: 42, networkToken: "cell-2", availability: availability())
            ),
            CellularIPRotationTransition(state: .idle)
        )
        XCTAssertEqual(
            reducer.reduce(
                .requested(attemptID: 43, networkToken: "cell-3", availability: availability())
            ).state,
            .preparing(
                attemptID: 43,
                originalNetworkToken: "cell-3",
                holdSeconds: 10,
                cellularLost: false,
                returnedNetworkToken: nil
            )
        )
    }

    func testCancellationAndTimeoutsAttemptBestEffortAgentResume() {
        var lossTimeout = awaitingAirplaneModeReducer()
        XCTAssertEqual(
            lossTimeout.reduce(.lossTimedOut(attemptID: 41)),
            CellularIPRotationTransition(
                state: .failed(attemptID: 41, failure: .cellularDidNotDisconnect),
                effects: [.resumeAgent(attemptID: 41)]
            )
        )

        var returnTimeout = awaitingAirplaneModeReducer()
        _ = returnTimeout.reduce(.cellularLost(attemptID: 41))
        _ = returnTimeout.reduce(.holdCountdownFinished(attemptID: 41))
        XCTAssertEqual(
            returnTimeout.reduce(.returnTimedOut(attemptID: 41)),
            CellularIPRotationTransition(
                state: .failed(attemptID: 41, failure: .cellularDidNotReturn),
                effects: [.resumeAgent(attemptID: 41)]
            )
        )

        var cancelled = awaitingAirplaneModeReducer()
        XCTAssertEqual(
            cancelled.reduce(.cancelled(attemptID: 41)),
            CellularIPRotationTransition(
                state: .failed(attemptID: 41, failure: .cancelled),
                effects: [
                    .cancelCellularLossTimeout(attemptID: 41),
                    .resumeAgent(attemptID: 41),
                ]
            )
        )
    }

    func testTunnelResumeFailureIsFiniteTerminalAndCannotRestartTheAttempt() {
        let completed = CellularIPRotationState.completed(
            attemptID: 41,
            before: beforeSnapshot,
            after: PublicIPSnapshot(ipv4: "198.51.100.11", ipv6: nil),
            result: .changed
        )
        var reducer = CellularIPRotationReducer(initialState: completed)

        let failed = reducer.reduce(.resumeFailed(attemptID: 41))
        XCTAssertEqual(failed.state, .failed(attemptID: 41, failure: .tunnelResumeFailed))
        XCTAssertTrue(failed.effects.isEmpty)
        XCTAssertEqual(
            reducer.reduce(.resumeFailed(attemptID: 41)),
            CellularIPRotationTransition(state: failed.state)
        )
        XCTAssertEqual(
            reducer.reduce(.beforeProbeCompleted(attemptID: 41, snapshot: beforeSnapshot)),
            CellularIPRotationTransition(state: failed.state)
        )
    }

    func testCheckpointIsCodableExpiresAfterFiveMinutesAndRecoversActiveWork() throws {
        let savedAt = Date(timeIntervalSince1970: 2_000_000_000)
        let state = CellularIPRotationState.holding(
            attemptID: 41,
            remainingSeconds: 7,
            before: beforeSnapshot,
            returnedNetworkToken: nil
        )
        let checkpoint = CellularIPRotationCheckpoint(state: state, savedAt: savedAt)
        let encoded = try JSONEncoder().encode(checkpoint)
        let decoded = try JSONDecoder().decode(CellularIPRotationCheckpoint.self, from: encoded)

        XCTAssertEqual(decoded, checkpoint)
        XCTAssertEqual(checkpoint.expiresAt, savedAt.addingTimeInterval(300))
        XCTAssertFalse(checkpoint.isExpired(at: savedAt.addingTimeInterval(299)))
        XCTAssertTrue(checkpoint.isExpired(at: savedAt.addingTimeInterval(300)))

        var reducer = CellularIPRotationReducer()
        XCTAssertEqual(
            reducer.reduce(.recover(checkpoint: checkpoint, at: savedAt.addingTimeInterval(30))),
            CellularIPRotationTransition(
                state: .awaitingCellularReturn(attemptID: 41, before: beforeSnapshot),
                effects: [
                    .pauseAgentAndStreams(attemptID: 41),
                    .scheduleCellularReturnTimeout(attemptID: 41, seconds: 157),
                ]
            )
        )
    }

    func testExpiredCheckpointFailsFiniteAndAttemptsAgentResume() {
        let savedAt = Date(timeIntervalSince1970: 2_000_000_000)
        let checkpoint = CellularIPRotationCheckpoint(
            state: .awaitingCellularReturn(attemptID: 41, before: beforeSnapshot),
            savedAt: savedAt
        )
        var reducer = CellularIPRotationReducer()

        XCTAssertEqual(
            reducer.reduce(.recover(checkpoint: checkpoint, at: savedAt.addingTimeInterval(300))),
            CellularIPRotationTransition(
                state: .failed(attemptID: 41, failure: .recoveryExpired),
                effects: [.resumeAgent(attemptID: 41)]
            )
        )
    }

    func testLossTimeoutRecoveryUsesOnlyTheRemainingTwoMinuteWindow() {
        let savedAt = Date(timeIntervalSince1970: 2_000_000_000)
        let checkpoint = CellularIPRotationCheckpoint(
            state: .awaitingAirplaneMode(
                attemptID: 41,
                originalNetworkToken: "cell-1",
                holdSeconds: 10,
                before: beforeSnapshot
            ),
            savedAt: savedAt,
            timeoutDeadline: savedAt.addingTimeInterval(120)
        )
        var recovering = CellularIPRotationReducer()

        XCTAssertEqual(
            recovering.reduce(.recover(checkpoint: checkpoint, at: savedAt.addingTimeInterval(45))),
            CellularIPRotationTransition(
                state: checkpoint.state,
                effects: [
                    .pauseAgentAndStreams(attemptID: 41),
                    .presentAirplaneModeGuidance(attemptID: 41),
                    .scheduleCellularLossTimeout(attemptID: 41, seconds: 75),
                ]
            )
        )

        var timedOut = CellularIPRotationReducer()
        XCTAssertEqual(
            timedOut.reduce(.recover(checkpoint: checkpoint, at: savedAt.addingTimeInterval(120))),
            CellularIPRotationTransition(
                state: .failed(attemptID: 41, failure: .cellularDidNotDisconnect),
                effects: [.resumeAgent(attemptID: 41)]
            )
        )
    }

    func testLateLossCheckpointPreservesTheOriginalPhaseDeadline() {
        let phaseStartedAt = Date(timeIntervalSince1970: 2_000_000_000)
        let checkpoint = CellularIPRotationCheckpoint(
            state: .awaitingAirplaneMode(
                attemptID: 41,
                originalNetworkToken: "cell-1",
                holdSeconds: 10,
                before: beforeSnapshot
            ),
            savedAt: phaseStartedAt.addingTimeInterval(90),
            timeoutDeadline: phaseStartedAt.addingTimeInterval(120)
        )
        var recovering = CellularIPRotationReducer()

        XCTAssertEqual(
            recovering.reduce(
                .recover(checkpoint: checkpoint, at: phaseStartedAt.addingTimeInterval(110))
            ).effects,
            [
                .pauseAgentAndStreams(attemptID: 41),
                .presentAirplaneModeGuidance(attemptID: 41),
                .scheduleCellularLossTimeout(attemptID: 41, seconds: 10),
            ]
        )

        var elapsed = CellularIPRotationReducer()
        XCTAssertEqual(
            elapsed.reduce(
                .recover(checkpoint: checkpoint, at: phaseStartedAt.addingTimeInterval(120))
            ),
            CellularIPRotationTransition(
                state: .failed(attemptID: 41, failure: .cellularDidNotDisconnect),
                effects: [.resumeAgent(attemptID: 41)]
            )
        )
    }

    func testReturnTimeoutRecoveryUsesOnlyTheRemainingThreeMinuteWindow() {
        let savedAt = Date(timeIntervalSince1970: 2_000_000_000)
        let checkpoint = CellularIPRotationCheckpoint(
            state: .awaitingCellularReturn(attemptID: 41, before: beforeSnapshot),
            savedAt: savedAt,
            timeoutDeadline: savedAt.addingTimeInterval(180)
        )
        var recovering = CellularIPRotationReducer()

        XCTAssertEqual(
            recovering.reduce(.recover(checkpoint: checkpoint, at: savedAt.addingTimeInterval(65))),
            CellularIPRotationTransition(
                state: checkpoint.state,
                effects: [
                    .pauseAgentAndStreams(attemptID: 41),
                    .scheduleCellularReturnTimeout(attemptID: 41, seconds: 115),
                ]
            )
        )

        var timedOut = CellularIPRotationReducer()
        XCTAssertEqual(
            timedOut.reduce(.recover(checkpoint: checkpoint, at: savedAt.addingTimeInterval(180))),
            CellularIPRotationTransition(
                state: .failed(attemptID: 41, failure: .cellularDidNotReturn),
                effects: [.resumeAgent(attemptID: 41)]
            )
        )
    }

    func testLateReturnCheckpointPreservesTheOriginalPhaseDeadline() {
        let phaseStartedAt = Date(timeIntervalSince1970: 2_000_000_000)
        let checkpoint = CellularIPRotationCheckpoint(
            state: .awaitingCellularReturn(attemptID: 41, before: beforeSnapshot),
            savedAt: phaseStartedAt.addingTimeInterval(150),
            timeoutDeadline: phaseStartedAt.addingTimeInterval(180)
        )
        var recovering = CellularIPRotationReducer()

        XCTAssertEqual(
            recovering.reduce(
                .recover(checkpoint: checkpoint, at: phaseStartedAt.addingTimeInterval(175))
            ).effects,
            [
                .pauseAgentAndStreams(attemptID: 41),
                .scheduleCellularReturnTimeout(attemptID: 41, seconds: 5),
            ]
        )

        var elapsed = CellularIPRotationReducer()
        XCTAssertEqual(
            elapsed.reduce(
                .recover(checkpoint: checkpoint, at: phaseStartedAt.addingTimeInterval(180))
            ),
            CellularIPRotationTransition(
                state: .failed(attemptID: 41, failure: .cellularDidNotReturn),
                effects: [.resumeAgent(attemptID: 41)]
            )
        )
    }

    func testCheckpointCodablePreservesDeadlineAndDecodesLegacySchema() throws {
        let savedAt = Date(timeIntervalSince1970: 2_000_000_000)
        let checkpoint = CellularIPRotationCheckpoint(
            state: .awaitingCellularReturn(attemptID: 41, before: beforeSnapshot),
            savedAt: savedAt,
            timeoutDeadline: savedAt.addingTimeInterval(80)
        )
        let encoder = JSONEncoder()
        let decoder = JSONDecoder()

        XCTAssertEqual(
            try decoder.decode(
                CellularIPRotationCheckpoint.self,
                from: encoder.encode(checkpoint)
            ),
            checkpoint
        )

        var legacyObject = try XCTUnwrap(
            JSONSerialization.jsonObject(with: encoder.encode(checkpoint)) as? [String: Any]
        )
        legacyObject.removeValue(forKey: "timeoutDeadline")
        let legacyCheckpoint = try decoder.decode(
            CellularIPRotationCheckpoint.self,
            from: JSONSerialization.data(withJSONObject: legacyObject)
        )

        XCTAssertNil(legacyCheckpoint.timeoutDeadline)
        var reducer = CellularIPRotationReducer()
        XCTAssertEqual(
            reducer.reduce(
                .recover(checkpoint: legacyCheckpoint, at: savedAt.addingTimeInterval(65))
            ),
            CellularIPRotationTransition(
                state: .failed(attemptID: 41, failure: .cellularDidNotReturn),
                effects: [.resumeAgent(attemptID: 41)]
            )
        )
    }

    func testUntimedAwaitingCheckpointFailsSafeInsteadOfResettingPhaseWindow() {
        let savedAt = Date(timeIntervalSince1970: 2_000_000_000)
        let checkpoint = CellularIPRotationCheckpoint(
            state: .awaitingCellularReturn(attemptID: 41, before: beforeSnapshot),
            savedAt: savedAt
        )

        XCTAssertNil(checkpoint.timeoutDeadline)
        var reducer = CellularIPRotationReducer()
        XCTAssertEqual(
            reducer.reduce(
                .recover(checkpoint: checkpoint, at: savedAt.addingTimeInterval(1))
            ),
            CellularIPRotationTransition(
                state: .failed(attemptID: 41, failure: .cellularDidNotReturn),
                effects: [.resumeAgent(attemptID: 41)]
            )
        )
    }

    func testHoldRecoveryPreservesTenSecondWindowAndAppliesReturnOverflow() {
        let savedAt = Date(timeIntervalSince1970: 2_000_000_000)
        let checkpoint = CellularIPRotationCheckpoint(
            state: .holding(
                attemptID: 41,
                remainingSeconds: 10,
                before: beforeSnapshot,
                returnedNetworkToken: nil
            ),
            savedAt: savedAt
        )
        var recoveringHold = CellularIPRotationReducer()

        XCTAssertEqual(
            recoveringHold.reduce(
                .recover(checkpoint: checkpoint, at: savedAt.addingTimeInterval(4))
            ),
            CellularIPRotationTransition(
                state: .holding(
                    attemptID: 41,
                    remainingSeconds: 6,
                    before: beforeSnapshot,
                    returnedNetworkToken: nil
                ),
                effects: [
                    .pauseAgentAndStreams(attemptID: 41),
                    .startHoldCountdown(attemptID: 41, seconds: 6),
                ]
            )
        )

        var recoveringReturn = CellularIPRotationReducer()
        XCTAssertEqual(
            recoveringReturn.reduce(
                .recover(checkpoint: checkpoint, at: savedAt.addingTimeInterval(12))
            ),
            CellularIPRotationTransition(
                state: .awaitingCellularReturn(attemptID: 41, before: beforeSnapshot),
                effects: [
                    .pauseAgentAndStreams(attemptID: 41),
                    .scheduleCellularReturnTimeout(attemptID: 41, seconds: 178),
                ]
            )
        )
    }

    func testRetryHoldRecoveryPreservesRemainingThirtySecondWindow() {
        let savedAt = Date(timeIntervalSince1970: 2_000_000_000)
        let checkpoint = CellularIPRotationCheckpoint(
            state: .holding(
                attemptID: 41,
                remainingSeconds: 30,
                before: beforeSnapshot,
                returnedNetworkToken: "cell-2"
            ),
            savedAt: savedAt
        )
        var reducer = CellularIPRotationReducer()

        XCTAssertEqual(
            reducer.reduce(.recover(checkpoint: checkpoint, at: savedAt.addingTimeInterval(12))),
            CellularIPRotationTransition(
                state: .holding(
                    attemptID: 41,
                    remainingSeconds: 18,
                    before: beforeSnapshot,
                    returnedNetworkToken: "cell-2"
                ),
                effects: [
                    .pauseAgentAndStreams(attemptID: 41),
                    .startHoldCountdown(attemptID: 41, seconds: 18),
                ]
            )
        )

        var completedHold = CellularIPRotationReducer()
        XCTAssertEqual(
            completedHold.reduce(
                .recover(checkpoint: checkpoint, at: savedAt.addingTimeInterval(30))
            ),
            CellularIPRotationTransition(
                state: .verifying(
                    attemptID: 41,
                    before: beforeSnapshot,
                    returnedNetworkToken: "cell-2"
                ),
                effects: [
                    .pauseAgentAndStreams(attemptID: 41),
                    .probeAfter(attemptID: 41, networkToken: "cell-2"),
                ]
            )
        )
    }

    private var beforeSnapshot: PublicIPSnapshot {
        PublicIPSnapshot(ipv4: "198.51.100.10", ipv6: nil)
    }

    private func availability(
        isEnrolled: Bool = true,
        isAgentRunning: Bool = true,
        isCellularAvailable: Bool = true,
        activeStreamCount: Int = 0
    ) -> CellularIPRotationAvailability {
        CellularIPRotationAvailability(
            isEnrolled: isEnrolled,
            isAgentRunning: isAgentRunning,
            isCellularAvailable: isCellularAvailable,
            activeStreamCount: activeStreamCount
        )
    }

    private func awaitingAirplaneModeReducer() -> CellularIPRotationReducer {
        var reducer = CellularIPRotationReducer()
        _ = reducer.reduce(
            .requested(attemptID: 41, networkToken: "cell-1", availability: availability())
        )
        _ = reducer.reduce(.beforeProbeCompleted(attemptID: 41, snapshot: beforeSnapshot))
        return reducer
    }
}
