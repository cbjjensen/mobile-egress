import XCTest
@testable import MobileEgressCore

final class TunnelPreferenceTransactionTests: XCTestCase {
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
private final class RecordingTunnelPreferenceSession: TunnelPreferenceSession {
    private(set) var operations: [RecordedPreferenceOperation] = []
    private let failure: RecordedPreferenceOperation?
    private let failureAtOperationIndex: Int?

    init(
        failure: RecordedPreferenceOperation? = nil,
        failureAtOperationIndex: Int? = nil
    ) {
        self.failure = failure
        self.failureAtOperationIndex = failureAtOperationIndex
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
