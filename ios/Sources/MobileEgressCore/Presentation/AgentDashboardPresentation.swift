import Foundation

public enum MobileEgressBranding {
    public static let displayName = "ZFNF Mobile Egress"
    public static let agentName = "ZFNF Mobile Egress Agent"
    public static let headerTitle = "ZFNF MOBILE EGRESS"
    public static let statusClipboardLabel = "ZFNF Mobile Egress status"
}

public enum AgentOperationalState: String, Codable, Equatable, Sendable {
    case stopped
    case starting
    case running
    case stopping
    case failed
}

public enum CellularHealth: String, Codable, Equatable, Sendable {
    case unavailable
    case available
}

public enum RelayHealth: String, Codable, Equatable, Sendable {
    case disconnected
    case connecting
    case connected
}

public enum AgentStatusErrorClass: String, Codable, Equatable, Hashable, Sendable {
    case none
    case cellularUnavailable
    case relayTLS
    case relayAuthentication
    case relayUnavailable
    case protocolViolation
    case targetPolicy
    case targetConnect
    case backpressure
    case credential
    case internalFailure

    fileprivate var safeName: String {
        switch self {
        case .none: "none"
        case .cellularUnavailable: "cellular unavailable"
        case .relayTLS: "relay tls"
        case .relayAuthentication: "relay authentication"
        case .relayUnavailable: "relay unavailable"
        case .protocolViolation: "protocol"
        case .targetPolicy: "target policy"
        case .targetConnect: "target connect"
        case .backpressure: "backpressure"
        case .credential: "credential"
        case .internalFailure: "internal"
        }
    }

    fileprivate var presentationCopy: String? {
        switch self {
        case .none:
            nil
        case .cellularUnavailable:
            "Cellular data is unavailable."
        case .relayTLS:
            "The secure relay connection failed TLS validation."
        case .relayAuthentication:
            "The relay rejected this Agent identity."
        case .relayUnavailable:
            "The secure relay is temporarily unavailable."
        case .protocolViolation:
            "The relay session ended because of a protocol error."
        case .targetPolicy:
            "A requested target was blocked by the public-address policy."
        case .targetConnect:
            "A target connection could not be opened."
        case .backpressure:
            "The Agent stopped a stream to protect bounded buffers."
        case .credential:
            "The Agent identity is unavailable. Pair this phone again."
        case .internalFailure:
            "The Agent stopped because of an internal error."
        }
    }
}

public struct AgentStatusSnapshot: Codable, Equatable, Sendable {
    public static let idle = AgentStatusSnapshot(
        agentState: .stopped,
        cellular: .unavailable,
        relay: .disconnected,
        activeStreamCount: 0,
        bytesUploaded: 0,
        bytesDownloaded: 0,
        errorClass: .none,
        rotation: .idle
    )

    public let agentState: AgentOperationalState
    public let cellular: CellularHealth
    public let relay: RelayHealth
    public let activeStreamCount: Int
    public let bytesUploaded: UInt64
    public let bytesDownloaded: UInt64
    public let errorClass: AgentStatusErrorClass
    public let rotation: CellularIPRotationState

    public init(
        agentState: AgentOperationalState,
        cellular: CellularHealth,
        relay: RelayHealth,
        activeStreamCount: Int,
        bytesUploaded: UInt64,
        bytesDownloaded: UInt64,
        errorClass: AgentStatusErrorClass,
        rotation: CellularIPRotationState
    ) {
        self.agentState = agentState
        self.cellular = cellular
        self.relay = relay
        self.activeStreamCount = max(0, activeStreamCount)
        self.bytesUploaded = bytesUploaded
        self.bytesDownloaded = bytesDownloaded
        self.errorClass = errorClass
        self.rotation = rotation
    }

    public func safeCopiedStatus(isEnrolled: Bool) -> String {
        [
            MobileEgressBranding.agentName,
            "Enrolled: \(isEnrolled ? "yes" : "no")",
            "Agent: \(agentState.rawValue)",
            "Cellular: \(cellular.rawValue)",
            "Relay: \(relay.rawValue)",
            "Active streams: \(activeStreamCount)",
            "Bytes uploaded: \(bytesUploaded)",
            "Bytes downloaded: \(bytesDownloaded)",
            "Error class: \(errorClass.safeName)",
            "IP rotation: \(rotation.safeDiagnosticName)",
        ].joined(separator: "\n")
    }
}

public enum AgentPairingState: String, Codable, Equatable, Sendable {
    case idle
    case paired
    case cameraPermissionRequired
    case scannerUnavailable
    case qrNotRecognized
    case failed
}

public struct AgentDashboardState: Codable, Equatable, Sendable {
    public let isEnrolled: Bool
    public let pairingInProgress: Bool
    public let pairingState: AgentPairingState
    public let status: AgentStatusSnapshot

    public init(
        isEnrolled: Bool,
        pairingInProgress: Bool = false,
        pairingState: AgentPairingState = .idle,
        status: AgentStatusSnapshot = .idle
    ) {
        self.isEnrolled = isEnrolled
        self.pairingInProgress = pairingInProgress
        self.pairingState = pairingState
        self.status = status
    }
}

public enum AgentDashboardTone: String, Codable, Equatable, Sendable {
    case neutral
    case accent
    case info
    case success
    case warning
    case error
}

public enum AgentPrimaryAction: String, Codable, Equatable, Sendable {
    case none
    case start
    case stop
}

public enum CellularIPRotationAction: String, Codable, Equatable, Sendable {
    case none
    case rotate
    case retry
}

public struct CellularIPRotationConfirmationPresentation: Codable, Equatable, Sendable {
    public let title: String
    public let message: String
    public let confirmLabel: String
    public let declineLabel: String

    public init(title: String, message: String, confirmLabel: String, declineLabel: String) {
        self.title = title
        self.message = message
        self.confirmLabel = confirmLabel
        self.declineLabel = declineLabel
    }
}

public struct AgentHealthPresentation: Codable, Equatable, Sendable {
    public let label: String
    public let value: String
    public let tone: AgentDashboardTone

    public init(label: String, value: String, tone: AgentDashboardTone) {
        self.label = label
        self.value = value
        self.tone = tone
    }
}

public struct AgentMetricPresentation: Codable, Equatable, Sendable {
    public let label: String
    public let value: String

    public init(label: String, value: String) {
        self.label = label
        self.value = value
    }
}

public struct AgentDashboardPresentation: Codable, Equatable, Sendable {
    public let appTitle: String
    public let headline: String
    public let summary: String
    public let badge: String
    public let tone: AgentDashboardTone
    public let pairingTone: AgentDashboardTone
    public let scanLabel: String
    public let isScanEnabled: Bool
    public let primaryAgentAction: AgentPrimaryAction
    public let inactiveAgentMessage: String
    public let rotationAction: CellularIPRotationAction
    public let rotationLabel: String
    public let isRotationEnabled: Bool
    public let requiresActiveStreamConfirmation: Bool
    public let rotationConfirmation: CellularIPRotationConfirmationPresentation?
    public let cellularHealth: AgentHealthPresentation
    public let relayHealth: AgentHealthPresentation
    public let metrics: [AgentMetricPresentation]
    public let finiteErrorCopy: String?
    public let safeStatusText: String

    public static func present(_ state: AgentDashboardState) -> Self {
        let statusPresentation = statusPresentation(for: state)
        let availability = CellularIPRotationAvailability(
            isEnrolled: state.isEnrolled,
            isAgentRunning: state.status.agentState == .running,
            isCellularAvailable: state.status.cellular == .available,
            activeStreamCount: state.status.activeStreamCount
        )
        return Self(
            appTitle: MobileEgressBranding.headerTitle,
            headline: statusPresentation.headline,
            summary: statusPresentation.summary,
            badge: statusPresentation.badge,
            tone: statusPresentation.tone,
            pairingTone: resolvePairingTone(for: state),
            scanLabel: state.pairingInProgress ? "Pairing…" : "Scan QR",
            isScanEnabled: !state.pairingInProgress && state.status.agentState == .stopped,
            primaryAgentAction: primaryAction(for: state),
            inactiveAgentMessage: resolveInactiveAgentMessage(for: state),
            rotationAction: resolveRotationAction(for: state),
            rotationLabel: resolveRotationLabel(for: state.status.rotation),
            isRotationEnabled: availability.isEligible(for: state.status.rotation),
            requiresActiveStreamConfirmation: requiresRotationConfirmation(
                availability: availability,
                rotation: state.status.rotation
            ),
            rotationConfirmation: rotationConfirmation(for: state.status.rotation),
            cellularHealth: cellularPresentation(state.status.cellular),
            relayHealth: relayPresentation(state.status.relay),
            metrics: [
                AgentMetricPresentation(
                    label: "Active streams",
                    value: String(state.status.activeStreamCount)
                ),
                AgentMetricPresentation(
                    label: "Uploaded",
                    value: formatMobileEgressByteCount(state.status.bytesUploaded)
                ),
                AgentMetricPresentation(
                    label: "Downloaded",
                    value: formatMobileEgressByteCount(state.status.bytesDownloaded)
                ),
            ],
            finiteErrorCopy: state.status.errorClass.presentationCopy,
            safeStatusText: state.status.safeCopiedStatus(isEnrolled: state.isEnrolled)
        )
    }
}

public func formatMobileEgressByteCount(_ bytes: UInt64) -> String {
    switch bytes {
    case 0 ..< 1_024:
        "\(bytes) B"
    case 1_024 ..< 1_024 * 1_024:
        String(
            format: "%.1f KB",
            locale: Locale(identifier: "en_US_POSIX"),
            Double(bytes) / 1_024
        )
    case 1_024 * 1_024 ..< 1_024 * 1_024 * 1_024:
        String(
            format: "%.1f MB",
            locale: Locale(identifier: "en_US_POSIX"),
            Double(bytes) / (1_024 * 1_024)
        )
    default:
        String(
            format: "%.1f GB",
            locale: Locale(identifier: "en_US_POSIX"),
            Double(bytes) / (1_024 * 1_024 * 1_024)
        )
    }
}

private struct DashboardStatusPresentation {
    let headline: String
    let summary: String
    let badge: String
    let tone: AgentDashboardTone
}

private func statusPresentation(for state: AgentDashboardState) -> DashboardStatusPresentation {
    if state.pairingInProgress {
        return DashboardStatusPresentation(
            headline: "Pairing phone",
            summary: "Creating a secure cellular identity for this device.",
            badge: "Pairing",
            tone: .accent
        )
    }
    if !state.isEnrolled {
        return DashboardStatusPresentation(
            headline: "Ready to pair",
            summary: "Scan the QR from your controller to link this phone.",
            badge: "Phone setup",
            tone: .accent
        )
    }
    switch state.status.agentState {
    case .stopped:
        return DashboardStatusPresentation(
            headline: "Ready to connect",
            summary: "Your phone is paired. Start the Agent when cellular data is available.",
            badge: "Paired",
            tone: .success
        )
    case .starting:
        return DashboardStatusPresentation(
            headline: "Starting Agent",
            summary: "Preparing the cellular relay and secure connection.",
            badge: "Starting",
            tone: .info
        )
    case .stopping:
        return DashboardStatusPresentation(
            headline: "Stopping Agent",
            summary: "Closing proxy streams and the secure relay connection.",
            badge: "Stopping",
            tone: .info
        )
    case .failed:
        return DashboardStatusPresentation(
            headline: "Agent needs attention",
            summary: "The Agent stopped after an error. Review the details below.",
            badge: "Agent error",
            tone: .error
        )
    case .running:
        break
    }
    if let rotation = rotationPresentation(state.status.rotation) {
        return rotation
    }
    if state.status.cellular == .unavailable {
        return DashboardStatusPresentation(
            headline: "Waiting for cellular",
            summary: "Wi-Fi will not be used as a fallback for proxied traffic.",
            badge: "Connecting",
            tone: .warning
        )
    }
    if state.status.relay != .connected && blockingRelayErrors.contains(state.status.errorClass) {
        return DashboardStatusPresentation(
            headline: "Connection needs attention",
            summary: "The secure relay session could not connect. Review the Agent details below.",
            badge: "Connection issue",
            tone: .error
        )
    }
    if state.status.relay != .connected {
        return DashboardStatusPresentation(
            headline: "Connecting to relay",
            summary: "Cellular is ready while the secure relay session comes online.",
            badge: "Connecting",
            tone: .warning
        )
    }
    return DashboardStatusPresentation(
        headline: "Cellular relay active",
        summary: "Paired workloads can now use this phone's cellular connection.",
        badge: "Connected",
        tone: .success
    )
}

private func rotationPresentation(
    _ rotation: CellularIPRotationState
) -> DashboardStatusPresentation? {
    switch rotation {
    case .idle:
        nil
    case .awaitingConfirmation:
        DashboardStatusPresentation(
            headline: "Confirm IP rotation",
            summary: "Active proxy streams will be disconnected before rotation starts.",
            badge: "Confirmation",
            tone: .warning
        )
    case .preparing:
        DashboardStatusPresentation(
            headline: "Preparing IP rotation",
            summary: "Disconnecting proxy streams and checking the current cellular address.",
            badge: "Preparing",
            tone: .info
        )
    case .awaitingAirplaneMode:
        DashboardStatusPresentation(
            headline: "Turn Airplane Mode on",
            summary: "Open Control Center, turn Airplane Mode on, and keep it on until the cue.",
            badge: "Your action",
            tone: .warning
        )
    case .holding:
        DashboardStatusPresentation(
            headline: "Keep Airplane Mode on",
            summary: "Wait for the cue before turning Airplane Mode off in Control Center.",
            badge: "Resetting",
            tone: .warning
        )
    case .awaitingCellularReturn:
        DashboardStatusPresentation(
            headline: "Waiting for cellular",
            summary: "Turn Airplane Mode off in Control Center. The Agent will verify cellular automatically.",
            badge: "Reconnecting",
            tone: .warning
        )
    case .verifying:
        DashboardStatusPresentation(
            headline: "Checking your new IP",
            summary: "Cellular is back. The Agent is comparing public addresses before restoring traffic.",
            badge: "Verifying",
            tone: .info
        )
    case let .completed(_, _, _, result):
        completedRotationPresentation(result)
    case let .failed(_, failure):
        failedRotationPresentation(failure)
    }
}

private func completedRotationPresentation(
    _ result: CellularIPRotationResult
) -> DashboardStatusPresentation {
    switch result {
    case .changed:
        DashboardStatusPresentation(
            headline: "Cellular IP changed",
            summary: "The cellular relay is ready with a different public address.",
            badge: "Changed",
            tone: .success
        )
    case .unchanged:
        DashboardStatusPresentation(
            headline: "Carrier reused the IP",
            summary: "The reset completed, but the carrier returned the same address. A longer retry may help.",
            badge: "Unchanged",
            tone: .warning
        )
    case .unverified:
        DashboardStatusPresentation(
            headline: "Cellular reconnected",
            summary: "The Agent restored the relay but could not compare a public address before and after.",
            badge: "Unverified",
            tone: .warning
        )
    }
}

private func failedRotationPresentation(
    _ failure: CellularIPRotationFailure
) -> DashboardStatusPresentation {
    switch failure {
    case .cellularDidNotDisconnect:
        DashboardStatusPresentation(
            headline: "IP rotation cancelled",
            summary: "Cellular never disconnected, so the Agent attempted to restore the relay.",
            badge: "Not rotated",
            tone: .warning
        )
    case .cellularDidNotReturn:
        DashboardStatusPresentation(
            headline: "Cellular did not return",
            summary: "Turn Airplane Mode off and restore cellular data. The Agent attempted to resume.",
            badge: "No cellular",
            tone: .error
        )
    case .cancelled:
        DashboardStatusPresentation(
            headline: "IP rotation cancelled",
            summary: "The Agent attempted to restore the cellular relay.",
            badge: "Cancelled",
            tone: .neutral
        )
    case .recoveryExpired:
        DashboardStatusPresentation(
            headline: "IP rotation expired",
            summary: "The saved rotation attempt expired, so the Agent attempted to resume safely.",
            badge: "Expired",
            tone: .warning
        )
    case .tunnelResumeFailed:
        DashboardStatusPresentation(
            headline: "Agent needs attention",
            summary: "IP rotation ended, but the Agent could not resume. Start the Agent again.",
            badge: "Resume failed",
            tone: .error
        )
    }
}

private func resolvePairingTone(for state: AgentDashboardState) -> AgentDashboardTone {
    if state.pairingInProgress { return .accent }
    switch state.pairingState {
    case .cameraPermissionRequired, .scannerUnavailable, .qrNotRecognized, .failed:
        return .error
    case .paired:
        return .success
    case .idle:
        return state.isEnrolled ? .success : .neutral
    }
}

private func primaryAction(for state: AgentDashboardState) -> AgentPrimaryAction {
    guard state.isEnrolled, !state.pairingInProgress else { return .none }
    switch state.status.agentState {
    case .stopped:
        return .start
    case .running:
        return .stop
    case .starting, .stopping, .failed:
        return .none
    }
}

private func resolveInactiveAgentMessage(for state: AgentDashboardState) -> String {
    if state.pairingInProgress && state.isEnrolled {
        return "Finish the endpoint update before starting the Agent."
    }
    if state.pairingInProgress {
        return "Finish secure phone pairing before starting the Agent."
    }
    return "Pair this phone before starting the Agent."
}

private func resolveRotationAction(for state: AgentDashboardState) -> CellularIPRotationAction {
    guard state.isEnrolled,
          state.status.agentState == .running,
          !state.status.rotation.isActive else {
        return .none
    }
    if case .completed(_, _, _, .unchanged) = state.status.rotation {
        return .retry
    }
    return .rotate
}

private func requiresRotationConfirmation(
    availability: CellularIPRotationAvailability,
    rotation: CellularIPRotationState
) -> Bool {
    if case .awaitingConfirmation = rotation { return true }
    return availability.requiresConfirmation(for: rotation)
}

private func rotationConfirmation(
    for rotation: CellularIPRotationState
) -> CellularIPRotationConfirmationPresentation? {
    guard case let .awaitingConfirmation(_, _, _, activeStreamCount) = rotation else {
        return nil
    }
    return CellularIPRotationConfirmationPresentation(
        title: "Disconnect \(activeStreamCount) active streams?",
        message: "Rotating the cellular IP will close every active proxy stream.",
        confirmLabel: "Disconnect and rotate",
        declineLabel: "Keep current connection"
    )
}

private func resolveRotationLabel(for rotation: CellularIPRotationState) -> String {
    switch rotation {
    case .awaitingConfirmation:
        "Confirm rotation"
    case .preparing:
        "Preparing rotation…"
    case .awaitingAirplaneMode:
        "Waiting for Airplane Mode"
    case .holding:
        "Keep Airplane Mode on"
    case .awaitingCellularReturn:
        "Waiting for cellular"
    case .verifying:
        "Checking public IP…"
    case .completed(_, _, _, .unchanged):
        "Retry with 30-second reset"
    case .idle, .completed, .failed:
        "Rotate cellular IP"
    }
}

private func cellularPresentation(_ health: CellularHealth) -> AgentHealthPresentation {
    switch health {
    case .unavailable:
        AgentHealthPresentation(label: "Cellular", value: "Unavailable", tone: .warning)
    case .available:
        AgentHealthPresentation(label: "Cellular", value: "Available", tone: .success)
    }
}

private func relayPresentation(_ health: RelayHealth) -> AgentHealthPresentation {
    switch health {
    case .disconnected:
        AgentHealthPresentation(label: "Relay", value: "Disconnected", tone: .neutral)
    case .connecting:
        AgentHealthPresentation(label: "Relay", value: "Connecting", tone: .warning)
    case .connected:
        AgentHealthPresentation(label: "Relay", value: "Connected", tone: .success)
    }
}

private extension CellularIPRotationState {
    var safeDiagnosticName: String {
        switch self {
        case .idle: "idle"
        case .awaitingConfirmation: "awaiting confirmation"
        case .preparing: "preparing"
        case .awaitingAirplaneMode: "waiting for airplane mode"
        case .holding: "cellular detached"
        case .awaitingCellularReturn: "waiting for cellular"
        case .verifying: "verifying"
        case let .completed(_, _, _, result): result.rawValue
        case let .failed(_, failure): failure.safeDiagnosticName
        }
    }
}

private extension CellularIPRotationFailure {
    var safeDiagnosticName: String {
        switch self {
        case .cellularDidNotDisconnect: "cellular did not disconnect"
        case .cellularDidNotReturn: "cellular did not return"
        case .cancelled: "cancelled"
        case .recoveryExpired: "recovery expired"
        case .tunnelResumeFailed: "tunnel resume failed"
        }
    }
}

private let blockingRelayErrors: Set<AgentStatusErrorClass> = [
    .relayTLS,
    .relayAuthentication,
    .protocolViolation,
    .credential,
    .internalFailure,
]
