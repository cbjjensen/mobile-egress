import Foundation
import XCTest
@testable import MobileEgressCore

final class CellularIPRotationCoordinatorTests: XCTestCase {
    func testStartObservesLossAndEarlyReturnPersistsOriginalDeadlineAndCompletes() async {
        let clock = RotationClockStub()
        let sleeper = RotationSleeperStub(immediateUnitSleeps: true)
        let probe = RotationProbeStub(snapshots: [
            PublicIPSnapshot(ipv4: "198.51.100.10"),
            PublicIPSnapshot(ipv4: "198.51.100.11"),
        ])
        let path = RotationPathObserverStub()
        let store = RotationCheckpointStoreStub()
        let cue = RotationNotificationCueStub(scheduleResult: .denied)
        let tunnel = await RotationTunnelStub()
        let coordinator = await CellularIPRotationCoordinator(
            clock: clock,
            sleeper: sleeper,
            probe: probe,
            pathObserver: path,
            checkpointStore: store,
            notificationCue: cue,
            tunnel: tunnel
        )

        await coordinator.resumeAfterActivation()
        path.emit(true)
        await waitUntil { await coordinator.isCellularAvailable }
        await coordinator.updateAgentAvailability(
            isEnrolled: true,
            isAgentRunning: true,
            activeStreamCount: 0
        )
        await coordinator.start(holdSeconds: 10)
        await waitUntil {
            if case .awaitingAirplaneMode = await coordinator.state { return true }
            return false
        }

        XCTAssertEqual(store.lastSaved?.timeoutDeadline, clock.now.addingTimeInterval(120))
        let pauseCount = await tunnel.pauseCount
        XCTAssertEqual(pauseCount, 1)

        path.emit(false)
        path.emit(true)
        await waitUntil {
            if case .completed(_, _, _, .changed) = await coordinator.state { return true }
            return false
        }
        await waitUntil { await tunnel.resumeReceipts.count == 1 }

        let cueSnapshot = await cue.snapshot()
        XCTAssertEqual(cueSnapshot.scheduledDeadlines, [clock.now.addingTimeInterval(10)])
        let resumeReceipts = await tunnel.resumeReceipts
        XCTAssertEqual(resumeReceipts.count, 1)
        XCTAssertNotNil(resumeReceipts.first ?? nil)
        XCTAssertEqual(store.retireCount, 1)
        XCTAssertEqual(cueSnapshot.cancelledAttemptIDs, [1])
    }

    func testDeterminedOutcomeKeepsReceiptCheckpointAndRejectsStartUntilResumeSettles() async {
        let resumeGate = RotationResumeGate()
        let path = RotationPathObserverStub()
        let store = RotationCheckpointStoreStub()
        let before = PublicIPSnapshot(ipv4: "198.51.100.10")
        let after = PublicIPSnapshot(ipv4: "198.51.100.11")
        let tunnel = await RotationTunnelStub(resumeGate: resumeGate)
        let coordinator = await CellularIPRotationCoordinator(
            clock: RotationClockStub(),
            sleeper: RotationSleeperStub(immediateUnitSleeps: true),
            probe: RotationProbeStub(snapshots: [before, after]),
            pathObserver: path,
            checkpointStore: store,
            notificationCue: RotationNotificationCueStub(),
            tunnel: tunnel
        )

        await coordinator.resumeAfterActivation()
        path.emit(true)
        await waitUntil { await coordinator.isCellularAvailable }
        await coordinator.updateAgentAvailability(
            isEnrolled: true,
            isAgentRunning: true,
            activeStreamCount: 0
        )
        await coordinator.start(holdSeconds: 10)
        await waitUntil {
            if case .awaitingAirplaneMode = await coordinator.state { return true }
            return false
        }
        path.emit(false)
        path.emit(true)
        await resumeGate.waitUntilEntered()

        let expectedState = CellularIPRotationState.restoring(
            attemptID: 1,
            outcome: .completed(before: before, after: after, result: .changed)
        )
        let pendingState = await coordinator.state
        XCTAssertEqual(pendingState, expectedState)
        XCTAssertEqual(store.currentCheckpoint?.state, expectedState)
        guard case .paused = store.currentCheckpoint?.pauseDisposition else {
            XCTFail("Pending restoration lost its opaque tunnel receipt")
            await resumeGate.open()
            return
        }
        XCTAssertEqual(store.retireCount, 0)

        let checkpointBeforeSecondStart = store.currentCheckpoint
        let captureCountBeforeSecondStart = await tunnel.captureCount
        let pauseCountBeforeSecondStart = await tunnel.pauseCount
        await coordinator.start(holdSeconds: 10)

        let stateAfterSecondStart = await coordinator.state
        let captureCountAfterSecondStart = await tunnel.captureCount
        let pauseCountAfterSecondStart = await tunnel.pauseCount
        let resumeCountAfterSecondStart = await tunnel.resumeReceipts.count
        XCTAssertEqual(stateAfterSecondStart, expectedState)
        XCTAssertEqual(store.currentCheckpoint, checkpointBeforeSecondStart)
        XCTAssertEqual(captureCountAfterSecondStart, captureCountBeforeSecondStart)
        XCTAssertEqual(pauseCountAfterSecondStart, pauseCountBeforeSecondStart)
        XCTAssertEqual(resumeCountAfterSecondStart, 1)
        XCTAssertEqual(store.retireCount, 0)

        await resumeGate.open()
        await waitUntil {
            if case .completed(1, _, _, .changed) = await coordinator.state { return true }
            return false
        }
        XCTAssertEqual(store.retireCount, 1)
        XCTAssertNil(store.currentCheckpoint)
    }

    func testReconstructedRestorationResumesExactlyOnceBeforeAndDuringResume() async {
        let clock = RotationClockStub()
        let receipt = TunnelRotationReceipt(wasRunning: true, wasOnDemandEnabled: true)
        let restoring = CellularIPRotationState.restoring(
            attemptID: 41,
            outcome: .failed(.cancelled)
        )

        for appliesIntentBeforeGate in [false, true] {
            let resumeGate = RotationResumeGate()
            let path = RotationPathObserverStub()
            let checkpoint = CellularIPRotationCheckpoint(
                state: restoring,
                savedAt: clock.now.addingTimeInterval(-600),
                pauseDisposition: .paused(receipt)
            )
            let store = RotationCheckpointStoreStub(checkpoint: checkpoint)
            let tunnel = await RotationTunnelStub(
                isRunning: false,
                isOnDemandEnabled: false,
                resumeGate: resumeGate,
                appliesIntentBeforeResumeGate: appliesIntentBeforeGate
            )
            let coordinator = await CellularIPRotationCoordinator(
                clock: clock,
                sleeper: RotationSleeperStub(),
                probe: RotationProbeStub(),
                pathObserver: path,
                checkpointStore: store,
                notificationCue: RotationNotificationCueStub(),
                tunnel: tunnel
            )

            await coordinator.resumeAfterActivation()
            await resumeGate.waitUntilEntered()
            path.emit(true)
            await waitUntil { await coordinator.isCellularAvailable }
            await coordinator.updateAgentAvailability(
                isEnrolled: true,
                isAgentRunning: true,
                activeStreamCount: 0
            )

            let pendingState = await coordinator.state
            XCTAssertEqual(pendingState, restoring)
            XCTAssertEqual(store.currentCheckpoint, checkpoint)
            XCTAssertEqual(store.retireCount, 0)
            await coordinator.start(holdSeconds: 10)
            let stateAfterSecondStart = await coordinator.state
            let pendingResumeReceipts = await tunnel.resumeReceipts
            XCTAssertEqual(stateAfterSecondStart, restoring)
            XCTAssertEqual(store.currentCheckpoint, checkpoint)
            XCTAssertEqual(pendingResumeReceipts, [receipt])

            await resumeGate.open()
            await waitUntil {
                if case .failed(41, .cancelled) = await coordinator.state { return true }
                return false
            }
            let restored = await tunnel.intentSnapshot()
            XCTAssertTrue(restored.isRunning)
            XCTAssertTrue(restored.isOnDemandEnabled)
            let completedResumeReceipts = await tunnel.resumeReceipts
            XCTAssertEqual(completedResumeReceipts, [receipt])
            XCTAssertEqual(store.retireCount, 1)
            XCTAssertNil(store.currentCheckpoint)

            await coordinator.resumeAfterActivation()
            let finalResumeReceipts = await tunnel.resumeReceipts
            XCTAssertEqual(finalResumeReceipts, [receipt])
        }
    }

    func testLossTimeoutFailsFiniteAndResumesExactlyOnce() async {
        let sleeper = RotationSleeperStub()
        let path = RotationPathObserverStub()
        let tunnel = await RotationTunnelStub()
        let coordinator = await makeCoordinator(
            sleeper: sleeper,
            path: path,
            tunnel: tunnel,
            probe: RotationProbeStub(snapshots: [PublicIPSnapshot(ipv4: "198.51.100.10")])
        )

        await coordinator.resumeAfterActivation()
        path.emit(true)
        await waitUntil { await coordinator.isCellularAvailable }
        await coordinator.updateAgentAvailability(
            isEnrolled: true,
            isAgentRunning: true,
            activeStreamCount: 0
        )
        await coordinator.start(holdSeconds: 10)
        await waitUntil { await sleeper.pendingSeconds.contains(120) }

        await sleeper.fire(seconds: 120)
        await waitUntil {
            if case .failed(_, .cellularDidNotDisconnect) = await coordinator.state { return true }
            return false
        }
        await waitUntil { await tunnel.resumeReceipts.count == 1 }

        let resumeCount = await tunnel.resumeReceipts.count
        XCTAssertEqual(resumeCount, 1)
    }

    func testDuplicateStartDuringActiveAttemptDoesNotCancelOwnedLossTimeout() async {
        let sleeper = RotationSleeperStub()
        let path = RotationPathObserverStub()
        let tunnel = await RotationTunnelStub()
        let coordinator = await makeCoordinator(
            sleeper: sleeper,
            path: path,
            tunnel: tunnel,
            probe: RotationProbeStub(snapshots: [PublicIPSnapshot(ipv4: "198.51.100.10")])
        )

        await coordinator.resumeAfterActivation()
        path.emit(true)
        await waitUntil { await coordinator.isCellularAvailable }
        await coordinator.updateAgentAvailability(
            isEnrolled: true,
            isAgentRunning: true,
            activeStreamCount: 0
        )
        await coordinator.start(holdSeconds: 10)
        await waitUntil { await sleeper.pendingSeconds.contains(120) }

        await coordinator.start(holdSeconds: 10)
        await sleeper.fire(seconds: 120)
        await waitUntil {
            if case .failed(_, .cellularDidNotDisconnect) = await coordinator.state { return true }
            return false
        }
        await waitUntil { await tunnel.resumeReceipts.count == 1 }

        let pauseCount = await tunnel.pauseCount
        XCTAssertEqual(pauseCount, 1)
    }

    func testReturnTimeoutFailsFiniteAndResumesExactlyOnce() async {
        let sleeper = RotationSleeperStub(immediateUnitSleeps: true)
        let path = RotationPathObserverStub()
        let tunnel = await RotationTunnelStub()
        let coordinator = await makeCoordinator(
            sleeper: sleeper,
            path: path,
            tunnel: tunnel,
            probe: RotationProbeStub(snapshots: [PublicIPSnapshot(ipv4: "198.51.100.10")])
        )

        await coordinator.resumeAfterActivation()
        path.emit(true)
        await waitUntil { await coordinator.isCellularAvailable }
        await coordinator.updateAgentAvailability(
            isEnrolled: true,
            isAgentRunning: true,
            activeStreamCount: 0
        )
        await coordinator.start(holdSeconds: 10)
        await waitUntil {
            if case .awaitingAirplaneMode = await coordinator.state { return true }
            return false
        }

        path.emit(false)
        await waitUntil { await sleeper.pendingSeconds.contains(180) }
        await sleeper.fire(seconds: 180)
        await waitUntil {
            if case .failed(_, .cellularDidNotReturn) = await coordinator.state { return true }
            return false
        }
        await waitUntil { await tunnel.resumeReceipts.count == 1 }

        let resumeCount = await tunnel.resumeReceipts.count
        XCTAssertEqual(resumeCount, 1)
    }

    func testCancelWaitsForEveryAwaitedPauseStageAndNeverAllowsLateStop() async {
        for stage in SuspendedPauseStage.allCases {
            let gate = PauseStageGate()
            let path = RotationPathObserverStub()
            let tunnel = await StageControlledRotationTunnel(stage: stage, gate: gate)
            let coordinator = await CellularIPRotationCoordinator(
                clock: RotationClockStub(),
                sleeper: RotationSleeperStub(),
                probe: RotationProbeStub(snapshots: [
                    PublicIPSnapshot(ipv4: "198.51.100.10"),
                ]),
                pathObserver: path,
                checkpointStore: RotationCheckpointStoreStub(),
                notificationCue: RotationNotificationCueStub(),
                tunnel: tunnel
            )

            await coordinator.resumeAfterActivation()
            path.emit(true)
            await waitUntil { await coordinator.isCellularAvailable }
            await coordinator.updateAgentAvailability(
                isEnrolled: true,
                isAgentRunning: true,
                activeStreamCount: 0
            )
            await coordinator.start(holdSeconds: 10)
            await gate.waitUntilEntered()

            let cancelTask = Task { @MainActor in await coordinator.cancel() }
            await gate.waitUntilCancellationObserved()
            let stateBeforeRelease = await coordinator.state
            if !stateBeforeRelease.isActive {
                await waitUntil { await tunnel.resumeCallCount == 1 }
            }
            XCTAssertTrue(
                stateBeforeRelease.isActive,
                "Cancel must await the suspended \(stage) pause stage"
            )

            await gate.release()
            await cancelTask.value
            await waitUntil { await tunnel.pauseInvocationFinished }
            await waitUntil {
                if case .failed(_, .cancelled) = await coordinator.state { return true }
                return false
            }

            let snapshot = await tunnel.snapshot()
            XCTAssertTrue(snapshot.isRunning, "Running intent lost at \(stage)")
            XCTAssertTrue(snapshot.isOnDemandEnabled, "On-demand intent lost at \(stage)")
            XCTAssertEqual(snapshot.stopCount, 0, "Late stop escaped at \(stage)")
            XCTAssertEqual(snapshot.resumeCallCount, 0, "Coordinator restored twice at \(stage)")
            XCTAssertEqual(
                snapshot.restoreApplicationCount,
                stage == .initialLoad ? 0 : 1,
                "Pause rollback did not restore exactly once at \(stage)"
            )
        }
    }

    func testRecoveredAwaitingAirplaneModeConsumesFirstUnavailablePathSample() async {
        let clock = RotationClockStub()
        let receipt = TunnelRotationReceipt(wasRunning: true, wasOnDemandEnabled: true)
        let checkpoint = CellularIPRotationCheckpoint(
            state: .awaitingAirplaneMode(
                attemptID: 41,
                originalNetworkToken: "cellular-1",
                holdSeconds: 10,
                before: PublicIPSnapshot(ipv4: "198.51.100.10")
            ),
            savedAt: clock.now,
            timeoutDeadline: clock.now.addingTimeInterval(120),
            pauseDisposition: .paused(receipt)
        )
        let path = RotationPathObserverStub()
        let coordinator = await CellularIPRotationCoordinator(
            clock: clock,
            sleeper: RotationSleeperStub(),
            probe: RotationProbeStub(),
            pathObserver: path,
            checkpointStore: RotationCheckpointStoreStub(checkpoint: checkpoint),
            notificationCue: RotationNotificationCueStub(),
            tunnel: RotationTunnelStub(isRunning: false, isOnDemandEnabled: false)
        )

        await coordinator.resumeAfterActivation()
        path.emit(false)
        await waitUntil {
            if case .holding(41, 10, _, nil) = await coordinator.state { return true }
            return false
        }

        await coordinator.cancel()
    }

    func testPrePauseRecoveryPreservesFreshStoppedAndRunningIntentReceipts() async {
        let clock = RotationClockStub()
        let recoveryStates: [CellularIPRotationState] = [
            .awaitingConfirmation(
                attemptID: 41,
                originalNetworkToken: "cellular-1",
                holdSeconds: 10,
                activeStreamCount: 1
            ),
            .preparing(
                attemptID: 41,
                originalNetworkToken: "cellular-1",
                holdSeconds: 10,
                cellularLost: false,
                returnedNetworkToken: nil
            ),
        ]
        let intents = [
            (isRunning: false, isOnDemandEnabled: false),
            (isRunning: true, isOnDemandEnabled: true),
        ]

        for recoveryState in recoveryStates {
            for intent in intents {
                let store = RotationCheckpointStoreStub(checkpoint: CellularIPRotationCheckpoint(
                    state: recoveryState,
                    savedAt: clock.now,
                    pauseDisposition: .pending
                ))
                let tunnel = await RotationTunnelStub(
                    isRunning: intent.isRunning,
                    isOnDemandEnabled: intent.isOnDemandEnabled
                )
                let coordinator = await CellularIPRotationCoordinator(
                    clock: clock,
                    sleeper: RotationSleeperStub(),
                    probe: RotationProbeStub(snapshots: [
                        PublicIPSnapshot(ipv4: "198.51.100.10"),
                    ]),
                    pathObserver: RotationPathObserverStub(),
                    checkpointStore: store,
                    notificationCue: RotationNotificationCueStub(),
                    tunnel: tunnel
                )

                await coordinator.resumeAfterActivation()
                if case .awaitingConfirmation = recoveryState {
                    await coordinator.confirm(proceed: true)
                }
                await waitUntil {
                    if case .awaitingAirplaneMode = await coordinator.state { return true }
                    return false
                }
                await coordinator.cancel()
                await waitUntil { await tunnel.resumeReceipts.count == 1 }

                let snapshot = await tunnel.intentSnapshot()
                XCTAssertEqual(snapshot.isRunning, intent.isRunning)
                XCTAssertEqual(snapshot.isOnDemandEnabled, intent.isOnDemandEnabled)
                let receipt = await tunnel.resumeReceipts.first ?? nil
                XCTAssertNotNil(receipt, "Pre-pause recovery must retain a fresh receipt")
            }
        }
    }

    func testPauseCompletionPersistsOriginalIntentBeforeBeforeProbeCanBegin() async {
        let probeGate = RotationProbeGate()
        let path = RotationPathObserverStub()
        let store = RotationCheckpointStoreStub()
        let tunnel = await RotationTunnelStub(isRunning: true, isOnDemandEnabled: true)
        let coordinator = await CellularIPRotationCoordinator(
            clock: RotationClockStub(),
            sleeper: RotationSleeperStub(),
            probe: RotationProbeStub(
                snapshots: [PublicIPSnapshot(ipv4: "198.51.100.10")],
                gate: probeGate
            ),
            pathObserver: path,
            checkpointStore: store,
            notificationCue: RotationNotificationCueStub(),
            tunnel: tunnel
        )

        await coordinator.resumeAfterActivation()
        path.emit(true)
        await waitUntil { await coordinator.isCellularAvailable }
        await coordinator.updateAgentAvailability(
            isEnrolled: true,
            isAgentRunning: true,
            activeStreamCount: 0
        )
        await coordinator.start(holdSeconds: 10)
        await probeGate.waitUntilEntered()

        let checkpoints = store.savedCheckpoints
        XCTAssertEqual(checkpoints.count, 3)
        XCTAssertEqual(checkpoints[0].pauseDisposition, .pending)
        guard case let .pausing(pausingReceipt) = checkpoints[1].pauseDisposition else {
            XCTFail("Pre-pause checkpoint did not persist the opaque intent")
            await coordinator.cancel()
            await probeGate.open()
            return
        }
        if case let .paused(pausedReceipt) = checkpoints[2].pauseDisposition {
            // The original intent is durable before the probe dependency finishes.
            XCTAssertEqual(pausedReceipt, pausingReceipt)
        } else {
            XCTFail("Post-pause checkpoint did not persist the opaque intent")
        }

        await coordinator.cancel()
        await probeGate.open()
    }

    func testCoordinatorPersistsPausingReceiptBeforeAnyTunnelMutation() async {
        let pauseGate = RotationPauseApplicationGate()
        let path = RotationPathObserverStub()
        let store = RotationCheckpointStoreStub()
        let tunnel = await RotationTunnelStub(
            isRunning: true,
            isOnDemandEnabled: true,
            pauseApplicationGate: pauseGate
        )
        let coordinator = await CellularIPRotationCoordinator(
            clock: RotationClockStub(),
            sleeper: RotationSleeperStub(),
            probe: RotationProbeStub(snapshots: [
                PublicIPSnapshot(ipv4: "198.51.100.10"),
            ]),
            pathObserver: path,
            checkpointStore: store,
            notificationCue: RotationNotificationCueStub(),
            tunnel: tunnel
        )

        await coordinator.resumeAfterActivation()
        path.emit(true)
        await waitUntil { await coordinator.isCellularAvailable }
        await coordinator.updateAgentAvailability(
            isEnrolled: true,
            isAgentRunning: true,
            activeStreamCount: 0
        )
        await coordinator.start(holdSeconds: 10)
        await pauseGate.waitUntilEntered()

        let checkpoints = store.savedCheckpoints
        let intent = await tunnel.intentSnapshot()
        let captureCount = await tunnel.captureCount
        XCTAssertEqual(checkpoints.count, 2)
        XCTAssertEqual(checkpoints[0].pauseDisposition, .pending)
        if case .pausing = checkpoints[1].pauseDisposition {
            // The original receipt is durable while the tunnel is still untouched.
        } else {
            XCTFail("Original intent was not persisted before pause application")
        }
        XCTAssertEqual(intent.isRunning, true)
        XCTAssertEqual(intent.isOnDemandEnabled, true)
        XCTAssertEqual(captureCount, 1)

        await pauseGate.open()
        await waitUntil {
            if case .awaitingAirplaneMode = await coordinator.state { return true }
            return false
        }
        await coordinator.cancel()
    }

    func testPausingRecoveryUsesPersistedReceiptAcrossEveryCrashBoundary() async throws {
        let clock = RotationClockStub()
        let intents = [
            (isRunning: false, isOnDemandEnabled: false),
            (isRunning: true, isOnDemandEnabled: true),
        ]

        for intent in intents {
            let receiptSource = await RotationTunnelStub(
                isRunning: intent.isRunning,
                isOnDemandEnabled: intent.isOnDemandEnabled
            )
            let receipt = try await receiptSource.captureRotationIntent()
            let crashBoundaries = [
                (
                    name: "after receipt checkpoint",
                    isRunning: intent.isRunning,
                    isOnDemandEnabled: intent.isOnDemandEnabled
                ),
                (
                    name: "after disable save and reload",
                    isRunning: intent.isRunning,
                    isOnDemandEnabled: false
                ),
                (
                    name: "after stop before paused checkpoint",
                    isRunning: false,
                    isOnDemandEnabled: false
                ),
            ]

            for boundary in crashBoundaries {
                let store = RotationCheckpointStoreStub(checkpoint: CellularIPRotationCheckpoint(
                    state: .preparing(
                        attemptID: 41,
                        originalNetworkToken: "cellular-1",
                        holdSeconds: 10,
                        cellularLost: false,
                        returnedNetworkToken: nil
                    ),
                    savedAt: clock.now,
                    pauseDisposition: .pausing(receipt)
                ))
                let tunnel = await RotationTunnelStub(
                    isRunning: boundary.isRunning,
                    isOnDemandEnabled: boundary.isOnDemandEnabled
                )
                let coordinator = await CellularIPRotationCoordinator(
                    clock: clock,
                    sleeper: RotationSleeperStub(),
                    probe: RotationProbeStub(snapshots: [
                        PublicIPSnapshot(ipv4: "198.51.100.10"),
                    ]),
                    pathObserver: RotationPathObserverStub(),
                    checkpointStore: store,
                    notificationCue: RotationNotificationCueStub(),
                    tunnel: tunnel
                )

                await coordinator.resumeAfterActivation()
                await waitUntil {
                    if case .awaitingAirplaneMode = await coordinator.state { return true }
                    return false
                }
                await coordinator.cancel()
                await waitUntil { await tunnel.resumeReceipts.count == 1 }

                let restored = await tunnel.intentSnapshot()
                let captureCount = await tunnel.captureCount
                let pauseCount = await tunnel.pauseCount
                let resumeReceipt = await tunnel.resumeReceipts.first ?? nil
                XCTAssertEqual(captureCount, 0, boundary.name)
                XCTAssertEqual(pauseCount, 1, boundary.name)
                XCTAssertEqual(resumeReceipt, receipt, boundary.name)
                XCTAssertEqual(restored.isRunning, intent.isRunning, boundary.name)
                XCTAssertEqual(
                    restored.isOnDemandEnabled,
                    intent.isOnDemandEnabled,
                    boundary.name
                )
            }
        }
    }

    func testCancelBeforeRecoveredPauseApplicationRestoresPersistedIntentExactlyOnce() async {
        let clock = RotationClockStub()
        let intents = [
            TunnelRotationReceipt(wasRunning: false, wasOnDemandEnabled: false),
            TunnelRotationReceipt(wasRunning: true, wasOnDemandEnabled: true),
        ]

        for receipt in intents {
            let pauseGate = RotationPauseApplicationGate()
            let tunnel = await RotationTunnelStub(
                isRunning: false,
                isOnDemandEnabled: false,
                pauseApplicationGate: pauseGate
            )
            let coordinator = await CellularIPRotationCoordinator(
                clock: clock,
                sleeper: RotationSleeperStub(),
                probe: RotationProbeStub(snapshots: [
                    PublicIPSnapshot(ipv4: "198.51.100.10"),
                ]),
                pathObserver: RotationPathObserverStub(),
                checkpointStore: RotationCheckpointStoreStub(
                    checkpoint: CellularIPRotationCheckpoint(
                        state: .preparing(
                            attemptID: 41,
                            originalNetworkToken: "cellular-1",
                            holdSeconds: 10,
                            cellularLost: false,
                            returnedNetworkToken: nil
                        ),
                        savedAt: clock.now,
                        pauseDisposition: .pausing(receipt)
                    )
                ),
                notificationCue: RotationNotificationCueStub(),
                tunnel: tunnel
            )

            await coordinator.resumeAfterActivation()
            await pauseGate.waitUntilEntered()
            let cancelTask = Task { @MainActor in await coordinator.cancel() }
            await Task.yield()
            await pauseGate.open()
            await cancelTask.value
            await waitUntil { await tunnel.resumeReceipts.count == 1 }

            let restored = await tunnel.intentSnapshot()
            let resumeReceipts = await tunnel.resumeReceipts
            XCTAssertEqual(resumeReceipts, [receipt])
            XCTAssertEqual(restored.isRunning, receipt.wasRunning)
            XCTAssertEqual(restored.isOnDemandEnabled, receipt.wasOnDemandEnabled)
        }
    }

    func testLegacyMissingReceiptFailsSafeForEveryLaterActiveRecoveryState() async {
        let clock = RotationClockStub()
        let laterStates: [(CellularIPRotationState, Date?)] = [
            (
                .awaitingAirplaneMode(
                    attemptID: 41,
                    originalNetworkToken: "cellular-1",
                    holdSeconds: 10,
                    before: PublicIPSnapshot(ipv4: "198.51.100.10")
                ),
                clock.now.addingTimeInterval(120)
            ),
            (
                .holding(
                    attemptID: 41,
                    remainingSeconds: 10,
                    before: PublicIPSnapshot(ipv4: "198.51.100.10"),
                    returnedNetworkToken: nil
                ),
                nil
            ),
            (
                .awaitingCellularReturn(
                    attemptID: 41,
                    before: PublicIPSnapshot(ipv4: "198.51.100.10")
                ),
                clock.now.addingTimeInterval(180)
            ),
            (
                .verifying(
                    attemptID: 41,
                    before: PublicIPSnapshot(ipv4: "198.51.100.10"),
                    returnedNetworkToken: "cellular-2"
                ),
                nil
            ),
        ]

        for (state, deadline) in laterStates {
            let tunnel = await RotationTunnelStub(isRunning: false, isOnDemandEnabled: false)
            let coordinator = await CellularIPRotationCoordinator(
                clock: clock,
                sleeper: RotationSleeperStub(),
                probe: RotationProbeStub(),
                pathObserver: RotationPathObserverStub(),
                checkpointStore: RotationCheckpointStoreStub(
                    checkpoint: CellularIPRotationCheckpoint(
                        state: state,
                        savedAt: clock.now,
                        timeoutDeadline: deadline,
                        pauseDisposition: .legacyUnknown
                    )
                ),
                notificationCue: RotationNotificationCueStub(),
                tunnel: tunnel
            )

            await coordinator.resumeAfterActivation()
            await waitUntil {
                if case .failed(41, .recoveryExpired) = await coordinator.state { return true }
                return false
            }
            await waitUntil { await tunnel.resumeReceipts.count == 1 }

            let captureCount = await tunnel.captureCount
            let pauseCount = await tunnel.pauseCount
            let resumeReceipt = await tunnel.resumeReceipts.first ?? nil
            XCTAssertEqual(captureCount, 0)
            XCTAssertEqual(pauseCount, 0)
            XCTAssertNil(resumeReceipt)
        }
    }

    func testPreparingRecoveryDistinguishesBeforePauseAndAfterStopCrashCheckpoints() async throws {
        let clock = RotationClockStub()
        let intents = [
            (isRunning: false, isOnDemandEnabled: false),
            (isRunning: true, isOnDemandEnabled: true),
        ]

        for intent in intents {
            let receiptSource = await RotationTunnelStub(
                isRunning: intent.isRunning,
                isOnDemandEnabled: intent.isOnDemandEnabled
            )
            let originalReceipt = try await receiptSource.captureRotationIntent()
            let crashCases: [(
                disposition: CellularIPRotationPauseDisposition,
                recoveredRunning: Bool,
                recoveredOnDemand: Bool,
                expectedPauseCount: Int
            )] = [
                (.pending, intent.isRunning, intent.isOnDemandEnabled, 1),
                (.paused(originalReceipt), false, false, 0),
            ]

            for crashCase in crashCases {
                let checkpoint = CellularIPRotationCheckpoint(
                    state: .preparing(
                        attemptID: 41,
                        originalNetworkToken: "cellular-1",
                        holdSeconds: 10,
                        cellularLost: false,
                        returnedNetworkToken: nil
                    ),
                    savedAt: clock.now,
                    pauseDisposition: crashCase.disposition
                )
                let tunnel = await RotationTunnelStub(
                    isRunning: crashCase.recoveredRunning,
                    isOnDemandEnabled: crashCase.recoveredOnDemand
                )
                let coordinator = await CellularIPRotationCoordinator(
                    clock: clock,
                    sleeper: RotationSleeperStub(),
                    probe: RotationProbeStub(snapshots: [
                        PublicIPSnapshot(ipv4: "198.51.100.10"),
                    ]),
                    pathObserver: RotationPathObserverStub(),
                    checkpointStore: RotationCheckpointStoreStub(checkpoint: checkpoint),
                    notificationCue: RotationNotificationCueStub(),
                    tunnel: tunnel
                )

                await coordinator.resumeAfterActivation()
                await waitUntil {
                    if case .awaitingAirplaneMode = await coordinator.state { return true }
                    return false
                }
                await coordinator.cancel()
                await waitUntil { await tunnel.resumeReceipts.count == 1 }

                let restoredIntent = await tunnel.intentSnapshot()
                let pauseCount = await tunnel.pauseCount
                XCTAssertEqual(pauseCount, crashCase.expectedPauseCount)
                XCTAssertEqual(restoredIntent.isRunning, intent.isRunning)
                XCTAssertEqual(restoredIntent.isOnDemandEnabled, intent.isOnDemandEnabled)
            }
        }
    }

    func testTerminalRetirementPreventsReplayWhenPhysicalCheckpointDeletionFails() async {
        let clock = RotationClockStub()
        let receipt = TunnelRotationReceipt(wasRunning: true, wasOnDemandEnabled: true)
        let checkpoint = CellularIPRotationCheckpoint(
            state: .awaitingAirplaneMode(
                attemptID: 41,
                originalNetworkToken: "cellular-1",
                holdSeconds: 10,
                before: PublicIPSnapshot(ipv4: "198.51.100.10")
            ),
            savedAt: clock.now.addingTimeInterval(-30),
            timeoutDeadline: clock.now.addingTimeInterval(90),
            pauseDisposition: .paused(receipt)
        )
        let store = RotationCheckpointStoreStub(
            checkpoint: checkpoint,
            simulatesFailingPhysicalRetirement: true
        )
        let sleeper = RotationSleeperStub()
        let first = await CellularIPRotationCoordinator(
            clock: clock,
            sleeper: sleeper,
            probe: RotationProbeStub(),
            pathObserver: RotationPathObserverStub(),
            checkpointStore: store,
            notificationCue: RotationNotificationCueStub(),
            tunnel: RotationTunnelStub(isRunning: false, isOnDemandEnabled: false)
        )

        await first.resumeAfterActivation()
        await waitUntil { await sleeper.pendingSeconds.contains(90) }
        await sleeper.fire(seconds: 90)
        await waitUntil {
            if case .failed(41, .cellularDidNotDisconnect) = await first.state { return true }
            return false
        }

        let second = await CellularIPRotationCoordinator(
            clock: clock,
            sleeper: RotationSleeperStub(),
            probe: RotationProbeStub(),
            pathObserver: RotationPathObserverStub(),
            checkpointStore: store,
            notificationCue: RotationNotificationCueStub(),
            tunnel: RotationTunnelStub()
        )
        await second.resumeAfterActivation()

        XCTAssertEqual(store.retireCount, 1)
        XCTAssertEqual(store.loadCount, 2)
        let secondState = await second.state
        XCTAssertEqual(secondState, .idle)
    }

    func testDoubleRetirementFailureBecomesFiniteOnlyAfterOriginalIntentRestoration() async {
        let resumeGate = RotationResumeGate()
        let path = RotationPathObserverStub()
        let store = RotationCheckpointStoreStub(retirementError: .writeFailed)
        let tunnel = await RotationTunnelStub(
            isRunning: true,
            isOnDemandEnabled: true,
            resumeGate: resumeGate
        )
        let coordinator = await CellularIPRotationCoordinator(
            clock: RotationClockStub(),
            sleeper: RotationSleeperStub(),
            probe: RotationProbeStub(snapshots: [
                PublicIPSnapshot(ipv4: "198.51.100.10"),
            ]),
            pathObserver: path,
            checkpointStore: store,
            notificationCue: RotationNotificationCueStub(),
            tunnel: tunnel
        )

        await coordinator.resumeAfterActivation()
        path.emit(true)
        await waitUntil { await coordinator.isCellularAvailable }
        await coordinator.updateAgentAvailability(
            isEnrolled: true,
            isAgentRunning: true,
            activeStreamCount: 0
        )
        await coordinator.start(holdSeconds: 10)
        await waitUntil {
            if case .awaitingAirplaneMode = await coordinator.state { return true }
            return false
        }

        await coordinator.cancel()
        await resumeGate.waitUntilEntered()
        let stateWhileRestorationIsPending = await coordinator.state
        XCTAssertEqual(
            stateWhileRestorationIsPending,
            .restoring(attemptID: 1, outcome: .failed(.cancelled))
        )
        XCTAssertEqual(store.retireCount, 0)

        await resumeGate.open()
        await waitUntil {
            if case .failed(1, .checkpointRetirementFailed) = await coordinator.state {
                return true
            }
            return false
        }

        let restoredIntent = await tunnel.intentSnapshot()
        let resumeCount = await tunnel.resumeReceipts.count
        XCTAssertTrue(restoredIntent.isRunning)
        XCTAssertTrue(restoredIntent.isOnDemandEnabled)
        XCTAssertEqual(resumeCount, 1)
        XCTAssertEqual(store.retireCount, 1, "Finite failure must not recurse into retirement")
    }

    func testCancellationRejectsLateProbeCallbackAndClearsCheckpointAndCue() async {
        let gate = RotationProbeGate()
        let path = RotationPathObserverStub()
        let store = RotationCheckpointStoreStub()
        let cue = RotationNotificationCueStub()
        let tunnel = await RotationTunnelStub()
        let coordinator = await CellularIPRotationCoordinator(
            clock: RotationClockStub(),
            sleeper: RotationSleeperStub(),
            probe: RotationProbeStub(
                snapshots: [PublicIPSnapshot(ipv4: "198.51.100.10")],
                gate: gate
            ),
            pathObserver: path,
            checkpointStore: store,
            notificationCue: cue,
            tunnel: tunnel
        )

        await coordinator.resumeAfterActivation()
        path.emit(true)
        await waitUntil { await coordinator.isCellularAvailable }
        await coordinator.updateAgentAvailability(
            isEnrolled: true,
            isAgentRunning: true,
            activeStreamCount: 0
        )
        await coordinator.start(holdSeconds: 10)
        await gate.waitUntilEntered()

        await coordinator.cancel()
        await waitUntil {
            if case .failed(_, .cancelled) = await coordinator.state { return true }
            return false
        }
        await gate.open()
        await waitUntil { await tunnel.resumeReceipts.count == 1 }

        if case .failed(_, .cancelled) = await coordinator.state {
            // Expected: the late probe cannot advance the terminal attempt.
        } else {
            XCTFail("Late probe callback changed a cancelled attempt")
        }
        XCTAssertEqual(store.retireCount, 1)
        let cancelledAttemptIDs = await cue.snapshot().cancelledAttemptIDs
        let resumeCount = await tunnel.resumeReceipts.count
        XCTAssertEqual(cancelledAttemptIDs, [1])
        XCTAssertEqual(resumeCount, 1)
    }

    func testActivationRecoversCheckpointOnceUsingOriginalDeadlineAndStoredReceipt() async {
        let clock = RotationClockStub()
        let receipt = TunnelRotationReceipt(wasRunning: true, wasOnDemandEnabled: true)
        let checkpoint = CellularIPRotationCheckpoint(
            state: .awaitingAirplaneMode(
                attemptID: 41,
                originalNetworkToken: "cellular-1",
                holdSeconds: 10,
                before: PublicIPSnapshot(ipv4: "198.51.100.10")
            ),
            savedAt: clock.now.addingTimeInterval(-30),
            timeoutDeadline: clock.now.addingTimeInterval(90),
            pauseDisposition: .paused(receipt)
        )
        let store = RotationCheckpointStoreStub(checkpoint: checkpoint)
        let sleeper = RotationSleeperStub()
        let tunnel = await RotationTunnelStub(isRunning: false, isOnDemandEnabled: false)
        let coordinator = await CellularIPRotationCoordinator(
            clock: clock,
            sleeper: sleeper,
            probe: RotationProbeStub(),
            pathObserver: RotationPathObserverStub(),
            checkpointStore: store,
            notificationCue: RotationNotificationCueStub(),
            tunnel: tunnel
        )

        await coordinator.resumeAfterActivation()
        await waitUntil { await sleeper.pendingSeconds.contains(90) }
        await coordinator.resumeAfterActivation()
        XCTAssertEqual(store.loadCount, 1)
        let pendingSeconds = await sleeper.pendingSeconds
        XCTAssertFalse(pendingSeconds.contains(120))

        await sleeper.fire(seconds: 90)
        await waitUntil {
            if case .failed(41, .cellularDidNotDisconnect) = await coordinator.state { return true }
            return false
        }
        await waitUntil { await tunnel.resumeReceipts.count == 1 }
        let resumeReceipts = await tunnel.resumeReceipts
        XCTAssertEqual(resumeReceipts, [receipt])
    }

    func testMalformedRecoveryFailsFiniteClearsDataAndNeverReplays() async {
        let store = RotationCheckpointStoreStub(loadError: .malformed)
        let tunnel = await RotationTunnelStub()
        let coordinator = await CellularIPRotationCoordinator(
            clock: RotationClockStub(),
            sleeper: RotationSleeperStub(),
            probe: RotationProbeStub(),
            pathObserver: RotationPathObserverStub(),
            checkpointStore: store,
            notificationCue: RotationNotificationCueStub(),
            tunnel: tunnel
        )

        await coordinator.resumeAfterActivation()
        await waitUntil {
            if case .failed(_, .recoveryExpired) = await coordinator.state { return true }
            return false
        }
        await coordinator.resumeAfterActivation()
        await waitUntil { await tunnel.resumeReceipts.count == 1 }

        XCTAssertEqual(store.loadCount, 1)
        XCTAssertGreaterThanOrEqual(store.clearCount, 1)
        let resumeReceipts = await tunnel.resumeReceipts
        XCTAssertEqual(resumeReceipts, [nil])
    }

    func testResumeFailureBecomesFiniteTerminalWithoutReplay() async {
        let path = RotationPathObserverStub()
        let tunnel = await RotationTunnelStub(failResume: true)
        let coordinator = await makeCoordinator(
            sleeper: RotationSleeperStub(immediateUnitSleeps: true),
            path: path,
            tunnel: tunnel,
            probe: RotationProbeStub(snapshots: [
                PublicIPSnapshot(ipv4: "198.51.100.10"),
                PublicIPSnapshot(ipv4: "198.51.100.11"),
            ])
        )

        await coordinator.resumeAfterActivation()
        path.emit(true)
        await waitUntil { await coordinator.isCellularAvailable }
        await coordinator.updateAgentAvailability(
            isEnrolled: true,
            isAgentRunning: true,
            activeStreamCount: 0
        )
        await coordinator.start(holdSeconds: 10)
        await waitUntil {
            if case .awaitingAirplaneMode = await coordinator.state { return true }
            return false
        }
        path.emit(false)
        path.emit(true)

        await waitUntil {
            if case .failed(_, .tunnelResumeFailed) = await coordinator.state { return true }
            return false
        }
        let firstResumeCount = await tunnel.resumeReceipts.count
        XCTAssertEqual(firstResumeCount, 1)
        await coordinator.resumeAfterActivation()
        let finalResumeCount = await tunnel.resumeReceipts.count
        XCTAssertEqual(finalResumeCount, 1)
    }

    private func makeCoordinator(
        sleeper: RotationSleeperStub,
        path: RotationPathObserverStub,
        tunnel: RotationTunnelStub,
        probe: RotationProbeStub
    ) async -> CellularIPRotationCoordinator<RotationTunnelStub> {
        await CellularIPRotationCoordinator(
            clock: RotationClockStub(),
            sleeper: sleeper,
            probe: probe,
            pathObserver: path,
            checkpointStore: RotationCheckpointStoreStub(),
            notificationCue: RotationNotificationCueStub(),
            tunnel: tunnel
        )
    }

    private func waitUntil(
        _ condition: @escaping @Sendable () async -> Bool,
        file: StaticString = #filePath,
        line: UInt = #line
    ) async {
        for _ in 0 ..< 2_000 {
            if await condition() { return }
            await Task.yield()
        }
        XCTFail("Timed out waiting for coordinator state", file: file, line: line)
    }
}

private struct RotationClockStub: CellularIPRotationClock {
    let now = Date(timeIntervalSince1970: 2_100_000_000)

    func currentDate() -> Date { now }
}

private actor RotationSleeperStub: CellularIPRotationSleeping {
    private struct PendingSleep {
        let seconds: Int
        let continuation: CheckedContinuation<Void, Error>
    }

    private let immediateUnitSleeps: Bool
    private var pending: [UUID: PendingSleep] = [:]

    init(immediateUnitSleeps: Bool = false) {
        self.immediateUnitSleeps = immediateUnitSleeps
    }

    var pendingSeconds: [Int] {
        pending.values.map(\.seconds)
    }

    func sleep(seconds: Int) async throws {
        if immediateUnitSleeps && seconds == 1 {
            await Task.yield()
            return
        }
        let identifier = UUID()
        try await withTaskCancellationHandler {
            try await withCheckedThrowingContinuation { continuation in
                pending[identifier] = PendingSleep(seconds: seconds, continuation: continuation)
            }
        } onCancel: {
            Task { await self.cancel(identifier) }
        }
    }

    func fire(seconds: Int) {
        guard let entry = pending.first(where: { $0.value.seconds == seconds }) else { return }
        pending.removeValue(forKey: entry.key)?.continuation.resume()
    }

    private func cancel(_ identifier: UUID) {
        pending.removeValue(forKey: identifier)?.continuation.resume(throwing: CancellationError())
    }
}

private actor RotationProbeStub: CellularPublicIPProbing {
    private var snapshots: [PublicIPSnapshot]
    private let gate: RotationProbeGate?

    init(snapshots: [PublicIPSnapshot] = [], gate: RotationProbeGate? = nil) {
        self.snapshots = snapshots
        self.gate = gate
    }

    func probe() async -> PublicIPSnapshot {
        if let gate { await gate.wait() }
        return snapshots.isEmpty ? PublicIPSnapshot() : snapshots.removeFirst()
    }
}

private actor RotationProbeGate {
    private var isOpen = false
    private var isEntered = false
    private var waiters: [CheckedContinuation<Void, Never>] = []
    private var entryWaiters: [CheckedContinuation<Void, Never>] = []

    func wait() async {
        isEntered = true
        let entered = entryWaiters
        entryWaiters.removeAll()
        entered.forEach { $0.resume() }
        guard !isOpen else { return }
        await withCheckedContinuation { waiters.append($0) }
    }

    func waitUntilEntered() async {
        guard !isEntered else { return }
        await withCheckedContinuation { entryWaiters.append($0) }
    }

    func open() {
        isOpen = true
        let pending = waiters
        waiters.removeAll()
        pending.forEach { $0.resume() }
    }
}

private actor RotationResumeGate {
    private var isOpen = false
    private var isEntered = false
    private var waiters: [CheckedContinuation<Void, Never>] = []
    private var entryWaiters: [CheckedContinuation<Void, Never>] = []

    func wait() async {
        isEntered = true
        entryWaiters.forEach { $0.resume() }
        entryWaiters.removeAll()
        guard !isOpen else { return }
        await withCheckedContinuation { waiters.append($0) }
    }

    func waitUntilEntered() async {
        guard !isEntered else { return }
        await withCheckedContinuation { entryWaiters.append($0) }
    }

    func open() {
        isOpen = true
        waiters.forEach { $0.resume() }
        waiters.removeAll()
    }
}

private final class RotationPathObserverStub: CellularPathObserving, @unchecked Sendable {
    private let lock = NSLock()
    private var handler: CellularPathAvailabilityHandler?

    func start(handler: @escaping CellularPathAvailabilityHandler) {
        lock.withLock { self.handler = handler }
    }

    func cancel() {
        lock.withLock { handler = nil }
    }

    func emit(_ available: Bool) {
        let callback = lock.withLock { handler }
        callback?(available)
    }
}

private final class RotationCheckpointStoreStub: CellularIPRotationCheckpointStoring, @unchecked Sendable {
    private let lock = NSLock()
    private var checkpoint: CellularIPRotationCheckpoint?
    private let loadError: CellularIPRotationCheckpointStoreError?
    private let simulatesFailingPhysicalRetirement: Bool
    private let retirementError: CellularIPRotationCheckpointStoreError?
    private var saved: [CellularIPRotationCheckpoint] = []
    private var loads = 0
    private var clears = 0
    private var retires = 0
    private var highestRetiredAttemptID: UInt64?

    init(
        checkpoint: CellularIPRotationCheckpoint? = nil,
        loadError: CellularIPRotationCheckpointStoreError? = nil,
        simulatesFailingPhysicalRetirement: Bool = false,
        retirementError: CellularIPRotationCheckpointStoreError? = nil
    ) {
        self.checkpoint = checkpoint
        self.loadError = loadError
        self.simulatesFailingPhysicalRetirement = simulatesFailingPhysicalRetirement
        self.retirementError = retirementError
    }

    var lastSaved: CellularIPRotationCheckpoint? { lock.withLock { saved.last } }
    var savedCheckpoints: [CellularIPRotationCheckpoint] { lock.withLock { saved } }
    var loadCount: Int { lock.withLock { loads } }
    var clearCount: Int { lock.withLock { clears } }
    var retireCount: Int { lock.withLock { retires } }
    var currentCheckpoint: CellularIPRotationCheckpoint? { lock.withLock { checkpoint } }

    func save(_ checkpoint: CellularIPRotationCheckpoint) throws {
        lock.withLock {
            self.checkpoint = checkpoint
            saved.append(checkpoint)
        }
    }

    func load(at _: Date) throws -> CellularIPRotationCheckpoint? {
        try lock.withLock {
            loads += 1
            if let loadError { throw loadError }
            if let attemptID = checkpoint?.state.attemptID,
               let highestRetiredAttemptID,
               attemptID <= highestRetiredAttemptID {
                return nil
            }
            return checkpoint
        }
    }

    func clear() throws {
        try lock.withLock {
            clears += 1
            if simulatesFailingPhysicalRetirement {
                throw CellularIPRotationCheckpointStoreError.writeFailed
            }
            checkpoint = nil
        }
    }

    func retire(attemptID: UInt64) throws {
        try lock.withLock {
            retires += 1
            if let retirementError { throw retirementError }
            highestRetiredAttemptID = max(highestRetiredAttemptID ?? 0, attemptID)
            if !simulatesFailingPhysicalRetirement {
                checkpoint = nil
            }
        }
    }
}

private actor RotationNotificationCueStub: CellularIPRotationNotificationCueing {
    struct Snapshot: Sendable {
        let scheduledDeadlines: [Date]
        let cancelledAttemptIDs: [UInt64]
    }

    private let scheduleResult: CellularIPRotationNotificationCueResult
    private var scheduledDeadlines: [Date] = []
    private var cancelledAttemptIDs: [UInt64] = []

    init(scheduleResult: CellularIPRotationNotificationCueResult = .scheduled) {
        self.scheduleResult = scheduleResult
    }

    func schedule(
        attemptID _: UInt64,
        holdDeadline: Date
    ) async -> CellularIPRotationNotificationCueResult {
        scheduledDeadlines.append(holdDeadline)
        return scheduleResult
    }

    func cancel(attemptID: UInt64) async {
        cancelledAttemptIDs.append(attemptID)
    }

    func snapshot() -> Snapshot {
        Snapshot(
            scheduledDeadlines: scheduledDeadlines,
            cancelledAttemptIDs: cancelledAttemptIDs
        )
    }
}

@MainActor
private final class RotationTunnelStub:
    CellularIPRotationTunnelControlling,
    TunnelRotationPreferenceSession
{
    private(set) var captureCount = 0
    private(set) var pauseCount = 0
    private(set) var resumeReceipts: [TunnelRotationReceipt?] = []
    private let failResume: Bool
    private let resumeGate: RotationResumeGate?
    private let appliesIntentBeforeResumeGate: Bool
    private let pauseApplicationGate: RotationPauseApplicationGate?
    private(set) var isRotationTunnelRunning: Bool
    private(set) var isOnDemandEnabled: Bool

    init(
        failResume: Bool = false,
        isRunning: Bool = true,
        isOnDemandEnabled: Bool = true,
        resumeGate: RotationResumeGate? = nil,
        appliesIntentBeforeResumeGate: Bool = false,
        pauseApplicationGate: RotationPauseApplicationGate? = nil
    ) {
        self.failResume = failResume
        self.resumeGate = resumeGate
        self.appliesIntentBeforeResumeGate = appliesIntentBeforeResumeGate
        self.pauseApplicationGate = pauseApplicationGate
        isRotationTunnelRunning = isRunning
        self.isOnDemandEnabled = isOnDemandEnabled
    }

    func captureRotationIntent() async throws -> TunnelRotationReceipt {
        captureCount += 1
        return try await TunnelRotationPreferenceTransaction.captureIntent(using: self)
    }

    func pauseForRotation(using receipt: TunnelRotationReceipt) async throws {
        pauseCount += 1
        if let pauseApplicationGate { await pauseApplicationGate.wait() }
        try await TunnelRotationPreferenceTransaction.pause(using: self, receipt: receipt)
    }

    func resumeAfterRotation(_ receipt: TunnelRotationReceipt?) async throws {
        resumeReceipts.append(receipt)
        if appliesIntentBeforeResumeGate {
            try await TunnelRotationPreferenceTransaction.resume(using: self, receipt: receipt)
            if let resumeGate { await resumeGate.wait() }
        } else {
            if let resumeGate { await resumeGate.wait() }
            try await TunnelRotationPreferenceTransaction.resume(using: self, receipt: receipt)
        }
        if failResume { throw RotationTestError.injected }
    }

    func intentSnapshot() -> (isRunning: Bool, isOnDemandEnabled: Bool) {
        (isRotationTunnelRunning, isOnDemandEnabled)
    }

    func loadPreferences() async throws {}

    func applyConfiguration(onDemandEnabled: Bool) {
        isOnDemandEnabled = onDemandEnabled
    }

    func savePreferences() async throws {}

    func startTunnelSession() throws {
        isRotationTunnelRunning = true
    }

    func stopTunnelSession() {
        isRotationTunnelRunning = false
    }
}

private actor RotationPauseApplicationGate {
    private var entered = false
    private var isOpen = false
    private var entryWaiters: [CheckedContinuation<Void, Never>] = []
    private var applicationWaiters: [CheckedContinuation<Void, Never>] = []

    func wait() async {
        entered = true
        entryWaiters.forEach { $0.resume() }
        entryWaiters.removeAll()
        guard !isOpen else { return }
        await withCheckedContinuation { applicationWaiters.append($0) }
    }

    func waitUntilEntered() async {
        guard !entered else { return }
        await withCheckedContinuation { entryWaiters.append($0) }
    }

    func open() {
        isOpen = true
        applicationWaiters.forEach { $0.resume() }
        applicationWaiters.removeAll()
    }
}

private enum SuspendedPauseStage: String, CaseIterable, Sendable {
    case initialLoad
    case save
    case reload
}

private actor PauseStageGate {
    private var didEnter = false
    private var didObserveCancellation = false
    private var isReleased = false
    private var suspension: CheckedContinuation<Void, Never>?
    private var entryWaiters: [CheckedContinuation<Void, Never>] = []
    private var cancellationWaiters: [CheckedContinuation<Void, Never>] = []

    func suspend() async {
        didEnter = true
        entryWaiters.forEach { $0.resume() }
        entryWaiters.removeAll()

        await withTaskCancellationHandler {
            await withCheckedContinuation { continuation in
                if isReleased {
                    continuation.resume()
                } else {
                    suspension = continuation
                }
            }
        } onCancel: {
            Task { await self.recordCancellation() }
        }
    }

    func waitUntilEntered() async {
        guard !didEnter else { return }
        await withCheckedContinuation { entryWaiters.append($0) }
    }

    func waitUntilCancellationObserved() async {
        guard !didObserveCancellation else { return }
        await withCheckedContinuation { cancellationWaiters.append($0) }
    }

    func release() {
        isReleased = true
        suspension?.resume()
        suspension = nil
    }

    private func recordCancellation() {
        didObserveCancellation = true
        cancellationWaiters.forEach { $0.resume() }
        cancellationWaiters.removeAll()
    }
}

@MainActor
private final class StageControlledRotationTunnel:
    CellularIPRotationTunnelControlling,
    TunnelRotationPreferenceSession
{
    struct Snapshot: Sendable {
        let isRunning: Bool
        let isOnDemandEnabled: Bool
        let stopCount: Int
        let resumeCallCount: Int
        let restoreApplicationCount: Int
    }

    private let stage: SuspendedPauseStage
    private let gate: PauseStageGate
    private var loadCount = 0
    private var saveCount = 0
    private(set) var isRotationTunnelRunning = true
    private(set) var isOnDemandEnabled = true
    private(set) var stopCount = 0
    private(set) var resumeCallCount = 0
    private(set) var restoreApplicationCount = 0
    private(set) var pauseInvocationFinished = false

    init(stage: SuspendedPauseStage, gate: PauseStageGate) {
        self.stage = stage
        self.gate = gate
    }

    func captureRotationIntent() async throws -> TunnelRotationReceipt {
        do {
            return try await TunnelRotationPreferenceTransaction.captureIntent(using: self)
        } catch {
            pauseInvocationFinished = true
            throw error
        }
    }

    func pauseForRotation(using receipt: TunnelRotationReceipt) async throws {
        defer { pauseInvocationFinished = true }
        try await TunnelRotationPreferenceTransaction.pause(using: self, receipt: receipt)
    }

    func resumeAfterRotation(_ receipt: TunnelRotationReceipt?) async throws {
        resumeCallCount += 1
        try await TunnelRotationPreferenceTransaction.resume(using: self, receipt: receipt)
    }

    func loadPreferences() async throws {
        loadCount += 1
        if stage == .initialLoad, loadCount == 1 {
            await gate.suspend()
        } else if stage == .reload, loadCount == 2 {
            await gate.suspend()
        }
    }

    func applyConfiguration(onDemandEnabled: Bool) {
        isOnDemandEnabled = onDemandEnabled
        if onDemandEnabled {
            restoreApplicationCount += 1
        }
    }

    func savePreferences() async throws {
        saveCount += 1
        if stage == .save, saveCount == 1 {
            await gate.suspend()
        }
    }

    func startTunnelSession() throws {
        isRotationTunnelRunning = true
    }

    func stopTunnelSession() {
        stopCount += 1
        isRotationTunnelRunning = false
    }

    func snapshot() -> Snapshot {
        Snapshot(
            isRunning: isRotationTunnelRunning,
            isOnDemandEnabled: isOnDemandEnabled,
            stopCount: stopCount,
            resumeCallCount: resumeCallCount,
            restoreApplicationCount: restoreApplicationCount
        )
    }
}

private enum RotationTestError: Error {
    case injected
}
