import Foundation

public typealias CellularPathAvailabilityHandler = (Bool) -> Void

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
    private let monitorQueue = DispatchQueue(label: "com.mobileegress.agent.cellular-path-monitor")
    private let deliveryQueue = DispatchQueue(label: "com.mobileegress.agent.cellular-path-delivery")
    private let deliveryQueueKey = DispatchSpecificKey<UInt8>()
    private var handler: CellularPathAvailabilityHandler?
    private var active = false
    private var started = false

    public convenience init() {
        self.init(monitor: AppleCellularPathMonitorFactory().makeMonitor())
    }

    init(monitor: any CellularPathMonitoring) {
        self.monitor = monitor
        deliveryQueue.setSpecific(key: deliveryQueueKey, value: 1)
    }

    public func start(handler: @escaping CellularPathAvailabilityHandler) {
        deliveryQueue.sync {
            guard !started else { return }
            started = true
            active = true
            self.handler = handler
        }
        monitor.pathUpdateHandler = { [weak self] available in
            self?.deliveryQueue.async { [weak self] in
                guard let self, self.active else { return }
                self.handler?(available)
            }
        }
        monitor.start(queue: monitorQueue)
    }

    public func cancel() {
        let detach = {
            self.active = false
            self.handler = nil
        }
        if DispatchQueue.getSpecific(key: deliveryQueueKey) != nil {
            detach()
        } else {
            deliveryQueue.sync(execute: detach)
        }
        monitor.pathUpdateHandler = nil
        monitor.cancel()
    }
}

private final class NWCellularPathMonitor: CellularPathMonitoring, @unchecked Sendable {
    var pathUpdateHandler: (@Sendable (Bool) -> Void)?
    private let monitor: NWPathMonitor

    init(requiredInterfaceType: NWInterface.InterfaceType) {
        monitor = NWPathMonitor(requiredInterfaceType: requiredInterfaceType)
    }

    func start(queue: DispatchQueue) {
        monitor.pathUpdateHandler = { [weak self] path in
            self?.pathUpdateHandler?(
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
}
#endif
