import Foundation
import XCTest
@testable import MobileEgressCore

final class CurrentCellularIPControllerTests: XCTestCase {
    @MainActor func testChecksBeforeSharingAndCoalescesRefresh() async {
        let probe = CurrentIPProbeGate()
        let controller = CurrentCellularIPController(probe: probe)
        controller.cellularChanged(available: true)
        XCTAssertFalse(controller.state.checking)
        controller.setActive(true)
        await wait { await probe.count == 1 }
        XCTAssertTrue(controller.state.checking)
        controller.refresh()
        await probe.complete(0, with: .init(ipv4: "198.51.100.10", ipv6: "2001:db8::1"))
        await wait { !controller.state.checking }
        XCTAssertEqual(controller.state.snapshot, .init(ipv4: "198.51.100.10", ipv6: "2001:db8::1"))
        let calls = await probe.count
        XCTAssertEqual(calls, 1)
        XCTAssertTrue(controller.state.canRefresh)
    }

    @MainActor func testPathLossAndReturnRejectLateOldResult() async {
        let probe = CurrentIPProbeGate()
        let controller = CurrentCellularIPController(probe: probe)
        controller.setActive(true); controller.cellularChanged(available: true)
        await wait { await probe.count == 1 }
        controller.cellularChanged(available: false)
        XCTAssertEqual(controller.state.snapshot, .init())
        XCTAssertFalse(controller.state.canRefresh)
        controller.cellularChanged(available: true)
        await wait { await probe.count == 2 }
        await probe.complete(1, with: .init(ipv6: "2001:db8::2"))
        await wait { !controller.state.checking }
        await probe.complete(0, with: .init(ipv4: "198.51.100.10"))
        for _ in 0..<20 { await Task.yield() }
        XCTAssertEqual(controller.state.snapshot, .init(ipv6: "2001:db8::2"))
    }

    @MainActor func testRotationAndForegroundSuspensionInvalidatePendingWork() async {
        let probe = CurrentIPProbeGate()
        let controller = CurrentCellularIPController(probe: probe)
        controller.setActive(true); controller.cellularChanged(available: true)
        await wait { await probe.count == 1 }
        controller.setRotating(true)
        await probe.complete(0, with: .init(ipv4: "198.51.100.10"))
        for _ in 0..<20 { await Task.yield() }
        XCTAssertEqual(controller.state.snapshot, .init())
        XCTAssertFalse(controller.state.canRefresh)
        controller.setRotating(false)
        await wait { await probe.count == 2 }
        controller.setActive(false)
        await probe.complete(1, with: .init(ipv4: "198.51.100.20"))
        for _ in 0..<20 { await Task.yield() }
        XCTAssertEqual(controller.state.snapshot, .init())
        XCTAssertFalse(controller.state.canRefresh)
        controller.setActive(true)
        await wait { await probe.count == 3 }
        await probe.complete(2, with: .init())
        await wait { !controller.state.checking }
        XCTAssertEqual(controller.state.snapshot, .init())
        XCTAssertTrue(controller.state.canRefresh)
    }

    @MainActor func testRefreshClearsPreviousAddressAndHandlesPartialProbe() async {
        let probe = CurrentIPProbeGate()
        let controller = CurrentCellularIPController(probe: probe)
        controller.setActive(true); controller.cellularChanged(available: true)
        await wait { await probe.count == 1 }
        await probe.complete(0, with: .init(ipv4: "198.51.100.10"))
        await wait { !controller.state.checking }
        controller.refresh()
        XCTAssertEqual(controller.state.snapshot, .init())
        await wait { await probe.count == 2 }
        await probe.complete(1, with: .init(ipv6: "2001:db8::1"))
        await wait { !controller.state.checking }
        XCTAssertEqual(controller.state.snapshot, .init(ipv6: "2001:db8::1"))
    }

    @MainActor private func wait(_ predicate: () async -> Bool) async {
        for _ in 0..<2_000 { if await predicate() { return }; await Task.yield() }
        XCTFail("Timed out waiting for current IP controller")
    }
}

private actor CurrentIPProbeGate: CellularPublicIPProbing {
    private var pending: [CheckedContinuation<PublicIPSnapshot, Never>?] = []
    var count: Int { pending.count }
    func probe() async -> PublicIPSnapshot {
        await withCheckedContinuation { pending.append($0) }
    }
    func complete(_ index: Int, with snapshot: PublicIPSnapshot) {
        pending[index]?.resume(returning: snapshot); pending[index] = nil
    }
}
