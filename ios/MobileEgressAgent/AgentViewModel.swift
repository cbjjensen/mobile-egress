import Combine
import Foundation
import MobileEgressCore
import NetworkExtension

enum AgentUserError: String, Identifiable {
    case configurationUnavailable
    case scannerUnavailable
    case qrNotRecognized
    case enrollmentRejected
    case migrationRejected
    case identityUnavailable
    case vpnConfiguration
    case vpnStart
    case statusUnavailable

    var id: String { rawValue }

    var message: String {
        switch self {
        case .configurationUnavailable: "App configuration is unavailable."
        case .scannerUnavailable: "QR scanner is unavailable."
        case .qrNotRecognized: "QR code was not recognized."
        case .enrollmentRejected: "Enrollment was rejected."
        case .migrationRejected: "Endpoint update was rejected."
        case .identityUnavailable: "Enrollment is required."
        case .vpnConfiguration: "VPN configuration could not be saved."
        case .vpnStart: "Mobile Egress could not start."
        case .statusUnavailable: "Tunnel status is unavailable."
        }
    }
}

@MainActor
final class AgentViewModel: ObservableObject {
    @Published private(set) var isEnrolled = false
    @Published private(set) var isProcessingScan = false
    @Published private(set) var isChangingTunnel = false
    @Published private(set) var vpnStatus: NEVPNStatus = .invalid
    @Published private(set) var providerStatus = AgentViewModel.stoppedProviderStatus
    @Published private(set) var userError: AgentUserError?
    @Published var isScannerPresented = false

    private let dependencies: MobileEgressDependencies?
    private let tunnelManager: TunnelManager?
    private var monitorTask: Task<Void, Never>?
    private var connectionStatusTask: Task<Void, Never>?
    private var connectionState = TunnelConnectionStateReducer()

    init() {
        do {
            let dependencies = try MobileEgressDependencies.live()
            self.dependencies = dependencies
            self.tunnelManager = TunnelManager(configuration: dependencies.configuration)
        } catch {
            self.dependencies = nil
            self.tunnelManager = nil
            self.userError = .configurationUnavailable
        }
    }

    var canScan: Bool {
        !isProcessingScan && !isChangingTunnel && !vpnStatus.isInProgress
    }

    var canToggleTunnel: Bool {
        isEnrolled && !isProcessingScan && !isChangingTunnel
    }

    var isTunnelActive: Bool {
        vpnStatus.isInProgress
    }

    var statusTitle: String {
        if providerStatus.providerState == .failed { return "Failed" }
        if providerStatus.providerState == .starting && !vpnStatus.isInProgress { return "Starting" }
        switch vpnStatus {
        case .invalid: return isEnrolled ? "Not configured" : "Enrollment required"
        case .disconnected: return "Stopped"
        case .connecting: return "Starting"
        case .connected:
            switch providerStatus.runtimeSnapshot.connectionState {
            case .connecting: return "Connecting to relay"
            case .connected: return "Connected"
            case .stopping: return "Stopping"
            case .stopped: return "Tunnel connected"
            }
        case .reasserting: return "Reconnecting"
        case .disconnecting: return "Stopping"
        @unknown default: return "Unavailable"
        }
    }

    var errorMessage: String? {
        if let userError { return userError.message }
        if providerStatus.providerError != .none {
            return providerErrorMessage(providerStatus.providerError)
        }
        if providerStatus.runtimeSnapshot.errorClass != .none {
            return runtimeErrorMessage(providerStatus.runtimeSnapshot.errorClass)
        }
        return nil
    }

    var activeStreamCount: Int { providerStatus.runtimeSnapshot.activeStreamCount }
    var bytesUploaded: UInt64 { providerStatus.runtimeSnapshot.bytesUploaded }
    var bytesDownloaded: UInt64 { providerStatus.runtimeSnapshot.bytesDownloaded }

    func startMonitoring() {
        guard monitorTask == nil else { return }
        monitorTask = Task { [weak self] in
            guard let self else { return }
            await prepare()
            while !Task.isCancelled {
                await refresh()
                try? await Task.sleep(for: .seconds(1))
            }
        }
    }

    func refreshNow() async {
        await refresh()
    }

    func presentScanner() {
        guard canScan else { return }
        userError = nil
        isScannerPresented = true
    }

    func cancelScanner() {
        isScannerPresented = false
    }

    func scannerBecameUnavailable() {
        isScannerPresented = false
        userError = .scannerUnavailable
    }

    func acceptScannedCode(_ payload: String) {
        guard !isProcessingScan else { return }
        isScannerPresented = false
        guard !payload.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
            userError = .qrNotRecognized
            return
        }
        isProcessingScan = true
        userError = nil
        Task { [weak self] in
            await self?.completeScan(payload)
        }
    }

    func toggleTunnel() {
        guard canToggleTunnel else {
            if !isEnrolled { userError = .identityUnavailable }
            return
        }
        isChangingTunnel = true
        userError = nil
        Task { [weak self] in
            await self?.changeTunnelState()
        }
    }

    private func prepare() async {
        guard let dependencies, let tunnelManager else { return }
        do {
            isEnrolled = try dependencies.hasIdentity()
            guard isEnrolled else {
                vpnStatus = .invalid
                connectionState = TunnelConnectionStateReducer()
                providerStatus = Self.stoppedProviderStatus
                return
            }
            try await tunnelManager.prepare()
            startConnectionStatusMonitoring(using: tunnelManager)
            providerStatus = Self.providerStatus(for: connectionState.restorePersistentIntent(
                onDemandEnabled: tunnelManager.isOnDemandEnabled
            ))
            await refresh()
        } catch {
            userError = .vpnConfiguration
        }
    }

    private func completeScan(_ payload: String) async {
        defer { isProcessingScan = false }
        guard let dependencies, let tunnelManager else {
            userError = .configurationUnavailable
            return
        }
        do {
            _ = try await dependencies.processScannedPayload(payload)
            isEnrolled = true
            try await tunnelManager.prepare()
            startConnectionStatusMonitoring(using: tunnelManager)
            await refresh()
        } catch ScanWorkflowError.migrationRejected {
            userError = .migrationRejected
        } catch ScanWorkflowError.enrollmentRejected {
            userError = .enrollmentRejected
        } catch {
            userError = .vpnConfiguration
        }
    }

    private func changeTunnelState() async {
        defer { isChangingTunnel = false }
        guard let tunnelManager else {
            userError = .configurationUnavailable
            return
        }

        if isTunnelActive {
            await stopTunnel(using: tunnelManager)
        } else {
            await startTunnel(using: tunnelManager)
        }
    }

    private func startTunnel(using tunnelManager: TunnelManager) async {
        providerStatus = Self.providerStatus(for: connectionState.startRequested())
        do {
            try await tunnelManager.start()
            await refresh()
        } catch {
            providerStatus = Self.providerStatus(for: connectionState.startTransactionFailed(
                .runtimeUnavailable
            ))
            userError = .vpnStart
        }
    }

    private func stopTunnel(using tunnelManager: TunnelManager) async {
        providerStatus = Self.providerStatus(for: connectionState.stopRequested())
        do {
            try await tunnelManager.stop()
            providerStatus = Self.providerStatus(for:
                connectionState.stopTransactionCompleted(persistenceSucceeded: true)
            )
        } catch {
            providerStatus = Self.providerStatus(for:
                connectionState.stopTransactionCompleted(persistenceSucceeded: false)
            )
            userError = .vpnConfiguration
        }
        await refresh()
    }

    private func startConnectionStatusMonitoring(using tunnelManager: TunnelManager) {
        connectionStatusTask?.cancel()
        let updates = tunnelManager.statusUpdates()
        connectionStatusTask = Task { [weak self] in
            for await phase in updates {
                guard !Task.isCancelled else { return }
                await self?.refresh(observedPhase: phase)
            }
        }
    }

    private func refresh(observedPhase: TunnelConnectionPhase? = nil) async {
        guard let tunnelManager else { return }
        let refresh = await tunnelManager.connectionRefresh(observedPhase: observedPhase)
        vpnStatus = refresh.status
        let presentation = connectionState.observe(
            refresh.phase,
            disconnectError: refresh.disconnectError
        )
        guard vpnStatus == .connected else {
            providerStatus = Self.providerStatus(for: presentation)
            if presentation.providerError != .none, userError == .statusUnavailable {
                userError = nil
            }
            return
        }
        do {
            providerStatus = try await tunnelManager.providerStatus()
            if userError == .statusUnavailable { userError = nil }
        } catch {
            userError = .statusUnavailable
        }
    }

    private func providerErrorMessage(_ error: TunnelProviderErrorClass) -> String? {
        switch error {
        case .none: nil
        case .identityUnavailable: "Enrollment is required."
        case .invalidConfiguration: "Tunnel configuration is invalid."
        case .tunnelSettings: "iOS rejected the tunnel settings."
        case .runtimeUnavailable: "Agent runtime is unavailable."
        case .invalidMessage: "Tunnel status response was invalid."
        }
    }

    private func runtimeErrorMessage(_ error: AgentRuntimeErrorClass) -> String? {
        switch error {
        case .none: nil
        case .relayUnavailable: "Relay is unavailable."
        case .relayAuth: "Relay authentication failed."
        case .relayTLS: "Relay trust validation failed."
        case .protocol: "Relay protocol error."
        case .targetPolicy: "A target was blocked by policy."
        case .targetConnect: "A target connection failed."
        case .backpressure: "Agent capacity was exceeded."
        case .internal: "Agent runtime failed."
        }
    }

    private static let stoppedProviderStatus = TunnelProviderStatus(
        providerState: .stopped,
        runtimeSnapshot: AgentRuntimeSnapshot(
            connectionState: .stopped,
            activeStreamCount: 0,
            bytesUploaded: 0,
            bytesDownloaded: 0,
            errorClass: .none
        ),
        providerError: .none
    )

    private static func providerStatus(
        for presentation: TunnelConnectionPresentation
    ) -> TunnelProviderStatus {
        return TunnelProviderStatus(
            providerState: presentation.providerState,
            runtimeSnapshot: AgentRuntimeSnapshot(
                connectionState: .stopped,
                activeStreamCount: 0,
                bytesUploaded: 0,
                bytesDownloaded: 0,
                errorClass: .none
            ),
            providerError: presentation.providerError
        )
    }
}

private extension NEVPNStatus {
    var isInProgress: Bool {
        switch self {
        case .connecting, .connected, .reasserting, .disconnecting: true
        case .invalid, .disconnected: false
        @unknown default: false
        }
    }
}
