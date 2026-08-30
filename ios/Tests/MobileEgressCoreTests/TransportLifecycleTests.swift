import Foundation
import XCTest
@testable import MobileEgressCore

final class TransportLifecycleTests: XCTestCase {
    func testSuspendedDeliveryBlocksAnotherReceiveAndCompletionResumesExactlyOnce() throws {
        var gate = ReceiveDeliveryGate()
        let generation = gate.beginGeneration()

        XCTAssertTrue(gate.beginReceive(generation))
        let delivery = try XCTUnwrap(gate.completeReceive(generation, resumeReceiving: true))

        XCTAssertFalse(gate.beginReceive(generation))
        XCTAssertTrue(gate.completeDelivery(delivery))
        XCTAssertFalse(gate.completeDelivery(delivery))
        XCTAssertTrue(gate.beginReceive(generation))
        XCTAssertFalse(gate.beginReceive(generation))
    }

    func testCancelAndGenerationChangePreventStaleDeliveryFromResumingReceive() throws {
        var gate = ReceiveDeliveryGate()
        let staleGeneration = gate.beginGeneration()
        XCTAssertTrue(gate.beginReceive(staleGeneration))
        let staleDelivery = try XCTUnwrap(gate.completeReceive(staleGeneration, resumeReceiving: true))

        gate.invalidate(staleGeneration)
        let currentGeneration = gate.beginGeneration()

        XCTAssertFalse(gate.beginReceive(currentGeneration))
        XCTAssertFalse(gate.completeDelivery(staleDelivery))
        XCTAssertTrue(gate.beginReceive(currentGeneration))

        gate.invalidate(currentGeneration)
        XCTAssertNil(gate.completeReceive(currentGeneration, resumeReceiving: true))
    }

    func testStoppingReadsWhileDeliveryIsOutstandingPreventsReceiveResumption() throws {
        var gate = ReceiveDeliveryGate()
        let generation = gate.beginGeneration()
        XCTAssertTrue(gate.beginReceive(generation))
        let delivery = try XCTUnwrap(gate.completeReceive(generation, resumeReceiving: true))

        gate.stopReceiving(generation)

        XCTAssertFalse(gate.completeDelivery(delivery))
        XCTAssertFalse(gate.beginReceive(generation))
    }

    func testTargetReadEOFOrdersDataBeforeEndedAndKeepsWritableHalfOpenUntilCancel() {
        var lifecycle = TargetDuplexLifecycle()

        XCTAssertFalse(lifecycle.canRead)
        XCTAssertFalse(lifecycle.canWrite)
        XCTAssertTrue(lifecycle.markReady())
        XCTAssertTrue(lifecycle.canRead)
        XCTAssertTrue(lifecycle.canWrite)

        XCTAssertEqual(
            lifecycle.receive(content: Data([0x41, 0x42]), isComplete: true),
            [.data(Data([0x41, 0x42])), .ended]
        )
        XCTAssertFalse(lifecycle.canRead)
        XCTAssertTrue(lifecycle.canWrite)
        XCTAssertTrue(lifecycle.receive(content: Data([0x43]), isComplete: false).isEmpty)

        XCTAssertTrue(lifecycle.cancel())
        XCTAssertFalse(lifecycle.canRead)
        XCTAssertFalse(lifecycle.canWrite)
        XCTAssertFalse(lifecycle.cancel())
    }
}
