import XCTest
@testable import MobileEgressCore

final class TunnelPreferenceTransactionTests: XCTestCase {
    func testRotationIntentCaptureIsReadOnlyAndPausePersistsDisabledOnDemandBeforeStopping() async throws {
        let session = await RecordingTunnelPreferenceSession(
            isRotationTunnelRunning: true,
            isOnDemandEnabled: true
        )

        let receipt = try await TunnelRotationPreferenceTransaction.captureIntent(using: session)

        let intentAfterCapture = await session.intentSnapshot()
        let captureOperations = await session.operations
        XCTAssertEqual(intentAfterCapture.isRunning, true)
        XCTAssertEqual(intentAfterCapture.isOnDemandEnabled, true)
        XCTAssertEqual(captureOperations, [.load])

        try await TunnelRotationPreferenceTransaction.pause(using: session, receipt: receipt)

        let operations = await session.operations
        XCTAssertEqual(operations, [
            .load,
            .apply(onDemandEnabled: false),
            .save,
            .load,
            .stop,
        ])
    }

    func testRotationResumeRestoresPriorOnDemandAndRestartsOnlyPriorRunningIntent() async throws {
        let running = await RecordingTunnelPreferenceSession(
            isRotationTunnelRunning: true,
            isOnDemandEnabled: false
        )
        let stopped = await RecordingTunnelPreferenceSession(
            isRotationTunnelRunning: false,
            isOnDemandEnabled: true
        )
        let runningReceipt = try await TunnelRotationPreferenceTransaction.captureIntent(using: running)
        let stoppedReceipt = try await TunnelRotationPreferenceTransaction.captureIntent(using: stopped)
        try await TunnelRotationPreferenceTransaction.pause(using: running, receipt: runningReceipt)
        try await TunnelRotationPreferenceTransaction.pause(using: stopped, receipt: stoppedReceipt)
        await running.resetOperations()
        await stopped.resetOperations()

        try await TunnelRotationPreferenceTransaction.resume(using: running, receipt: runningReceipt)
        try await TunnelRotationPreferenceTransaction.resume(using: stopped, receipt: stoppedReceipt)

        let runningOperations = await running.operations
        let stoppedOperations = await stopped.operations
        XCTAssertEqual(runningOperations, [
            .load,
            .apply(onDemandEnabled: false),
            .save,
            .load,
            .start,
        ])
        XCTAssertEqual(stoppedOperations, [
            .load,
            .apply(onDemandEnabled: true),
            .save,
            .load,
        ])
    }

    func testRotationPauseFailureRestoresOnDemandIntentWithoutStopping() async {
        let session = await RecordingTunnelPreferenceSession(
            failure: .save,
            isRotationTunnelRunning: true,
            isOnDemandEnabled: true
        )

        do {
            let receipt = try await TunnelRotationPreferenceTransaction.captureIntent(using: session)
            try await TunnelRotationPreferenceTransaction.pause(using: session, receipt: receipt)
            XCTFail("Expected preference save to fail")
        } catch {
            XCTAssertEqual(error as? RecordingPreferenceError, .injected)
        }

        let operations = await session.operations
        XCTAssertEqual(operations, [
            .load,
            .apply(onDemandEnabled: false),
            .save,
            .apply(onDemandEnabled: true),
            .save,
            .load,
        ])
    }

    func testStartLoadsBeforeMutationAndReloadsBeforeSubmittingSessionStart() async throws {
        let session = await RecordingTunnelPreferenceSession()

        try await TunnelPreferenceTransaction.start(using: session)

        let operations = await session.operations
        XCTAssertEqual(operations, [
            .load,
            .apply(onDemandEnabled: true),
            .save,
            .load,
            .start,
        ])
    }

    func testStartRollsBackOnDemandWhenPostSaveReloadFails() async {
        let session = await RecordingTunnelPreferenceSession(failureAtOperationIndex: 3)

        do {
            try await TunnelPreferenceTransaction.start(using: session)
            XCTFail("Expected the post-save preference reload to fail")
        } catch {
            XCTAssertEqual(error as? RecordingPreferenceError, .injected)
        }

        let operations = await session.operations
        XCTAssertEqual(operations, [
            .load,
            .apply(onDemandEnabled: true),
            .save,
            .load,
            .apply(onDemandEnabled: false),
            .save,
            .load,
        ])
    }

    func testStartRollsBackOnDemandWhenTunnelSubmissionFails() async {
        let session = await RecordingTunnelPreferenceSession(failureAtOperationIndex: 4)

        do {
            try await TunnelPreferenceTransaction.start(using: session)
            XCTFail("Expected the tunnel start submission to fail")
        } catch {
            XCTAssertEqual(error as? RecordingPreferenceError, .injected)
        }

        let operations = await session.operations
        XCTAssertEqual(operations, [
            .load,
            .apply(onDemandEnabled: true),
            .save,
            .load,
            .start,
            .apply(onDemandEnabled: false),
            .save,
            .load,
        ])
    }

    func testSuccessfulStopPersistsDisabledOnDemandBeforeStoppingSession() async throws {
        let session = await RecordingTunnelPreferenceSession()

        try await TunnelPreferenceTransaction.stop(using: session)

        let operations = await session.operations
        XCTAssertEqual(operations, [
            .load,
            .apply(onDemandEnabled: false),
            .save,
            .load,
            .stop,
        ])
    }

    func testStopStillStopsSessionWhenPreferenceLoadOrSaveFails() async {
        let cases: [(RecordedPreferenceOperation, [RecordedPreferenceOperation])] = [
            (.load, [.load, .stop]),
            (.save, [.load, .apply(onDemandEnabled: false), .save, .stop]),
        ]

        for (failure, expectedOperations) in cases {
            let session = await RecordingTunnelPreferenceSession(failure: failure)
            do {
                try await TunnelPreferenceTransaction.stop(using: session)
                XCTFail("Expected preference transaction to fail at \(failure)")
            } catch {
                XCTAssertEqual(error as? RecordingPreferenceError, .injected)
            }
            let operations = await session.operations
            XCTAssertEqual(operations, expectedOperations)
        }
    }
}

private enum RecordedPreferenceOperation: Equatable, Sendable {
    case load
    case apply(onDemandEnabled: Bool)
    case save
    case start
    case stop
}

private enum RecordingPreferenceError: Error, Equatable {
    case injected
}

@MainActor
private final class RecordingTunnelPreferenceSession: TunnelRotationPreferenceSession {
    private(set) var operations: [RecordedPreferenceOperation] = []
    private let failure: RecordedPreferenceOperation?
    private let failureAtOperationIndex: Int?
    let isRotationTunnelRunning: Bool
    let isOnDemandEnabled: Bool

    init(
        failure: RecordedPreferenceOperation? = nil,
        failureAtOperationIndex: Int? = nil,
        isRotationTunnelRunning: Bool = false,
        isOnDemandEnabled: Bool = false
    ) {
        self.failure = failure
        self.failureAtOperationIndex = failureAtOperationIndex
        self.isRotationTunnelRunning = isRotationTunnelRunning
        self.isOnDemandEnabled = isOnDemandEnabled
    }

    func resetOperations() {
        operations = []
    }

    func intentSnapshot() -> (isRunning: Bool, isOnDemandEnabled: Bool) {
        (isRotationTunnelRunning, isOnDemandEnabled)
    }

    func loadPreferences() async throws {
        try record(.load)
    }

    func applyConfiguration(onDemandEnabled: Bool) {
        operations.append(.apply(onDemandEnabled: onDemandEnabled))
    }

    func savePreferences() async throws {
        try record(.save)
    }

    func startTunnelSession() throws {
        try record(.start)
    }

    func stopTunnelSession() {
        operations.append(.stop)
    }

    private func record(_ operation: RecordedPreferenceOperation) throws {
        operations.append(operation)
        if operation == failure || operations.count - 1 == failureAtOperationIndex {
            throw RecordingPreferenceError.injected
        }
    }
}
