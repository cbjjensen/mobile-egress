import XCTest
@testable import MobileEgressCore

final class TargetDialSequenceTests: XCTestCase {
    func testReadyAfterCandidateDeadlineMustMoveToAlternate() throws {
        var sequence = TargetDialSequence(candidateCount: 2, timeout: 30)
        _ = sequence.start(now: 100)
        XCTAssertFalse(sequence.ready(0, now: 103))
        XCTAssertEqual(sequence.failed(0, now: 103)?.index, 1)
    }

    func testFailedAttemptFallsBackImmediatelyAndLastRetainsOverallDeadline() throws {
        var sequence = TargetDialSequence(candidateCount: 3, timeout: 30)
        let first = try XCTUnwrap(sequence.start(now: 100))
        XCTAssertEqual(first.index, 0)
        XCTAssertEqual(first.timeout, 3)
        let second = try XCTUnwrap(sequence.failed(first.index, now: 101))
        XCTAssertEqual(second.index, 1)
        XCTAssertEqual(second.timeout, 3)
        let last = try XCTUnwrap(sequence.failed(second.index, now: 104))
        XCTAssertEqual(last.index, 2)
        XCTAssertEqual(last.timeout, 26)
        XCTAssertTrue(sequence.ready(last.index, now: 105))
        XCTAssertNil(sequence.failed(last.index, now: 106))
    }

    func testSingleAddressUsesExistingDeadlineAndTotalTimeoutNeverRestarts() throws {
        var single = TargetDialSequence(candidateCount: 1, timeout: 30)
        XCTAssertEqual(single.start(now: 100)?.timeout, 30)
        XCTAssertNil(single.failed(0, now: 101))
        var sequence = TargetDialSequence(candidateCount: 8, timeout: 5)
        XCTAssertEqual(sequence.start(now: 100)?.timeout, 3)
        XCTAssertEqual(sequence.failed(0, now: 103)?.timeout, 2)
        XCTAssertNil(sequence.failed(1, now: 105))
        XCTAssertFalse(sequence.ready(1, now: 106))
    }

    func testLateReadyAndFailuresCannotWinAfterFallbackOrCancellation() throws {
        var sequence = TargetDialSequence(candidateCount: 2, timeout: 30)
        _ = sequence.start(now: 100)
        _ = sequence.failed(0, now: 103)
        XCTAssertFalse(sequence.ready(0, now: 104))
        XCTAssertNil(sequence.failed(0, now: 104))
        sequence.cancel()
        XCTAssertFalse(sequence.ready(1, now: 104))
        XCTAssertNil(sequence.failed(1, now: 104))
        XCTAssertNil(sequence.start(now: 104))
    }

    func testReadyAtOverallDeadlineCannotOpenStream() {
        var sequence = TargetDialSequence(candidateCount: 2, timeout: 5)
        _ = sequence.start(now: 100)
        _ = sequence.failed(0, now: 103)
        XCTAssertFalse(sequence.ready(1, now: 105))
    }
}
