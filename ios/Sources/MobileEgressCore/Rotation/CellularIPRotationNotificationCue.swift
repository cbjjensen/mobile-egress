import Foundation

public enum CellularIPRotationNotificationAuthorization: String, Codable, Equatable, Sendable {
    case notDetermined
    case denied
    case authorized
}

public struct CellularIPRotationNotificationRequest: Equatable, Sendable {
    public let identifier: String
    public let title: String
    public let body: String
    public let deliveryDate: Date

    public init(identifier: String, title: String, body: String, deliveryDate: Date) {
        self.identifier = identifier
        self.title = title
        self.body = body
        self.deliveryDate = deliveryDate
    }
}

public enum CellularIPRotationNotificationCueResult: String, Codable, Equatable, Sendable {
    case scheduled
    case denied
    case authorizationFailed
    case schedulingFailed
}

public protocol CellularIPRotationNotificationCenter: Sendable {
    func authorizationStatus() async -> CellularIPRotationNotificationAuthorization
    func requestAuthorization() async throws -> Bool
    func schedule(_ request: CellularIPRotationNotificationRequest) async throws
    func removePendingRequests(withIdentifiers identifiers: [String]) async
}

public protocol CellularIPRotationNotificationFirstUseStoring: AnyObject, Sendable {
    var hasRequestedAuthorization: Bool { get set }
}

public protocol CellularIPRotationNotificationCueing: Sendable {
    func schedule(
        attemptID: UInt64,
        holdDeadline: Date
    ) async -> CellularIPRotationNotificationCueResult
    func cancel(attemptID: UInt64) async
}

public actor CellularIPRotationNotificationCue: CellularIPRotationNotificationCueing {
    private let center: any CellularIPRotationNotificationCenter
    private let firstUseStore: any CellularIPRotationNotificationFirstUseStoring

    public init(
        center: any CellularIPRotationNotificationCenter,
        firstUseStore: any CellularIPRotationNotificationFirstUseStoring
    ) {
        self.center = center
        self.firstUseStore = firstUseStore
    }

    public func schedule(
        attemptID: UInt64,
        holdDeadline: Date
    ) async -> CellularIPRotationNotificationCueResult {
        let authorization: CellularIPRotationNotificationAuthorization
        if !firstUseStore.hasRequestedAuthorization {
            firstUseStore.hasRequestedAuthorization = true
            let current = await center.authorizationStatus()
            if current == .notDetermined {
                do {
                    authorization = try await center.requestAuthorization() ? .authorized : .denied
                } catch {
                    return .authorizationFailed
                }
            } else {
                authorization = current
            }
        } else {
            authorization = await center.authorizationStatus()
        }

        switch authorization {
        case .denied:
            return .denied
        case .notDetermined:
            return .authorizationFailed
        case .authorized:
            do {
                try await center.schedule(
                    CellularIPRotationNotificationRequest(
                        identifier: Self.pendingIdentifier(for: attemptID),
                        title: MobileEgressBranding.displayName,
                        body: "Turn Airplane Mode off",
                        deliveryDate: holdDeadline
                    )
                )
                return .scheduled
            } catch {
                return .schedulingFailed
            }
        }
    }

    public func cancel(attemptID: UInt64) async {
        await center.removePendingRequests(
            withIdentifiers: [Self.pendingIdentifier(for: attemptID)]
        )
    }

    private static func pendingIdentifier(for attemptID: UInt64) -> String {
        "com.mobileegress.agent.rotation.\(attemptID)"
    }
}

public final class UserDefaultsNotificationFirstUseStore:
    CellularIPRotationNotificationFirstUseStoring,
    @unchecked Sendable
{
    private static let key = "cellularIPRotation.notificationAuthorizationRequested"
    private let lock = NSLock()
    private let defaults: UserDefaults

    public init(defaults: UserDefaults) {
        self.defaults = defaults
    }

    public var hasRequestedAuthorization: Bool {
        get {
            lock.lock()
            defer { lock.unlock() }
            return defaults.bool(forKey: Self.key)
        }
        set {
            lock.lock()
            defer { lock.unlock() }
            defaults.set(newValue, forKey: Self.key)
        }
    }
}

#if canImport(UserNotifications)
import UserNotifications

public struct AppleCellularIPRotationNotificationCenter:
    CellularIPRotationNotificationCenter,
    @unchecked Sendable
{
    private let center: UNUserNotificationCenter

    public init(center: UNUserNotificationCenter = .current()) {
        self.center = center
    }

    public func authorizationStatus() async -> CellularIPRotationNotificationAuthorization {
        let settings = await center.notificationSettings()
        switch settings.authorizationStatus {
        case .notDetermined:
            return .notDetermined
        case .denied:
            return .denied
        case .authorized, .provisional, .ephemeral:
            return .authorized
        @unknown default:
            return .denied
        }
    }

    public func requestAuthorization() async throws -> Bool {
        try await center.requestAuthorization(options: [.alert, .sound])
    }

    public func schedule(_ request: CellularIPRotationNotificationRequest) async throws {
        let content = UNMutableNotificationContent()
        content.title = request.title
        content.body = request.body
        content.sound = .default
        let components = Calendar.current.dateComponents(
            [.calendar, .timeZone, .year, .month, .day, .hour, .minute, .second],
            from: request.deliveryDate
        )
        let notification = UNNotificationRequest(
            identifier: request.identifier,
            content: content,
            trigger: UNCalendarNotificationTrigger(dateMatching: components, repeats: false)
        )
        try await center.add(notification)
    }

    public func removePendingRequests(withIdentifiers identifiers: [String]) async {
        center.removePendingNotificationRequests(withIdentifiers: identifiers)
    }
}
#endif
