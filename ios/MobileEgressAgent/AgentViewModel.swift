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
    @Published private(set) var cellularHealth: CellularHealth = .unavailable
    @Published private(set) var rotationState: CellularIPRotationState = .idle
    @Published private(set) var userError: AgentUserError?
    @Published var isScannerPresented = false

    private let dependencies: MobileEgressDependencies?
    private let tunnelManager: TunnelManager?
    private let rotationCoordinator: CellularIPRotationCoordinator<TunnelManager>?
    private var monitorTask: Task<Void, Never>?
    private var connectionStatusTask: Task<Void, Never>?
    private var connectionState = TunnelConnectionStateReducer()

    init() {
        do {
            let dependencies = try MobileEgressDependencies.live()
            let tunnelManager = TunnelManager(configuration: dependencies.configuration)
            self.dependencies = dependencies
            self.tunnelManager = tunnelManager
            self.rotationCoordinator = Self.makeRotationCoordinator(
                configuration: dependencies.configuration,
                tunnelManager: tunnelManager
            )
        } catch {
            self.dependencies = nil
            self.tunnelManager = nil
            self.rotationCoordinator = nil
            self.userError = .configurationUnavailable
        }
        rotationCoordinator?.setStateChangeHandler { [weak self] state in
            self?.rotationState = state
        }
        rotationCoordinator?.setCellularChangeHandler { [weak self] available in
            guard let self else { return }
            self.cellularHealth = available ? .available : .unavailable
            self.syncRotationAvailability()
        }
    }

    var canScan: Bool {
        !isProcessingScan && !isChangingTunnel && !vpnStatus.isInProgress
    }

    var canToggleTunnel: Bool {
        isEnrolled &&
            !isProcessingScan &&
            !isChangingTunnel &&
            tunnelCommandDecision.isEnabled
    }

    var tunnelCommandDecision: TunnelCommandDecision {
        TunnelCommandDecision.resolve(
            providerState: providerStatus.providerState,
            connectionPhase: vpnStatus.connectionPhase
        )
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
            return providerStatus.providerError.userMessage
        }
        if providerStatus.runtimeSnapshot.errorClass != .none {
            return runtimeErrorMessage(providerStatus.runtimeSnapshot.errorClass)
        }
        return nil
    }

    var activeStreamCount: Int { providerStatus.runtimeSnapshot.activeStreamCount }
    var bytesUploaded: UInt64 { providerStatus.runtimeSnapshot.bytesUploaded }
    var bytesDownloaded: UInt64 { providerStatus.runtimeSnapshot.bytesDownloaded }

    var rotationAvailability: CellularIPRotationAvailability {
        CellularIPRotationAvailability(
            isEnrolled: isEnrolled,
            isAgentRunning: providerStatus.providerState == .running,
            isCellularAvailable: cellularHealth == .available,
            activeStreamCount: activeStreamCount
        )
    }

    var canRotateCellularIP: Bool {
        rotationAvailability.isEligible(for: rotationState)
    }

    var rotationRequiresConfirmation: Bool {
        rotationAvailability.requiresConfirmation(for: rotationState)
    }

    var dashboardPresentation: AgentDashboardPresentation {
        AgentDashboardPresentation.present(
            AgentDashboardState(
                isEnrolled: isEnrolled,
                pairingInProgress: isProcessingScan,
                pairingState: dashboardPairingState,
                status: statusSnapshot
            )
        )
    }

    func startMonitoring() {
        guard monitorTask == nil else { return }
        monitorTask = Task { [weak self] in
            guard let self else { return }
            await self.rotationCoordinator?.resumeAfterActivation()
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

    func dismissUserError() {
        userError = nil
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
        let decision = tunnelCommandDecision
        guard canToggleTunnel else {
            if !isEnrolled { userError = .identityUnavailable }
            return
        }
        isChangingTunnel = true
        userError = nil
        Task { [weak self] in
            await self?.changeTunnelState(command: decision.command)
        }
    }

    func confirmStopAgent() {
        let command = TunnelCommandDecision.confirmedStopCommand(
            providerState: providerStatus.providerState,
            connectionPhase: vpnStatus.connectionPhase
        )
        guard isEnrolled,
              !isProcessingScan,
              !isChangingTunnel,
              let command else { return }
        isChangingTunnel = true
        userError = nil
        Task { [weak self] in
            await self?.changeTunnelState(command: command)
        }
    }

    func requestRotation() {
        guard canRotateCellularIP else { return }
        syncRotationAvailability()
        Task { [weak self] in
            await self?.rotationCoordinator?.start(holdSeconds: 10)
        }
    }

    func confirmRotationStart() {
        guard case .awaitingConfirmation = rotationState else { return }
        Task { [weak self] in
            await self?.rotationCoordinator?.confirm(proceed: true)
        }
    }

    func declineRotation() {
        guard case .awaitingConfirmation = rotationState else { return }
        Task { [weak self] in
            await self?.rotationCoordinator?.confirm(proceed: false)
        }
    }

    func cancelRotation() {
        guard rotationState.isActive else { return }
        Task { [weak self] in
            await self?.rotationCoordinator?.cancel()
        }
    }

    func retryRotation() {
        guard case .completed(_, _, _, .unchanged) = rotationState,
              canRotateCellularIP else { return }
        syncRotationAvailability()
        Task { [weak self] in
            await self?.rotationCoordinator?.start(holdSeconds: 30)
        }
    }

    func resumeAfterActivation() {
        Task { [weak self] in
            await self?.rotationCoordinator?.resumeAfterActivation()
        }
    }

    func safeStatusForCopy() -> String {
        statusSnapshot.safeCopiedStatus(isEnrolled: isEnrolled)
    }

    private func prepare() async {
        guard let dependencies, let tunnelManager else { return }
        do {
            isEnrolled = try dependencies.hasIdentity()
            guard isEnrolled else {
                vpnStatus = .invalid
                connectionState = TunnelConnectionStateReducer()
                providerStatus = Self.stoppedProviderStatus
                syncRotationAvailability()
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
        syncRotationAvailability()
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

    private func changeTunnelState(command: TunnelCommand) async {
        defer { isChangingTunnel = false }
        guard let tunnelManager else {
            userError = .configurationUnavailable
            return
        }

        switch command {
        case .stop:
            await stopTunnel(using: tunnelManager)
        case .start:
            await startTunnel(using: tunnelManager)
        }
    }

    private func startTunnel(using tunnelManager: TunnelManager) async {
        providerStatus = Self.providerStatus(for: connectionState.startRequested())
        syncRotationAvailability()
        do {
            try await tunnelManager.start()
            await refresh()
        } catch {
            providerStatus = Self.providerStatus(for: connectionState.startTransactionFailed(
                .runtimeUnavailable
            ))
            userError = .vpnStart
        }
        syncRotationAvailability()
    }

    private func stopTunnel(using tunnelManager: TunnelManager) async {
        providerStatus = Self.providerStatus(for: connectionState.stopRequested())
        syncRotationAvailability()
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
        syncRotationAvailability()
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
        defer { syncRotationAvailability() }
        guard let tunnelManager else { return }
        let observationToken = connectionState.observationToken
        let refresh = await tunnelManager.connectionRefresh(observedPhase: observedPhase)
        guard let presentation = connectionState.observe(
            refresh.phase,
            disconnectError: refresh.disconnectError,
            matching: observationToken
        ) else { return }
        vpnStatus = refresh.status
        guard vpnStatus == .connected else {
            providerStatus = Self.providerStatus(for: presentation)
            if presentation.providerError != .none, userError == .statusUnavailable {
                userError = nil
            }
            return
        }
        let providerStatusToken = connectionState.observationToken
        do {
            let refreshedProviderStatus = try await tunnelManager.providerStatus()
            guard connectionState.isCurrent(providerStatusToken) else { return }
            providerStatus = refreshedProviderStatus
            if userError == .statusUnavailable { userError = nil }
        } catch {
            guard connectionState.isCurrent(providerStatusToken) else { return }
            userError = .statusUnavailable
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

    private func syncRotationAvailability() {
        rotationCoordinator?.updateAgentAvailability(
            isEnrolled: isEnrolled,
            isAgentRunning: providerStatus.providerState == .running,
            activeStreamCount: activeStreamCount
        )
    }

    private var statusSnapshot: AgentStatusSnapshot {
        AgentStatusSnapshot(
            agentState: Self.agentOperationalState(providerStatus.providerState),
            cellular: cellularHealth,
            relay: Self.relayHealth(providerStatus.runtimeSnapshot.connectionState),
            activeStreamCount: activeStreamCount,
            bytesUploaded: bytesUploaded,
            bytesDownloaded: bytesDownloaded,
            errorClass: Self.statusErrorClass(
                provider: providerStatus.providerError,
                runtime: providerStatus.runtimeSnapshot.errorClass,
                cellular: cellularHealth
            ),
            rotation: rotationState
        )
    }

    private var dashboardPairingState: AgentPairingState {
        switch userError {
        case .scannerUnavailable:
            .scannerUnavailable
        case .qrNotRecognized:
            .qrNotRecognized
        case .enrollmentRejected, .migrationRejected:
            .failed
        case .none, .configurationUnavailable, .identityUnavailable, .vpnConfiguration,
             .vpnStart, .statusUnavailable:
            isEnrolled ? .paired : .idle
        }
    }

    private static func makeRotationCoordinator(
        configuration: MobileEgressSystemConfiguration,
        tunnelManager: TunnelManager
    ) -> CellularIPRotationCoordinator<TunnelManager>? {
        guard let checkpointStore = try? AppGroupCellularIPRotationCheckpointStore(
            appGroupIdentifier: configuration.appGroupIdentifier
        ) else { return nil }
        let defaults = UserDefaults(suiteName: configuration.appGroupIdentifier) ?? .standard
        let notificationCue = CellularIPRotationNotificationCue(
            center: AppleCellularIPRotationNotificationCenter(),
            firstUseStore: UserDefaultsNotificationFirstUseStore(defaults: defaults)
        )
        return CellularIPRotationCoordinator(
            probe: CellularPublicIPProbe(),
            pathObserver: CellularPathObserver(),
            checkpointStore: checkpointStore,
            notificationCue: notificationCue,
            tunnel: tunnelManager
        )
    }

    private static func agentOperationalState(
        _ state: TunnelProviderLifecycleState
    ) -> AgentOperationalState {
        switch state {
        case .stopped: .stopped
        case .starting: .starting
        case .running: .running
        case .stopping: .stopping
        case .failed: .failed
        }
    }

    private static func relayHealth(_ state: AgentRuntimeConnectionState) -> RelayHealth {
        switch state {
        case .stopped, .stopping: .disconnected
        case .connecting: .connecting
        case .connected: .connected
        }
    }

    private static func statusErrorClass(
        provider: TunnelProviderErrorClass,
        runtime: AgentRuntimeErrorClass,
        cellular: CellularHealth
    ) -> AgentStatusErrorClass {
        if provider != .none {
            switch provider {
            case .none: break
            case .identityUnavailable: return .credential
            case .relayUnavailable: return .relayUnavailable
            case .relayAuth: return .relayAuthentication
            case .relayTLS: return .relayTLS
            case .protocol, .invalidMessage: return .protocolViolation
            case .targetPolicy: return .targetPolicy
            case .targetConnect: return .targetConnect
            case .backpressure: return .backpressure
            case .invalidConfiguration, .tunnelSettings, .runtimeUnavailable, .internal:
                return .internalFailure
            }
        }
        switch runtime {
        case .none:
            return cellular == .available ? .none : .cellularUnavailable
        case .relayUnavailable: return .relayUnavailable
        case .relayAuth: return .relayAuthentication
        case .relayTLS: return .relayTLS
        case .protocol: return .protocolViolation
        case .targetPolicy: return .targetPolicy
        case .targetConnect: return .targetConnect
        case .backpressure: return .backpressure
        case .internal: return .internalFailure
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
