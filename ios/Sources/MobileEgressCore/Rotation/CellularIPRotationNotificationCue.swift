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
    case cancelled
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
    private struct SchedulingAttempt {
        let generation: UInt64
        var cancelled: Bool
    }

    private let center: any CellularIPRotationNotificationCenter
    private let firstUseStore: any CellularIPRotationNotificationFirstUseStoring
    private var nextGeneration: UInt64 = 0
    private var schedulingAttempts: [UInt64: SchedulingAttempt] = [:]

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
        nextGeneration &+= 1
        let generation = nextGeneration
        schedulingAttempts[attemptID] = SchedulingAttempt(
            generation: generation,
            cancelled: false
        )
        defer { finishScheduling(attemptID: attemptID, generation: generation) }

        let authorization: CellularIPRotationNotificationAuthorization
        if !firstUseStore.hasRequestedAuthorization {
            firstUseStore.hasRequestedAuthorization = true
            let current = await center.authorizationStatus()
            guard isScheduling(attemptID: attemptID, generation: generation) else {
                return .cancelled
            }
            if current == .notDetermined {
                do {
                    authorization = try await center.requestAuthorization() ? .authorized : .denied
                } catch {
                    guard isScheduling(attemptID: attemptID, generation: generation) else {
                        return .cancelled
                    }
                    return .authorizationFailed
                }
                guard isScheduling(attemptID: attemptID, generation: generation) else {
                    return .cancelled
                }
            } else {
                authorization = current
            }
        } else {
            authorization = await center.authorizationStatus()
            guard isScheduling(attemptID: attemptID, generation: generation) else {
                return .cancelled
            }
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
                guard isScheduling(attemptID: attemptID, generation: generation) else {
                    await center.removePendingRequests(
                        withIdentifiers: [Self.pendingIdentifier(for: attemptID)]
                    )
                    return .cancelled
                }
                return .scheduled
            } catch {
                guard isScheduling(attemptID: attemptID, generation: generation) else {
                    return .cancelled
                }
                return .schedulingFailed
            }
        }
    }

    public func cancel(attemptID: UInt64) async {
        if var attempt = schedulingAttempts[attemptID] {
            attempt.cancelled = true
            schedulingAttempts[attemptID] = attempt
        }
        await center.removePendingRequests(
            withIdentifiers: [Self.pendingIdentifier(for: attemptID)]
        )
    }

    private func isScheduling(attemptID: UInt64, generation: UInt64) -> Bool {
        guard let attempt = schedulingAttempts[attemptID] else { return false }
        return attempt.generation == generation && !attempt.cancelled
    }

    private func finishScheduling(attemptID: UInt64, generation: UInt64) {
        guard schedulingAttempts[attemptID]?.generation == generation else { return }
        schedulingAttempts[attemptID] = nil
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
    private let centerProvider: @Sendable () -> UNUserNotificationCenter

    public init() {
        centerProvider = { UNUserNotificationCenter.current() }
    }

    public func authorizationStatus() async -> CellularIPRotationNotificationAuthorization {
        let settings = await centerProvider().notificationSettings()
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
        try await centerProvider().requestAuthorization(options: [.alert, .sound])
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
        try await centerProvider().add(notification)
    }

    public func removePendingRequests(withIdentifiers identifiers: [String]) async {
        centerProvider().removePendingNotificationRequests(withIdentifiers: identifiers)
    }
}
#endif
