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
