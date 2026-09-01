import Foundation

public typealias CellularPathAvailabilityHandler = @Sendable (Bool) -> Void

public protocol CellularPathObserving: Sendable {
    func start(handler: @escaping CellularPathAvailabilityHandler)
    func cancel()
}

#if canImport(Network)
import Network

protocol CellularPathMonitoring: AnyObject, Sendable {
    var pathUpdateHandler: (@Sendable (Bool) -> Void)? { get set }
    func start(queue: DispatchQueue)
    func cancel()
}

struct AppleCellularPathMonitorFactory {
    let requiredInterfaceType: NWInterface.InterfaceType = .cellular

    func makeMonitor() -> any CellularPathMonitoring {
        NWCellularPathMonitor(requiredInterfaceType: requiredInterfaceType)
    }
}

public final class CellularPathObserver: CellularPathObserving, @unchecked Sendable {
    private let monitor: any CellularPathMonitoring
    private let lock = NSRecursiveLock()
    private let monitorQueue = DispatchQueue(label: "com.mobileegress.agent.cellular-path-monitor")
    private var handler: CellularPathAvailabilityHandler?
    private var active = false
    private var started = false
    private var cancelled = false

    public convenience init() {
        self.init(monitor: AppleCellularPathMonitorFactory().makeMonitor())
    }

    init(monitor: any CellularPathMonitoring) {
        self.monitor = monitor
    }

    public func start(handler: @escaping CellularPathAvailabilityHandler) {
        lock.lock()
        defer { lock.unlock() }
        guard !started, !cancelled else { return }
        started = true
        active = true
        self.handler = handler
        monitor.pathUpdateHandler = { [weak self] available in
            guard let self else { return }
            self.lock.lock()
            defer { self.lock.unlock() }
            guard self.active else { return }
            self.handler?(available)
        }
        monitor.start(queue: monitorQueue)
    }

    public func cancel() {
        lock.lock()
        defer { lock.unlock() }
        guard !cancelled else { return }
        cancelled = true
        active = false
        handler = nil
        monitor.pathUpdateHandler = nil
        monitor.cancel()
    }
}

private final class NWCellularPathMonitor: CellularPathMonitoring, @unchecked Sendable {
    private let lock = NSLock()
    private var storedPathUpdateHandler: (@Sendable (Bool) -> Void)?
    private let monitor: NWPathMonitor

    var pathUpdateHandler: (@Sendable (Bool) -> Void)? {
        get { lock.withLock { storedPathUpdateHandler } }
        set { lock.withLock { storedPathUpdateHandler = newValue } }
    }

    init(requiredInterfaceType: NWInterface.InterfaceType) {
        monitor = NWPathMonitor(requiredInterfaceType: requiredInterfaceType)
    }

    func start(queue: DispatchQueue) {
        monitor.pathUpdateHandler = { [weak self] path in
            self?.deliver(
                path.status == .satisfied && path.usesInterfaceType(.cellular)
            )
        }
        monitor.start(queue: queue)
    }

    func cancel() {
        monitor.pathUpdateHandler = nil
        pathUpdateHandler = nil
        monitor.cancel()
    }

    private func deliver(_ available: Bool) {
        let handler = lock.withLock { storedPathUpdateHandler }
        handler?(available)
    }
}
#endif
