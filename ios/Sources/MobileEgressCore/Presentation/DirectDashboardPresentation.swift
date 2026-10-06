import Foundation

/// Sanitized, presentation-only input. No identities, keys, invitations or addresses.
public enum DirectDashboardConnection: Equatable, Sendable {
    case waiting, connecting, authenticated, recovering(DirectPeerRecovery)
}
public enum DashboardTone: Equatable, Sendable { case neutral, mint, blue, warning }
public struct DirectDashboardClient: Identifiable, Equatable, Sendable {
    public let id: String
    public var name: String
    public var transport: ClientTransport
    public var enabled = true
    public var paired = true
    public var needsAcknowledgement = false
    public var removing = false
    public var connection: DirectDashboardConnection
    public var streams = 0
    public var uploaded: UInt64 = 0
    public var downloaded: UInt64 = 0
    public var actionError: String?
    public init(id: String, name: String, transport: ClientTransport, connection: DirectDashboardConnection = .waiting) {
        self.id = id; self.name = name; self.transport = transport; self.connection = connection
    }
}
public struct DirectDashboardClientPresentation: Identifiable, Equatable, Sendable {
    public let client: DirectDashboardClient
    public var id: String { client.id }
    public let status: String
    public let tone: DashboardTone
    public let guidance: String?
    public let canRetry: Bool
    public let canEnable: Bool
    public let connected: Bool
}
public struct DirectDashboardPresentation: Equatable, Sendable {
    public enum State: Equatable, Sendable { case stopped, paused, connecting, connected, partial, attention }
    public let state: State
    public let clients: [DirectDashboardClientPresentation]
    public let connectedCount: Int
    public let streams: Int
    public let uploaded: UInt64
    public let downloaded: UInt64
    public let canToggleSharing: Bool
    public let sharingRequested: Bool
    public let keepAwake: Bool
    public let preventsAutoLock: Bool
    public let cellular: DirectCellularAvailability
    public var isEmpty: Bool { clients.isEmpty }
    public var tone: DashboardTone {
        switch state { case .connected: .mint; case .connecting: .blue; case .partial, .attention: .warning; default: .neutral }
    }
    public var badge: String {
        switch state {
        case .stopped: "Stopped"
        case .paused: "Paused"
        case .connecting: "Connecting"
        case .connected: "Connected"
        case .partial: "Partially connected"
        case .attention: "Needs attention"
        }
    }
    public var headline: String {
        if isEmpty { return "Pair your first computer" }
        switch state {
        case .stopped: return "Ready when you are"
        case .paused: return "Sharing is paused"
        case .connecting: return "Connecting your computers"
        case .connected: return "You're connected"
        case .partial: return "Some computers are connected"
        case .attention: return "Let's get you connected"
        }
    }
    public var explanation: String {
        if isEmpty { return "Connect a computer to use this iPhone's mobile data in your apps." }
        switch state {
        case .stopped: return "Start sharing to connect your enabled computers."
        case .paused: return "Keep this app open. Sharing resumes when your iPhone is ready."
        case .connecting: return "Keep this app open while we connect over cellular."
        case .connected: return "Your computers can use this iPhone's mobile data."
        case .partial: return "Sharing continues. Check the other computers below."
        case .attention: return cellular == .unavailable ? "Turn on cellular data to continue sharing." : "Check the message on each computer below."
        }
    }
    public init(clients input: [DirectDashboardClient], lifecycle: DirectSharingLifecycle, cellular: DirectCellularAvailability, busy: Bool = false, runtimeAvailable: Bool = true) {
        sharingRequested = lifecycle.startIntent; keepAwake = lifecycle.keepAwake
        preventsAutoLock = lifecycle.idleTimerDisabled; self.cellular = cellular
        canToggleSharing = lifecycle.startIntent || (!busy && !input.isEmpty && lifecycle.sceneActive && runtimeAvailable)
        // An observed loss overrides the last polled session immediately. Unknown
        // cellular observations do not override an authenticated cellular-only socket.
        let serving = lifecycle.shouldServe && cellular != .unavailable
        clients = input.map { client in
            let connected = serving && client.enabled && client.paired && !client.removing && client.connection == .authenticated
            let status: String
            let tone: DashboardTone
            var guidance: String?
            if client.removing {
                status = "Removal pending"; tone = .warning
                guidance = "This computer is stopped. Open details and retry Remove to finish."
            } else if !client.enabled { status = "Disabled"; tone = .neutral }
            else if case let .recovering(recovery) = client.connection {
                status = recovery.automaticallyRetries ? "Retrying connection" : "Needs attention"
                tone = .warning; guidance = recovery.guidance
            } else if !client.paired || client.needsAcknowledgement {
                status = client.needsAcknowledgement ? "Finish pairing" : "Pairing pending"; tone = .warning
                guidance = "Keep the Client app open on your computer, then tap Retry pairing."
            } else if !lifecycle.startIntent { status = "Ready"; tone = .neutral }
            else if !lifecycle.shouldServe { status = "Paused"; tone = .neutral }
            else if cellular == .unavailable { status = "Waiting for cellular"; tone = .warning }
            else if connected { status = "Connected"; tone = .mint }
            else { status = "Connecting"; tone = .blue }
            if let error = client.actionError { guidance = error }
            return .init(client: client, status: status, tone: tone, guidance: guidance,
                         canRetry: !busy && lifecycle.sceneActive && client.enabled && !client.removing,
                         canEnable: !busy && !client.removing && lifecycle.sceneActive, connected: connected)
        }
        connectedCount = clients.filter(\.connected).count
        let eligible = input.filter { $0.enabled && !$0.removing }
        if !lifecycle.startIntent { state = .stopped }
        else if !lifecycle.shouldServe { state = .paused }
        else if cellular == .unavailable || eligible.isEmpty { state = .attention }
        else if connectedCount == eligible.count { state = .connected }
        else if connectedCount > 0 { state = .partial }
        else if clients.contains(where: { $0.tone == .warning }) { state = .attention }
        else { state = .connecting }
        // These are retained runtime counters, not a persistent usage ledger.
        let active = serving ? eligible : []
        streams = active.reduce(0) { saturatedSum($0, max(0, $1.streams)) }
        uploaded = active.reduce(0) { saturatedSum($0, $1.uploaded) }
        downloaded = active.reduce(0) { saturatedSum($0, $1.downloaded) }
    }
}
private func saturatedSum<T: FixedWidthInteger>(_ lhs: T, _ rhs: T) -> T {
    let (sum, overflow) = lhs.addingReportingOverflow(rhs)
    return overflow ? .max : sum
}

/// A single presentation attempt may receive repeated camera/file callbacks.
/// Reset only for an explicit new attempt; never store or log the accepted code.
public struct ClientCodeDelivery {
    private var delivered = false
    public init() {}
    public mutating func take(_ code: String) -> String? {
        let trimmed = code.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !delivered, !trimmed.isEmpty else { return nil }
        delivered = true
        return trimmed
    }
}

public enum ClientCodeImportRoute: Equatable, Sendable {
    case pair, update(expectedClientID: String?)
}
/// The sheet determines intent; a valid signed code must not silently redirect
/// an action for one computer to a different saved computer or a new pairing.
public enum ClientCodeImportDestination: Equatable, Sendable {
    case addClient, connectionUpdate(clientID: String?)
    public var clientID: String? {
        if case let .connectionUpdate(id) = self { return id }; return nil
    }
    public func route(isInvitation: Bool) throws -> ClientCodeImportRoute {
        switch self {
        case .addClient: return isInvitation ? .pair : .update(expectedClientID: nil)
        case let .connectionUpdate(id):
            guard !isInvitation else { throw DirectAgentError.invalidBundle }
            return .update(expectedClientID: id)
        }
    }
}
