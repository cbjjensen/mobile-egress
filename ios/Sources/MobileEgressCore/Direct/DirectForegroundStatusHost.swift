import Foundation

public enum DirectCellularAvailability: String, Equatable, Sendable {
    case unknown = "Unknown"
    case available = "Available"
    case unavailable = "Unavailable"

    public init(observed: Bool?) {
        switch observed {
        case true: self = .available
        case false: self = .unavailable
        case nil: self = .unknown
        }
    }
    public var safeStatusLine: String { "Cellular: \(rawValue)" }
}

/// The main app and native tests use this same observer wiring. Cellular path
/// availability is independent of sharing intent and of each Client session.
@MainActor
public protocol DirectForegroundStatusHosting: DirectForegroundRotationHosting {
    var cellularAvailability: DirectCellularAvailability { get set }
    var rotationState: CellularIPRotationState { get set }
}

public extension DirectForegroundStatusHosting {
    func bindForegroundStatus(to coordinator: CellularIPRotationCoordinator<Self>) {
        coordinator.setStateChangeHandler { [weak self] state in
            self?.observeRotationState(state)
            self?.rotationState = state
        }
        coordinator.setCellularChangeHandler { [weak self] available in
            self?.cellularAvailability = DirectCellularAvailability(observed: available)
        }
    }
}
