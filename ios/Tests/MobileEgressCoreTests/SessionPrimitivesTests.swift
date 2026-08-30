import Foundation
import XCTest
@testable import MobileEgressCore

final class SessionPrimitivesTests: XCTestCase {
    func testStreamAdmissionAllowsOnlyThirtyTwoUniqueStreams() {
        let admission = StreamAdmission(limit: 32)

        (0 ..< 32).forEach { XCTAssertTrue(admission.tryReserve("stream-\($0)")) }

        XCTAssertFalse(admission.tryReserve("stream-32"))
        XCTAssertFalse(admission.tryReserve("stream-0"))
        XCTAssertEqual(admission.count, 32)
    }

    func testOutboundMailboxSignalsRequiredControlSaturation() {
        let mailbox = OutboundMailbox(controlCapacity: 1, dataCapacity: 1, perStreamDataCapacity: 1)
        var saturated = false

        XCTAssertTrue(mailbox.offerRequiredControl(Data([1]), streamID: nil, onSaturated: { saturated = true }))
        XCTAssertFalse(mailbox.offerRequiredControl(Data([2]), streamID: nil, onSaturated: { saturated = true }))

        XCTAssertTrue(saturated)
    }

    func testOutboundMailboxEnforcesTotalAndPerStreamDataBounds() {
        let mailbox = OutboundMailbox(controlCapacity: 32, dataCapacity: 64, perStreamDataCapacity: 8)
        (0 ..< 9).forEach { mailbox.allowData("stream-\($0)") }

        (0 ..< 8).forEach { stream in
            (0 ..< 8).forEach { frame in
                XCTAssertTrue(mailbox.offerData(Data([UInt8(frame)]), streamID: "stream-\(stream)"))
            }
            XCTAssertFalse(mailbox.offerData(Data([0xFF]), streamID: "stream-\(stream)"))
        }

        XCTAssertFalse(mailbox.offerData(Data([0xAA]), streamID: "stream-8"))
        XCTAssertNotNil(mailbox.poll())
        XCTAssertTrue(mailbox.offerData(Data([0xAA]), streamID: "stream-8"))
    }

    func testOutboundMailboxPrioritizesEligibleControlsAndRoundRobinsData() throws {
        let mailbox = OutboundMailbox(controlCapacity: 32, dataCapacity: 64, perStreamDataCapacity: 8)
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

    func testOutboundMailboxDelaysGracefulControlUntilQueuedDataDrains() throws {
        let mailbox = OutboundMailbox(controlCapacity: 32, dataCapacity: 64, perStreamDataCapacity: 8)
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
        let mailbox = OutboundMailbox(controlCapacity: 32, dataCapacity: 64, perStreamDataCapacity: 8)
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

    func testTombstoneWindowRetainsOnlyThirtyTwoMostRecentEntries() {
        var tombstones = TombstoneWindow(limit: 32)
        (0 ..< 33).forEach { tombstones.insert("stream-\($0)") }

        XCTAssertFalse(tombstones.contains("stream-0"))
        XCTAssertTrue(tombstones.contains("stream-1"))
        XCTAssertTrue(tombstones.contains("stream-32"))
        XCTAssertEqual(tombstones.count, 32)
    }

    func testSessionCloseGateIsIdempotent() {
        let closeGate = SessionCloseGate()

        XCTAssertTrue(closeGate.tryClose())
        XCTAssertFalse(closeGate.tryClose())
        XCTAssertTrue(closeGate.isClosed)
    }
}
