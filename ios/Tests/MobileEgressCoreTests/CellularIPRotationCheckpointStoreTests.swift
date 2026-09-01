import Foundation
import XCTest
@testable import MobileEgressCore

final class CellularIPRotationCheckpointStoreTests: XCTestCase {
    private var containerURL: URL!

    override func setUpWithError() throws {
        containerURL = FileManager.default.temporaryDirectory
            .appendingPathComponent("mobile-egress-checkpoint-\(UUID().uuidString)", isDirectory: true)
        try FileManager.default.createDirectory(at: containerURL, withIntermediateDirectories: true)
    }

    override func tearDownWithError() throws {
        try FileManager.default.removeItem(at: containerURL)
        containerURL = nil
    }

    func testCheckpointStoreAtomicallyReplacesAndClearsOnlyValidActiveData() throws {
        let store = AppGroupCellularIPRotationCheckpointStore(containerURL: containerURL)
        let first = CellularIPRotationCheckpoint(
            state: .awaitingAirplaneMode(
                attemptID: 41,
                originalNetworkToken: "temporary-token-one",
                holdSeconds: 10,
                before: PublicIPSnapshot(ipv4: "198.51.100.1")
            ),
            savedAt: now,
            timeoutDeadline: now.addingTimeInterval(120)
        )
        let replacement = CellularIPRotationCheckpoint(
            state: .holding(
                attemptID: 42,
                remainingSeconds: 30,
                before: PublicIPSnapshot(ipv6: "2001:db8::2"),
                returnedNetworkToken: nil
            ),
            savedAt: now.addingTimeInterval(1)
        )

        try store.save(first)
        try store.save(replacement)

        XCTAssertEqual(try store.load(at: now.addingTimeInterval(2)), replacement)

        XCTAssertEqual(
            try store.load(expectedAttemptID: 42, at: now.addingTimeInterval(2)),
            replacement
        )
        XCTAssertThrowsError(
            try store.load(expectedAttemptID: 41, at: now.addingTimeInterval(2))
        ) { error in
            XCTAssertEqual(error as? CellularIPRotationCheckpointStoreError, .attemptMismatch)
        }

        try store.clear()
        XCTAssertNil(try store.load(expectedAttemptID: 42, at: now.addingTimeInterval(2)))
    }

    func testCheckpointStoreRejectsAndRemovesExpiredDataAtFiveMinutes() throws {
        let store = AppGroupCellularIPRotationCheckpointStore(containerURL: containerURL)
        let checkpoint = CellularIPRotationCheckpoint(
            state: .holding(
                attemptID: 41,
                remainingSeconds: 10,
                before: PublicIPSnapshot(ipv4: "198.51.100.1"),
                returnedNetworkToken: nil
            ),
            savedAt: now
        )
        try store.save(checkpoint)

        XCTAssertThrowsError(
            try store.load(expectedAttemptID: 41, at: now.addingTimeInterval(300))
        ) { error in
            XCTAssertEqual(error as? CellularIPRotationCheckpointStoreError, .expired)
        }
        XCTAssertNil(try store.load(expectedAttemptID: 41, at: now.addingTimeInterval(301)))
    }

    func testCheckpointStoreRejectsMalformedAndLegacyMissingTimingData() throws {
        let store = AppGroupCellularIPRotationCheckpointStore(containerURL: containerURL)
        try Data("not-json temporary-token 198.51.100.3".utf8).write(
            to: store.checkpointURL,
            options: .atomic
        )

        XCTAssertThrowsError(try store.load(expectedAttemptID: 41, at: now)) { error in
            XCTAssertEqual(error as? CellularIPRotationCheckpointStoreError, .malformed)
        }

        let missingTiming = CellularIPRotationCheckpoint(
            state: .awaitingCellularReturn(
                attemptID: 41,
                before: PublicIPSnapshot(ipv4: "198.51.100.3")
            ),
            savedAt: now
        )
        try JSONEncoder().encode(missingTiming).write(to: store.checkpointURL, options: .atomic)

        XCTAssertThrowsError(try store.load(expectedAttemptID: 41, at: now)) { error in
            XCTAssertEqual(error as? CellularIPRotationCheckpointStoreError, .missingRequiredTiming)
        }
    }

    func testCheckpointStoreRejectsTerminalStateAndUnavailableAppGroupContainer() throws {
        let store = AppGroupCellularIPRotationCheckpointStore(containerURL: containerURL)
        let terminal = CellularIPRotationCheckpoint(
            state: .completed(
                attemptID: 41,
                before: PublicIPSnapshot(),
                after: PublicIPSnapshot(),
                result: .unverified
            ),
            savedAt: now
        )

        XCTAssertThrowsError(try store.save(terminal)) { error in
            XCTAssertEqual(error as? CellularIPRotationCheckpointStoreError, .inactiveState)
        }
        XCTAssertThrowsError(
            try AppGroupCellularIPRotationCheckpointStore(
                appGroupIdentifier: "group.example.unavailable",
                containerResolver: { _ in nil }
            )
        ) { error in
            XCTAssertEqual(error as? CellularIPRotationCheckpointStoreError, .containerUnavailable)
        }
    }

    private var now: Date {
        Date(timeIntervalSince1970: 2_100_000_000)
    }
}
