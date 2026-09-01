#if canImport(Network)
import Foundation
import Network
import XCTest
@testable import MobileEgressCore

final class CellularPathObserverTests: XCTestCase {
    func testObserverPublishesCellularAvailabilityAndCancellationDetachesCallbacks() {
        let monitor = CellularPathMonitorStub()
        let observer = CellularPathObserver(monitor: monitor)
        var values: [Bool] = []

        observer.start { values.append($0) }
        monitor.emit(true)
        observer.cancel()
        monitor.emit(false)

        XCTAssertEqual(values, [true])
        XCTAssertEqual(monitor.startCount, 1)
        XCTAssertEqual(monitor.cancelCount, 1)
        XCTAssertNil(monitor.pathUpdateHandler)
    }

    func testApplePathMonitorFactoryIsCellularSpecific() {
        let factory = AppleCellularPathMonitorFactory()

        XCTAssertEqual(factory.requiredInterfaceType, .cellular)
        _ = CellularPathObserver()
    }
}

private final class CellularPathMonitorStub: CellularPathMonitoring, @unchecked Sendable {
    var pathUpdateHandler: (@Sendable (Bool) -> Void)?
    private(set) var startCount = 0
    private(set) var cancelCount = 0

    func start(queue _: DispatchQueue) {
        startCount += 1
    }

    func cancel() {
        cancelCount += 1
    }

    func emit(_ available: Bool) {
        pathUpdateHandler?(available)
    }
}
#endif
