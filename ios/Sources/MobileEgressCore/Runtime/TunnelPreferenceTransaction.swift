@MainActor
public protocol TunnelPreferenceSession: AnyObject {
    func loadPreferences() async throws
    func applyConfiguration(onDemandEnabled: Bool)
    func savePreferences() async throws
    func startTunnelSession() throws
    func stopTunnelSession()
}

@MainActor
public protocol TunnelRotationPreferenceSession: TunnelPreferenceSession {
    var isRotationTunnelRunning: Bool { get }
    var isOnDemandEnabled: Bool { get }
}

public struct TunnelRotationReceipt: Sendable {
    fileprivate let wasRunning: Bool
    fileprivate let wasOnDemandEnabled: Bool
}

@MainActor
public enum TunnelPreferenceTransaction {
    public static func start(using session: any TunnelPreferenceSession) async throws {
        try await session.loadPreferences()
        session.applyConfiguration(onDemandEnabled: true)
        try await session.savePreferences()
        do {
            try await session.loadPreferences()
            try session.startTunnelSession()
        } catch {
            session.applyConfiguration(onDemandEnabled: false)
            try? await session.savePreferences()
            try? await session.loadPreferences()
            throw error
        }
    }

    public static func stop(using session: any TunnelPreferenceSession) async throws {
        defer { session.stopTunnelSession() }
        try await session.loadPreferences()
        session.applyConfiguration(onDemandEnabled: false)
        try await session.savePreferences()
        try await session.loadPreferences()
    }
}

@MainActor
public enum TunnelRotationPreferenceTransaction {
    public static func pause(
        using session: any TunnelRotationPreferenceSession
    ) async throws -> TunnelRotationReceipt {
        try Task.checkCancellation()
        try await session.loadPreferences()
        try Task.checkCancellation()
        let receipt = TunnelRotationReceipt(
            wasRunning: session.isRotationTunnelRunning,
            wasOnDemandEnabled: session.isOnDemandEnabled
        )
        do {
            session.applyConfiguration(onDemandEnabled: false)
            try Task.checkCancellation()
            try await session.savePreferences()
            try Task.checkCancellation()
            try await session.loadPreferences()
            try Task.checkCancellation()
        } catch {
            await restorePreparedIntent(using: session, receipt: receipt)
            throw error
        }
        session.stopTunnelSession()
        return receipt
    }

    public static func resume(
        using session: any TunnelRotationPreferenceSession,
        receipt: TunnelRotationReceipt?
    ) async throws {
        guard let receipt else {
            try await TunnelPreferenceTransaction.start(using: session)
            return
        }
        try await session.loadPreferences()
        session.applyConfiguration(onDemandEnabled: receipt.wasOnDemandEnabled)
        try await session.savePreferences()
        try await session.loadPreferences()
        if receipt.wasRunning {
            try session.startTunnelSession()
        }
    }

    private static func restorePreparedIntent(
        using session: any TunnelRotationPreferenceSession,
        receipt: TunnelRotationReceipt
    ) async {
        session.applyConfiguration(onDemandEnabled: receipt.wasOnDemandEnabled)
        try? await session.savePreferences()
        try? await session.loadPreferences()
    }
}
