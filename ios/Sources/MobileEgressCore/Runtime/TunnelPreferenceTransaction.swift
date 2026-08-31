@MainActor
public protocol TunnelPreferenceSession: AnyObject {
    func loadPreferences() async throws
    func applyConfiguration(onDemandEnabled: Bool)
    func savePreferences() async throws
    func startTunnelSession() throws
    func stopTunnelSession()
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
