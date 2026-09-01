import Foundation
import XCTest
@testable import MobileEgressCore

final class SessionPrimitivesTests: XCTestCase {
    func testStreamAdmissionAllowsOnlyTwoHundredFiftySixUniqueStreams() {
        let admission = StreamAdmission(limit: 256)

        (0 ..< 256).forEach { XCTAssertTrue(admission.tryReserve("stream-\($0)")) }

        XCTAssertFalse(admission.tryReserve("stream-256"))
        XCTAssertFalse(admission.tryReserve("stream-0"))
        XCTAssertEqual(admission.count, 256)
    }

    func testOutboundMailboxSignalsRequiredControlSaturation() {
        let mailbox = OutboundMailbox(controlCapacity: 1, dataCapacity: 1, perStreamDataCapacity: 1)
        var saturated = false

        XCTAssertTrue(mailbox.offerRequiredControl(Data([1]), streamID: nil, onSaturated: { saturated = true }))
        XCTAssertFalse(mailbox.offerRequiredControl(Data([2]), streamID: nil, onSaturated: { saturated = true }))

        XCTAssertTrue(saturated)
    }

    func testOutboundMailboxEnforcesTotalAndPerStreamDataBounds() {
        let mailbox = OutboundMailbox(controlCapacity: 512, dataCapacity: 256, perStreamDataCapacity: 2)
        (0 ..< 129).forEach { mailbox.allowData("stream-\($0)") }

        (0 ..< 128).forEach { stream in
            (0 ..< 2).forEach { frame in
                XCTAssertTrue(mailbox.offerData(Data([UInt8(frame)]), streamID: "stream-\(stream)"))
            }
            XCTAssertFalse(mailbox.offerData(Data([0xFF]), streamID: "stream-\(stream)"))
        }

        XCTAssertFalse(mailbox.offerData(Data([0xAA]), streamID: "stream-128"))
        XCTAssertNotNil(mailbox.poll())
        XCTAssertTrue(mailbox.offerData(Data([0xAA]), streamID: "stream-128"))
    }

    func testOutboundMailboxPrioritizesEligibleControlsAndRoundRobinsData() throws {
        let mailbox = OutboundMailbox(controlCapacity: 512, dataCapacity: 256, perStreamDataCapacity: 2)
        mailbox.allowData("alpha")
        mailbox.allowData("beta")
        XCTAssertTrue(mailbox.offerData(Data("a1".utf8), streamID: "alpha"))
        XCTAssertTrue(mailbox.offerData(Data("a2".utf8), streamID: "alpha"))
        XCTAssertTrue(mailbox.offerData(Data("b1".utf8), streamID: "beta"))
        XCTAssertTrue(mailbox.offerData(Data("b2".utf8), streamID: "beta"))
        XCTAssertTrue(mailbox.offerRequiredControl(Data("control".utf8), streamID: nil, onSaturated: {}))

        let expected = ["control", "a1", "b1", "a2", "b2"]
        let actual = try expected.map { _ -> String in
            let frame = try XCTUnwrap(mailbox.poll())
            XCTAssertEqual(mailbox.emit(frame, sender: { _ in true }), .emitted)
            return try XCTUnwrap(String(data: frame.bytes, encoding: .utf8))
        }

        XCTAssertEqual(actual, expected)
    }

    func testOutboundMailboxGivesAllTwoHundredFiftySixReadyStreamsOneTurnBeforeASecondTurn() throws {
        let mailbox = OutboundMailbox(controlCapacity: 512, dataCapacity: 256, perStreamDataCapacity: 2)
        for index in 0 ..< 256 {
            let streamID = "stream-\(index)"
            mailbox.allowData(streamID)
            XCTAssertTrue(mailbox.offerData(Data("first-\(index)".utf8), streamID: streamID))
        }

        let first = try XCTUnwrap(mailbox.poll())
        XCTAssertEqual(String(data: first.bytes, encoding: .utf8), "first-0")
        XCTAssertEqual(mailbox.emit(first, sender: { _ in true }), .emitted)
        XCTAssertTrue(mailbox.offerData(Data("second-0".utf8), streamID: "stream-0"))

        let expected = (1 ..< 256).map { "first-\($0)" } + ["second-0"]
        let actual = try expected.map { _ -> String in
            let frame = try XCTUnwrap(mailbox.poll())
            XCTAssertEqual(mailbox.emit(frame, sender: { _ in true }), .emitted)
            return try XCTUnwrap(String(data: frame.bytes, encoding: .utf8))
        }

        XCTAssertEqual(actual, expected)
    }

    func testOutboundMailboxDelaysGracefulControlUntilQueuedDataDrains() throws {
        let mailbox = OutboundMailbox(controlCapacity: 512, dataCapacity: 256, perStreamDataCapacity: 2)
        mailbox.allowData("stream")
        XCTAssertTrue(mailbox.offerData(Data("one".utf8), streamID: "stream"))
        XCTAssertTrue(mailbox.offerData(Data("two".utf8), streamID: "stream"))
        XCTAssertTrue(mailbox.offerRequiredControlAfterData(
            Data("graceful-close".utf8),
            streamID: "stream",
            onSaturated: {}
        ))
        XCTAssertTrue(mailbox.offerRequiredControl(Data("pong".utf8), streamID: nil, onSaturated: {}))

        let expected = ["pong", "one", "two", "graceful-close"]
        let actual = try expected.map { _ -> String in
            let frame = try XCTUnwrap(mailbox.poll())
            XCTAssertEqual(mailbox.emit(frame, sender: { _ in true }), .emitted)
            return try XCTUnwrap(String(data: frame.bytes, encoding: .utf8))
        }

        XCTAssertEqual(actual, expected)
        XCTAssertFalse(mailbox.offerData(Data("late".utf8), streamID: "stream"))
    }

    func testOutboundMailboxForcedCloseDiscardsQueuedAndCancelsPolledData() throws {
        let mailbox = OutboundMailbox(controlCapacity: 512, dataCapacity: 256, perStreamDataCapacity: 2)
        mailbox.allowData("stream")
        XCTAssertTrue(mailbox.offerData(Data("in-flight".utf8), streamID: "stream"))
        XCTAssertTrue(mailbox.offerData(Data("queued".utf8), streamID: "stream"))
        let inFlight = try XCTUnwrap(mailbox.poll())

        mailbox.blockAndDiscardData(streamID: "stream")
        XCTAssertEqual(mailbox.emit(inFlight, sender: { _ in XCTFail("Canceled data must not reach the sender"); return true }), .canceled)
        XCTAssertTrue(mailbox.offerRequiredControl(Data("forced-close".utf8), streamID: "stream", onSaturated: {}))

        let terminal = try XCTUnwrap(mailbox.poll())
        XCTAssertEqual(terminal.bytes, Data("forced-close".utf8))
        XCTAssertNil(mailbox.poll())
    }

    func testOutboundMailboxCancellationTokenSurvivesStreamIDReuseWithoutCancelingNewData() throws {
        let mailbox = OutboundMailbox(controlCapacity: 512, dataCapacity: 256, perStreamDataCapacity: 2)
        mailbox.allowData("stream")
        XCTAssertTrue(mailbox.offerData(Data("old".utf8), streamID: "stream"))
        let oldFrame = try XCTUnwrap(mailbox.poll())

        XCTAssertTrue(mailbox.cancelStream("stream"))
        mailbox.allowData("stream")
        XCTAssertTrue(mailbox.offerData(Data("new".utf8), streamID: "stream"))

        XCTAssertEqual(
            mailbox.emit(oldFrame, sender: { _ in XCTFail("Canceled old generation must not emit"); return true }),
            .canceled
        )
        let newFrame = try XCTUnwrap(mailbox.poll())
        XCTAssertEqual(mailbox.emit(newFrame, sender: { $0 == Data("new".utf8) }), .emitted)
        XCTAssertNil(mailbox.poll())
    }

    func testOutboundMailboxCancellationReleasesDroppedAndInFlightBookkeeping() throws {
        let mailbox = OutboundMailbox(controlCapacity: 512, dataCapacity: 256, perStreamDataCapacity: 2)
        mailbox.allowData("stream")
        XCTAssertTrue(mailbox.offerData(Data("queued-1".utf8), streamID: "stream"))
        XCTAssertTrue(mailbox.offerData(Data("queued-2".utf8), streamID: "stream"))
        XCTAssertTrue(mailbox.offerRequiredControl(Data("control".utf8), streamID: "stream", onSaturated: {}))
        let inFlight = try XCTUnwrap(mailbox.poll())

        XCTAssertTrue(mailbox.cancelStream("stream"))
        XCTAssertEqual(
            mailbox.bookkeepingSnapshot,
            OutboundMailboxBookkeepingSnapshot(
                streamCancellationRecords: 1,
                dataCancellationRecords: 0,
                outstandingStreamFrames: 1,
                outstandingDataFrames: 0
            )
        )
        XCTAssertNil(mailbox.poll())

        XCTAssertEqual(mailbox.emit(inFlight, sender: { _ in false }), .canceled)
        XCTAssertEqual(mailbox.bookkeepingSnapshot, .empty)
    }

    func testOutboundMailboxRetainsOnlyNewestOneThousandTwentyFourCancellationEntries() {
        let mailbox = OutboundMailbox(controlCapacity: 512, dataCapacity: 256, perStreamDataCapacity: 2)

        (0 ... 1_024).forEach { mailbox.cancelStream("stream-\($0)") }

        XCTAssertEqual(
            mailbox.retentionSnapshot,
            OutboundMailboxRetentionSnapshot(
                blockedDataStreams: 1_024,
                canceledDataStreams: 1_024,
                canceledStreams: 1_024
            )
        )
        mailbox.allowData("stream-0")
        XCTAssertEqual(mailbox.retentionSnapshot.blockedDataStreams, 1_024)
        mailbox.allowData("stream-1024")
        XCTAssertEqual(
            mailbox.retentionSnapshot,
            OutboundMailboxRetentionSnapshot(
                blockedDataStreams: 1_023,
                canceledDataStreams: 1_023,
                canceledStreams: 1_023
            )
        )
    }

    func testOutboundMailboxDuplicateCancellationRefreshesAllHistoryRecency() {
        let mailbox = OutboundMailbox(controlCapacity: 512, dataCapacity: 256, perStreamDataCapacity: 2)
        (0 ..< 1_024).forEach { mailbox.cancelStream("stream-\($0)") }

        mailbox.cancelStream("stream-0")
        mailbox.cancelStream("stream-1024")
        mailbox.allowData("stream-0")
        XCTAssertEqual(
            mailbox.retentionSnapshot,
            OutboundMailboxRetentionSnapshot(
                blockedDataStreams: 1_023,
                canceledDataStreams: 1_023,
                canceledStreams: 1_023
            )
        )

        mailbox.allowData("stream-1")
        XCTAssertEqual(
            mailbox.retentionSnapshot,
            OutboundMailboxRetentionSnapshot(
                blockedDataStreams: 1_023,
                canceledDataStreams: 1_023,
                canceledStreams: 1_023
            )
        )
    }

    func testTombstoneWindowRetainsOnlyNewestOneThousandTwentyFourEntries() {
        var tombstones = TombstoneWindow(limit: 1_024)
        (0 ... 1_024).forEach { tombstones.insert("stream-\($0)") }

        XCTAssertFalse(tombstones.contains("stream-0"))
        XCTAssertTrue(tombstones.contains("stream-1"))
        XCTAssertTrue(tombstones.contains("stream-1024"))
        XCTAssertEqual(tombstones.count, 1_024)
    }

    func testTombstoneDuplicateRefreshesRecencyBeforeEviction() {
        var tombstones = TombstoneWindow(limit: 1_024)
        (0 ..< 1_024).forEach { tombstones.insert("stream-\($0)") }

        tombstones.insert("stream-0")
        tombstones.insert("stream-1024")

        XCTAssertTrue(tombstones.contains("stream-0"))
        XCTAssertFalse(tombstones.contains("stream-1"))
        XCTAssertTrue(tombstones.contains("stream-1024"))
        XCTAssertEqual(tombstones.count, 1_024)
    }

    func testBoundedDequeRejectsOverflowAndPreservesFIFOAcrossWraparound() {
        let deque = BoundedDeque<Int>(capacity: 3)

        XCTAssertTrue(deque.isEmpty)
        XCTAssertNil(deque.popFirst())
        XCTAssertTrue(deque.append(1))
        XCTAssertTrue(deque.append(2))
        XCTAssertTrue(deque.append(3))
        XCTAssertTrue(deque.isFull)
        XCTAssertFalse(deque.append(4))
        XCTAssertEqual(deque.popFirst(), 1)
        XCTAssertTrue(deque.append(4))
        XCTAssertEqual(deque.popFirst(), 2)
        XCTAssertEqual(deque.popFirst(), 3)
        XCTAssertEqual(deque.popFirst(), 4)
        XCTAssertNil(deque.popFirst())
        XCTAssertTrue(deque.isEmpty)
    }

    func testBoundedDequeUsesFixedBackingCapacityThroughRepeatedWrapCycles() {
        let deque = BoundedDeque<Int>(capacity: 4)

        for cycle in 0 ..< 1_000 {
            for offset in 0 ..< 4 {
                XCTAssertTrue(deque.append(cycle * 4 + offset))
            }
            XCTAssertFalse(deque.append(-1))
            for offset in 0 ..< 4 {
                XCTAssertEqual(deque.popFirst(), cycle * 4 + offset)
            }
            XCTAssertEqual(deque.backingCapacity, 4)
            XCTAssertEqual(deque.occupiedSlotCount, 0)
        }
    }

    func testBoundedDequeClearsPoppedReferences() {
        final class Reference {}
        weak var weakReference: Reference?
        var popped: Reference?
        let deque = BoundedDeque<Reference>(capacity: 2)

        do {
            let reference = Reference()
            weakReference = reference
            XCTAssertTrue(deque.append(reference))
        }
        XCTAssertNotNil(weakReference)

        popped = deque.popFirst()
        XCTAssertEqual(deque.occupiedSlotCount, 0)
        popped = nil

        XCTAssertNil(weakReference)
    }

    func testBoundedDequeBoundedRemovalClearsDiscardedReferencesAndPreservesOrder() {
        final class Reference {
            let id: Int

            init(id: Int) {
                self.id = id
            }
        }
        weak var removedReference: Reference?
        var removed: Reference? = Reference(id: 2)
        let deque = BoundedDeque<Reference>(capacity: 4)
        XCTAssertTrue(deque.append(Reference(id: 1)))
        removedReference = removed
        XCTAssertTrue(deque.append(removed!))
        XCTAssertTrue(deque.append(Reference(id: 3)))
        removed = nil

        deque.removeAll { $0.id == 2 }

        XCTAssertNil(removedReference)
        XCTAssertEqual(deque.occupiedSlotCount, 2)
        XCTAssertEqual(deque.popFirst()?.id, 1)
        XCTAssertEqual(deque.popFirst()?.id, 3)
        XCTAssertNil(deque.popFirst())
    }

    func testSessionCloseGateIsIdempotent() {
        let closeGate = SessionCloseGate()

        XCTAssertTrue(closeGate.tryClose())
        XCTAssertFalse(closeGate.tryClose())
        XCTAssertTrue(closeGate.isClosed)
    }
}
