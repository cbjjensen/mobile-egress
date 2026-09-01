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
        XCTAssertGreaterThanOrEqual(store.clearCount, 1)
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
        XCTAssertGreaterThanOrEqual(store.clearCount, 1)
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
    private var saved: [CellularIPRotationCheckpoint] = []
    private var loads = 0
    private var clears = 0

    init(
        checkpoint: CellularIPRotationCheckpoint? = nil,
        loadError: CellularIPRotationCheckpointStoreError? = nil
    ) {
        self.checkpoint = checkpoint
        self.loadError = loadError
    }

    var lastSaved: CellularIPRotationCheckpoint? { lock.withLock { saved.last } }
    var loadCount: Int { lock.withLock { loads } }
    var clearCount: Int { lock.withLock { clears } }

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
            return checkpoint
        }
    }

    func clear() throws {
        lock.withLock {
            clears += 1
            checkpoint = nil
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
    }

    private(set) var pauseCount = 0
    private(set) var resumeReceipts: [RotationReceipt?] = []
    private let failResume: Bool

    init(failResume: Bool = false) {
        self.failResume = failResume
    }

    func pauseForRotation() async throws -> RotationReceipt {
        pauseCount += 1
        return RotationReceipt(identifier: pauseCount)
    }

    func resumeAfterRotation(_ receipt: RotationReceipt?) async throws {
        resumeReceipts.append(receipt)
        if failResume { throw RotationTestError.injected }
    }
}

private enum RotationTestError: Error {
    case injected
}
