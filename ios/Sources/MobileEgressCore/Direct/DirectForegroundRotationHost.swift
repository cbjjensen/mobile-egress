import Foundation

/// App-facing rotation wiring shared with native integration tests. Reconciliation
/// always reads current preferences, never the historical checkpoint's Start intent.
@MainActor
public protocol DirectForegroundRotationHosting: CellularIPRotationTunnelControlling where RotationReceipt == TunnelRotationReceipt {
    var lifecycle: DirectSharingLifecycle { get set }
    func reconcileDirectSharing() async
}

public extension DirectForegroundRotationHosting {
    /// Registry access may suspend while the scene changes or Stop is pressed.
    func restoreForegroundPreferences(
        load: @MainActor () async -> DirectRegistryDocument,
        isCurrent: @MainActor () -> Bool,
        explicitStop: @MainActor () -> Bool
    ) async -> Bool {
        guard isCurrent(), !Task.isCancelled else { return false }
        let document = await load()
        guard isCurrent(), !Task.isCancelled else { return false }
        lifecycle = DirectSharingLifecycle(startIntent: document.startIntent && !explicitStop(), keepAwake: document.keepAwake)
        beginForegroundActivation()
        return true
    }
    func beginForegroundActivation() {
        lifecycle.sceneActive = true
        lifecycle.rotationRecoveryPending = true
    }
    func completeRotationRecovery(state: CellularIPRotationState) {
        lifecycle.rotationPaused = state.blocksDirectSharing
        lifecycle.rotationRecoveryPending = false
    }
    func observeRotationState(_ state: CellularIPRotationState) {
        // Suspension publishes idle while retaining a checkpoint. Activation,
        // not that temporary idle state, owns clearing the recovery gate.
        guard state != .idle else { return }
        lifecycle.rotationPaused = state.blocksDirectSharing
    }
    func captureRotationIntent() async throws -> TunnelRotationReceipt {
        TunnelRotationReceipt(wasRunning: lifecycle.shouldServe, wasOnDemandEnabled: lifecycle.startIntent)
    }
    func pauseForRotation(using receipt: TunnelRotationReceipt) async throws {
        lifecycle.rotationPaused = true
        await reconcileDirectSharing()
    }
    func resumeAfterRotation(_ receipt: TunnelRotationReceipt?) async throws {
        lifecycle.rotationPaused = false
        await reconcileDirectSharing()
    }
}

private extension CellularIPRotationState {
    var blocksDirectSharing: Bool {
        switch self {
        case .preparing, .awaitingAirplaneMode, .holding, .awaitingCellularReturn, .verifying, .restoring: true
        case .idle, .awaitingConfirmation, .completed: false
        case .failed: requiresRecoveryReconstruction
        }
    }
}
