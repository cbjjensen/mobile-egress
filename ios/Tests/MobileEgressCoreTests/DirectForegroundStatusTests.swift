import Foundation
import XCTest
@testable import MobileEgressCore

final class DirectForegroundStatusTests: XCTestCase {
    @MainActor
    func testAppObserverReportsUnknownUntilObservedAndKeepsCellularSeparateFromSharing() async {
        let host = ForegroundStatusHost()
        let path = StatusPathObserver()
        let coordinator = CellularIPRotationCoordinator(probe: StatusProbe(), pathObserver: path,
            checkpointStore: StatusCheckpointStore(), notificationCue: StatusCue(), tunnel: host)
        host.bindForegroundStatus(to: coordinator)
        XCTAssertEqual(host.cellularAvailability, .unknown)
        XCTAssertEqual(host.cellularAvailability.safeStatusLine, "Cellular: Unknown")
        await coordinator.resumeAfterActivation()
        XCTAssertEqual(host.cellularAvailability, .unknown, "activation itself is not a path observation")
        path.emit(true)
        for _ in 0..<100 where host.cellularAvailability != .available { await Task.yield() }
        XCTAssertEqual(host.cellularAvailability.safeStatusLine, "Cellular: Available")
        XCTAssertFalse(host.lifecycle.startIntent, "an available path must not start sharing")
        host.lifecycle.sceneActive = true; host.lifecycle.migrationReady = true
        try? host.lifecycle.start()
        path.emit(false)
        for _ in 0..<100 where host.cellularAvailability != .unavailable { await Task.yield() }
        XCTAssertEqual(host.cellularAvailability.safeStatusLine, "Cellular: Unavailable")
        XCTAssertTrue(host.lifecycle.startIntent, "loss preserves the owner's Start intent")
        host.lifecycle.stop()
        path.emit(true)
        for _ in 0..<100 where host.cellularAvailability != .available { await Task.yield() }
        XCTAssertEqual(host.cellularAvailability, .available)
        XCTAssertFalse(host.lifecycle.shouldServe)
    }
}

@MainActor private final class ForegroundStatusHost: DirectForegroundStatusHosting {
    var lifecycle = DirectSharingLifecycle()
    var cellularAvailability = DirectCellularAvailability.unknown
    var rotationState = CellularIPRotationState.idle
    func reconcileDirectSharing() async {}
}
private final class StatusPathObserver: CellularPathObserving, @unchecked Sendable {
    private let lock = NSLock()
    private var handler: CellularPathAvailabilityHandler?
    func start(handler: @escaping CellularPathAvailabilityHandler) { lock.withLock { self.handler = handler } }
    func cancel() { lock.withLock { handler = nil } }
    func emit(_ available: Bool) { lock.withLock { handler }?(available) }
}
private struct StatusProbe: CellularPublicIPProbing {
    func probe() async -> PublicIPSnapshot { PublicIPSnapshot() }
}
private struct StatusCheckpointStore: CellularIPRotationCheckpointStoring {
    func save(_ checkpoint: CellularIPRotationCheckpoint) throws {}
    func load(at date: Date) throws -> CellularIPRotationCheckpoint? { nil }
    func clear() throws {}
}
private struct StatusCue: CellularIPRotationNotificationCueing {
    func schedule(attemptID: UInt64, holdDeadline: Date) async -> CellularIPRotationNotificationCueResult { .denied }
    func cancel(attemptID: UInt64) async {}
}
