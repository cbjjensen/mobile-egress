#if canImport(Network)
import Foundation
import Network
import XCTest
@testable import MobileEgressCore

final class CellularPathObserverTests: XCTestCase {
    func testObserverPublishesCellularAvailabilityAndCancellationDetachesCallbacks() {
        let monitor = CellularPathMonitorStub()
        let observer = CellularPathObserver(monitor: monitor)
        let values = ThreadSafeValues<Bool>()

        observer.start { values.append($0) }
        monitor.emit(true)
        observer.cancel()
        monitor.emit(false)

        XCTAssertEqual(values.snapshot, [true])
        XCTAssertEqual(monitor.startCount, 1)
        XCTAssertEqual(monitor.cancelCount, 1)
        XCTAssertNil(monitor.pathUpdateHandler)
    }

    func testConcurrentRepeatedCancellationIsIdempotentAndSuppressesLateUpdates() {
        let monitor = CellularPathMonitorStub()
        let observer = CellularPathObserver(monitor: monitor)
        let values = ThreadSafeValues<Bool>()
        observer.start { values.append($0) }
        monitor.emit(true)

        DispatchQueue.concurrentPerform(iterations: 32) { _ in
            observer.cancel()
        }
        monitor.emit(false)

        XCTAssertEqual(values.snapshot, [true])
        XCTAssertEqual(monitor.startCount, 1)
        XCTAssertEqual(monitor.cancelCount, 1)
        XCTAssertNil(monitor.pathUpdateHandler)
    }

    func testApplePathMonitorFactoryIsCellularSpecific() {
        let factory = AppleCellularPathMonitorFactory()

        XCTAssertEqual(factory.requiredInterfaceType, .cellular)
        _ = CellularPathObserver()
    }

    func testPublicAvailabilityHandlerIsSendable() {
        XCTAssertTrue(
            String(reflecting: CellularPathAvailabilityHandler.self).contains("@Sendable")
        )
    }
}

private final class CellularPathMonitorStub: CellularPathMonitoring, @unchecked Sendable {
    private let lock = NSLock()
    private var storedPathUpdateHandler: (@Sendable (Bool) -> Void)?
    private var storedStartCount = 0
    private var storedCancelCount = 0

    var pathUpdateHandler: (@Sendable (Bool) -> Void)? {
        get { lock.withLock { storedPathUpdateHandler } }
        set { lock.withLock { storedPathUpdateHandler = newValue } }
    }

    var startCount: Int {
        lock.withLock { storedStartCount }
    }

    var cancelCount: Int {
        lock.withLock { storedCancelCount }
    }

    func start(queue _: DispatchQueue) {
        lock.withLock { storedStartCount += 1 }
    }

    func cancel() {
        lock.withLock { storedCancelCount += 1 }
    }

    func emit(_ available: Bool) {
        pathUpdateHandler?(available)
    }
}

private final class ThreadSafeValues<Value: Sendable>: @unchecked Sendable {
    private let lock = NSLock()
    private var values: [Value] = []

    var snapshot: [Value] {
        lock.withLock { values }
    }

    func append(_ value: Value) {
        lock.withLock { values.append(value) }
    }
}
#endif
