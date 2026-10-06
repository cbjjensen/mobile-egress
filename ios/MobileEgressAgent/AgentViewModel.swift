import Combine
import Foundation
import MobileEgressCore
import UIKit

@MainActor
final class AgentViewModel: ObservableObject, DirectForegroundStatusHosting {
    @Published private(set) var clients: [DirectClientRecord] = []
    @Published private(set) var statuses: [DirectPeerStatus] = []
    @Published var lifecycle = DirectSharingLifecycle()
    @Published private(set) var isBusy = false
    @Published private(set) var errorMessage: String?
    @Published private(set) var errorClientID: String?
    @Published var rotationState: CellularIPRotationState = .idle
    @Published var cellularAvailability: DirectCellularAvailability = .unknown
    @Published var isScannerPresented = false
    private var dependencies: MobileEgressDependencies?
    private var cleaner: TunnelManager?
    private var rotation: CellularIPRotationCoordinator<AgentViewModel>?
    private var monitor: Task<Void, Never>?
    private var operation: Task<Void, Never>?
    private var lifecycleEpoch: UInt64 = 0
    private var prepared = false
    private let stopLatchKey = "direct-sharing-explicit-stop"

    var presentation: DirectDashboardPresentation {
        let snapshots = clients.map { client in
            let peer = statuses.first { $0.clientID == client.id }
            var snapshot = DirectDashboardClient(id: client.id, name: client.displayName, transport: client.transport)
            snapshot.enabled = client.enabled; snapshot.paired = client.isPaired
            snapshot.needsAcknowledgement = client.needsAcknowledgement; snapshot.removing = client.removing
            if let recovery = peer?.recovery { snapshot.connection = .recovering(recovery) }
            else if peer?.state == "Connected" { snapshot.connection = .authenticated }
            else if peer != nil { snapshot.connection = .connecting }
            snapshot.streams = peer?.streams ?? 0
            snapshot.uploaded = peer?.uploaded ?? 0; snapshot.downloaded = peer?.downloaded ?? 0
            if errorClientID == client.id { snapshot.actionError = errorMessage }
            return snapshot
        }
        return .init(clients: snapshots, lifecycle: lifecycle, cellular: cellularAvailability,
                     busy: isBusy, runtimeAvailable: dependencies != nil)
    }
    func reportImportError(_ message: String, clientID: String? = nil) {
        errorClientID = clientID; errorMessage = message
    }

    init() {
        do {
            let dependencies = try MobileEgressDependencies.live()
            self.dependencies = dependencies
            cleaner = TunnelManager(configuration: dependencies.configuration)
            let checkpoint = try AppGroupCellularIPRotationCheckpointStore(appGroupIdentifier: dependencies.configuration.appGroupIdentifier)
            let defaults = UserDefaults(suiteName: dependencies.configuration.appGroupIdentifier) ?? .standard
            rotation = CellularIPRotationCoordinator(probe: CellularPublicIPProbe(), pathObserver: CellularPathObserver(), checkpointStore: checkpoint, notificationCue: CellularIPRotationNotificationCue(center: AppleCellularIPRotationNotificationCenter(), firstUseStore: UserDefaultsNotificationFirstUseStore(defaults: defaults)), tunnel: self)
            if let rotation { bindForegroundStatus(to: rotation) }
        } catch { errorMessage = "Secure storage is unavailable. Unlock this iPhone and reopen the app." }
    }
    var activeStreamCount: Int { statuses.reduce(0) { $0 + $1.streams } }
    var title: String {
        if lifecycle.startIntent { return lifecycle.sceneActive ? "Sharing — keep this app open" : "Paused — return to this app" }
        return "Sharing stopped"
    }
    var awakeStatus: String { lifecycle.idleTimerDisabled ? "Auto-lock prevented while sharing" : "Auto-lock follows your iPhone settings" }
    func status(_ client: DirectClientRecord) -> String {
        if client.removing { return "Removal pending — retry Remove" }
        if !client.enabled { return "Disabled" }
        if let status = statuses.first(where: { $0.clientID == client.id }) { return status.state }
        if client.needsAcknowledgement { return "Confirmation pending" }
        return client.isPaired ? "Ready" : "Pairing pending — retry pairing"
    }
    func sceneChanged(active: Bool) {
        lifecycleEpoch &+= 1
        let epoch = lifecycleEpoch
        lifecycle.sceneActive = active
        if active { beginForegroundActivation() }
        updateIdleTimer()
        monitor?.cancel(); monitor = nil
        if !active {
            operation?.cancel()
            rotation?.suspendForForegroundOnly()
            Task { await dependencies?.supervisor.stop() }
            return
        }
        monitor = Task { [weak self] in
            guard let self else { return }
            await prepare(epoch: epoch)
            guard lifecycleEpoch == epoch, !Task.isCancelled else { return }
            guard let rotation else { return }
            await rotation.resumeAfterActivation()
            guard lifecycleEpoch == epoch, !Task.isCancelled else { return }
            completeRotationRecovery(state: rotation.state)
            while !Task.isCancelled, lifecycleEpoch == epoch, lifecycle.sceneActive {
                await refresh()
                try? await Task.sleep(for: .seconds(1))
            }
        }
    }
    private func prepare(epoch: UInt64) async {
        guard let dependencies, let cleaner else { return }
        do {
            try await cleaner.removeLegacyProfiles()
            guard lifecycleEpoch == epoch, lifecycle.sceneActive, !Task.isCancelled else { return }
            if !prepared {
                guard await restoreForegroundPreferences(load: { await dependencies.repository.snapshot() },
                    isCurrent: { self.lifecycleEpoch == epoch && self.lifecycle.sceneActive },
                    explicitStop: { UserDefaults.standard.bool(forKey: self.stopLatchKey) }) else { return }
                prepared = true
            }
            lifecycle.migrationReady = true
            updateIdleTimer()
        } catch {
            guard lifecycleEpoch == epoch, !Task.isCancelled else { return }
            lifecycle.migrationReady = false; lifecycle.terminalFailure(); updateIdleTimer()
            try? await persistPreferences()
            errorClientID = nil
            errorMessage = "Remove the legacy Mobile Egress VPN in Settings → General → VPN & Device Management, then reopen Inevitable Mobile Relay. Other VPNs should remain unchanged."
        }
    }
    func toggleSharing() {
        errorClientID = nil
        if lifecycle.startIntent {
            // Stop stays fail-closed across a relaunch even if protected registry
            // storage becomes unavailable before the preference commit.
            UserDefaults.standard.set(true, forKey: stopLatchKey)
            lifecycle.stop(); updateIdleTimer()
            operation?.cancel()
            operation = Task { [weak self] in
                guard let self else { return }
                await dependencies?.supervisor.stop()
                do { try await persistPreferences() } catch { errorMessage = "Sharing is stopped. Secure storage could not be updated; explicit Start is required to share again." }
            }
            return
        }
        guard !isBusy, !clients.isEmpty, lifecycle.sceneActive, dependencies != nil else { return }
        operation = Task { [weak self] in
            guard let self else { return }
            isBusy = true; defer { isBusy = false }
            do {
                try await cleaner?.removeLegacyProfiles()
                try Task.checkCancellation()
                guard lifecycle.sceneActive else { return }
                lifecycle.migrationReady = true
                try lifecycle.start()
                try await persistPreferences()
                UserDefaults.standard.removeObject(forKey: stopLatchKey)
                updateIdleTimer(); await refresh()
            } catch { lifecycle.terminalFailure(); updateIdleTimer(); errorMessage = "Cannot start sharing. Check secure storage and remove the legacy Mobile Egress VPN in Settings." }
        }
    }
    func setKeepAwake(_ enabled: Bool) {
        errorClientID = nil
        lifecycle.keepAwake = enabled; updateIdleTimer()
        Task { do { try await persistPreferences() } catch { errorMessage = "The screen-awake preference could not be saved." } }
    }
    func acceptScannedCode(_ encoded: String, destination: ClientCodeImportDestination = .addClient, completion: @escaping (Bool) -> Void = { _ in }) {
        isScannerPresented = false
        guard !isBusy, lifecycle.sceneActive, dependencies != nil else { completion(false); return }
        // Claim synchronously: repeated scan/import callbacks must not enqueue
        // multiple operations before the MainActor task begins.
        isBusy = true; errorMessage = nil; errorClientID = destination.clientID
        operation = Task { [weak self] in
            guard let self else { return }
            var succeeded = false
            defer { isBusy = false; completion(succeeded) }
            guard let dependencies else { return }
            do {
                let normalized = try CompactQRInput.normalize(encoded)
                switch try destination.route(isInvitation: (try? DirectInvitation.parse(normalized)) != nil) {
                case .pair:
                    let id = try await dependencies.repository.add(normalized)
                    await dependencies.supervisor.clearRemovalSuppression(id)
                    try await dependencies.repository.recover(id)
                case let .update(expectedClientID):
                    try await dependencies.repository.importUpdate(normalized, expectedClientID: expectedClientID)
                }
                try Task.checkCancellation()
                await refresh()
                succeeded = true
            } catch is CancellationError { } catch DirectAgentError.removalIntentPersistence {
                errorMessage = "Local storage could not save the pairing change. Free storage and retry; this Client has not been re-enabled."
                await refresh()
            } catch let error as DirectAgentError where error == .invitationInvalid || error == .expiredInvitation {
                errorMessage = "The Client invitation expired or was canceled. Generate a new invitation on the workload Client and scan it again."
                await refresh()
            } catch {
                errorMessage = destination == .addClient
                    ? "Pairing or update could not finish. Use a current Client code, then retry; pending pairing is saved."
                    : "This update couldn't be applied. Use a current connection-update code from the selected computer's Client app."
                await refresh()
            }
        }
    }
    func retry(_ client: DirectClientRecord) {
        guard !isBusy, lifecycle.sceneActive else { return }
        errorMessage = nil; errorClientID = client.id
        operation = Task { [weak self] in
            guard let self, let dependencies else { return }
            isBusy = true; defer { isBusy = false }
            do {
                if !client.isPaired { try await dependencies.repository.recover(client.id) }
                await dependencies.supervisor.retryPeer(client.id)
                await refresh()
            }
            catch let error as DirectAgentError where error == .invitationInvalid || error == .expiredInvitation {
                errorMessage = "The Client invitation expired or was canceled. Generate a new invitation on the workload Client and scan it again."
                await refresh()
            }
            catch { errorMessage = DirectPeerRecovery.classify(error).guidance; await refresh() }
        }
    }
    func setEnabled(_ client: DirectClientRecord, _ enabled: Bool) {
        errorMessage = nil; errorClientID = client.id
        Task {
            do { try await dependencies?.repository.setEnabled(client.id, enabled: enabled); await refresh() }
            catch { errorMessage = "Client preference could not be saved." }
        }
    }
    func remove(_ client: DirectClientRecord) {
        errorMessage = nil; errorClientID = client.id
        Task {
            await dependencies?.supervisor.suppressPeerForRemoval(client.id)
            do {
                try await dependencies?.repository.remove(client.id)
                await dependencies?.supervisor.clearRemovalSuppression(client.id)
            } catch DirectAgentError.removalIntentPersistence {
                errorMessage = "This Client is stopped, but removal could not be saved across app relaunch. Keep the app open and retry Remove after freeing storage."
            } catch { errorMessage = "Client removal is pending and reconnection is blocked. Retry Remove after unlocking the phone." }
            await refresh()
        }
    }
    private func refresh() async {
        guard let dependencies else { return }
        clients = await dependencies.repository.snapshot().clients
        await dependencies.supervisor.reconcile(serve: lifecycle.shouldServe)
        statuses = await dependencies.supervisor.statuses()
        rotation?.updateAgentAvailability(isEnrolled: !clients.isEmpty, isAgentRunning: lifecycle.shouldServe, activeStreamCount: activeStreamCount)
    }
    private func persistPreferences() async throws {
        try await dependencies?.repository.preferences(startIntent: lifecycle.startIntent, keepAwake: lifecycle.keepAwake)
    }
    private func updateIdleTimer() { UIApplication.shared.isIdleTimerDisabled = lifecycle.idleTimerDisabled }
    func safeStatusForCopy() -> String {
        "\(MobileEgressBranding.displayName)\niOS foreground only\nSharing: \(lifecycle.startIntent ? "enabled" : "stopped")\n\(cellularAvailability.safeStatusLine)\nClients: \(clients.count)/10\nConnected: \(presentation.connectedCount)\nStreams: \(presentation.streams)\nCellular only; no automatic fallback"
    }
    func requestRotation() { Task { await rotation?.start() } }
    func confirmRotation(_ accepted: Bool) { Task { await rotation?.confirm(proceed: accepted) } }
    func cancelRotation() { Task { await rotation?.cancel() } }
    func reconcileDirectSharing() async {
        await dependencies?.supervisor.reconcile(serve: lifecycle.shouldServe)
        updateIdleTimer()
    }
}
