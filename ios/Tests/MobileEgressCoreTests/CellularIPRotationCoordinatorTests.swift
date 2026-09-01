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
        let checkpoint = CellularIPRotationCheckpoint(
            state: .awaitingAirplaneMode(
                attemptID: 41,
                originalNetworkToken: "cellular-1",
                holdSeconds: 10,
                before: PublicIPSnapshot(ipv4: "198.51.100.10")
            ),
            savedAt: clock.now,
            timeoutDeadline: clock.now.addingTimeInterval(120)
        )
        let path = RotationPathObserverStub()
        let coordinator = await CellularIPRotationCoordinator(
            clock: clock,
            sleeper: RotationSleeperStub(),
            probe: RotationProbeStub(),
            pathObserver: path,
            checkpointStore: RotationCheckpointStoreStub(checkpoint: checkpoint),
            notificationCue: RotationNotificationCueStub(),
            tunnel: RotationTunnelStub()
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
                    savedAt: clock.now
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

    func testTerminalRetirementPreventsReplayWhenPhysicalCheckpointDeletionFails() async {
        let clock = RotationClockStub()
        let checkpoint = CellularIPRotationCheckpoint(
            state: .awaitingAirplaneMode(
                attemptID: 41,
                originalNetworkToken: "cellular-1",
                holdSeconds: 10,
                before: PublicIPSnapshot(ipv4: "198.51.100.10")
            ),
            savedAt: clock.now.addingTimeInterval(-30),
            timeoutDeadline: clock.now.addingTimeInterval(90)
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
            tunnel: RotationTunnelStub()
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

    func testActivationRecoversCheckpointOnceUsingOriginalDeadlineAndFallbackResume() async {
        let clock = RotationClockStub()
        let checkpoint = CellularIPRotationCheckpoint(
            state: .awaitingAirplaneMode(
                attemptID: 41,
                originalNetworkToken: "cellular-1",
                holdSeconds: 10,
                before: PublicIPSnapshot(ipv4: "198.51.100.10")
            ),
            savedAt: clock.now.addingTimeInterval(-30),
            timeoutDeadline: clock.now.addingTimeInterval(90)
        )
        let store = RotationCheckpointStoreStub(checkpoint: checkpoint)
        let sleeper = RotationSleeperStub()
        let tunnel = await RotationTunnelStub()
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
        XCTAssertEqual(resumeReceipts, [nil])
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
    private var saved: [CellularIPRotationCheckpoint] = []
    private var loads = 0
    private var clears = 0
    private var retires = 0
    private var highestRetiredAttemptID: UInt64?

    init(
        checkpoint: CellularIPRotationCheckpoint? = nil,
        loadError: CellularIPRotationCheckpointStoreError? = nil,
        simulatesFailingPhysicalRetirement: Bool = false
    ) {
        self.checkpoint = checkpoint
        self.loadError = loadError
        self.simulatesFailingPhysicalRetirement = simulatesFailingPhysicalRetirement
    }

    var lastSaved: CellularIPRotationCheckpoint? { lock.withLock { saved.last } }
    var loadCount: Int { lock.withLock { loads } }
    var clearCount: Int { lock.withLock { clears } }
    var retireCount: Int { lock.withLock { retires } }

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
        lock.withLock {
            retires += 1
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
private final class RotationTunnelStub: CellularIPRotationTunnelControlling {
    struct RotationReceipt: Equatable, Sendable {
        let identifier: Int
        let wasRunning: Bool
        let wasOnDemandEnabled: Bool
    }

    private(set) var pauseCount = 0
    private(set) var resumeReceipts: [RotationReceipt?] = []
    private let failResume: Bool
    private var isRunning: Bool
    private var isOnDemandEnabled: Bool

    init(
        failResume: Bool = false,
        isRunning: Bool = true,
        isOnDemandEnabled: Bool = true
    ) {
        self.failResume = failResume
        self.isRunning = isRunning
        self.isOnDemandEnabled = isOnDemandEnabled
    }

    func pauseForRotation() async throws -> RotationReceipt {
        pauseCount += 1
        let receipt = RotationReceipt(
            identifier: pauseCount,
            wasRunning: isRunning,
            wasOnDemandEnabled: isOnDemandEnabled
        )
        isOnDemandEnabled = false
        isRunning = false
        return receipt
    }

    func resumeAfterRotation(_ receipt: RotationReceipt?) async throws {
        resumeReceipts.append(receipt)
        if let receipt {
            isRunning = receipt.wasRunning
            isOnDemandEnabled = receipt.wasOnDemandEnabled
        } else {
            isRunning = true
            isOnDemandEnabled = true
        }
        if failResume { throw RotationTestError.injected }
    }

    func intentSnapshot() -> (isRunning: Bool, isOnDemandEnabled: Bool) {
        (isRunning, isOnDemandEnabled)
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

    func pauseForRotation() async throws -> TunnelRotationReceipt {
        defer { pauseInvocationFinished = true }
        return try await TunnelRotationPreferenceTransaction.pause(using: self)
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
