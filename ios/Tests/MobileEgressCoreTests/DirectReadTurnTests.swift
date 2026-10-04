import Foundation
import XCTest
@testable import MobileEgressCore

final class DirectReadTurnTests: XCTestCase {
    func testReadyPeersTakeFairTurnsAndCancelledWaiterCannotBlockOthers() async throws {
        let gate = DirectReadTurns()
        let a = gate.makePeer(), b = gate.makePeer(), c = gate.makePeer()
        let firstValue = await gate.acquire(a)
        let first = try XCTUnwrap(firstValue)
        let a1 = Task { await gate.acquire(a) }
        await waitUntil { gate.waitingCount == 1 }
        let a2 = Task { await gate.acquire(a) }
        await waitUntil { gate.waitingCount == 2 }
        let b1 = Task { await gate.acquire(b) }
        await waitUntil { gate.waitingCount == 3 }
        let c1 = Task { await gate.acquire(c) }
        await waitUntil { gate.waitingCount == 4 }
        gate.cancel(c)
        let cancelled = await c1.value
        XCTAssertNil(cancelled)
        first.complete()
        let a1Value = await a1.value
        let turnA = try XCTUnwrap(a1Value)
        turnA.complete()
        let b1Value = await b1.value
        let turnB = try XCTUnwrap(b1Value)
        XCTAssertEqual(gate.waitingCount, 1, "a second busy-peer chunk cannot bypass another ready peer")
        turnB.complete()
        let a2Value = await a2.value
        let turnA2 = try XCTUnwrap(a2Value)
        turnA2.complete()
        XCTAssertEqual(gate.waitingCount, 0)
        let cancelledAgain = await gate.acquire(c)
        XCTAssertNil(cancelledAgain)
    }
    private func waitUntil(_ condition: () -> Bool) async {
        for _ in 0..<2000 { if condition() { return }; await Task.yield() }
        XCTFail("gate did not reach expected queue state")
    }
}
