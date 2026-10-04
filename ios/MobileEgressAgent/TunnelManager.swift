import Foundation
import NetworkExtension
import MobileEgressCore

/// Cleanup compatibility only; this adapter has no method that starts a VPN.
@MainActor
final class TunnelManager: DirectLegacyVPNProfileManaging {
    private let providerIdentifier: String
    private var loaded: [String: NETunnelProviderManager] = [:]
    private var cleaning = false
    init(configuration: MobileEgressSystemConfiguration) { providerIdentifier = configuration.providerBundleIdentifier }
    func removeLegacyProfiles() async throws {
        guard !cleaning else { throw DirectAgentError.migrationRequired }
        cleaning = true
        defer { cleaning = false }
        try await DirectLegacyVPNCleanup.run(providerIdentifier: providerIdentifier, store: self)
    }
    func listProfiles() async throws -> [DirectLegacyVPNProfile] {
        let profiles = try await NETunnelProviderManager.loadAllFromPreferences()
        loaded = [:]
        return profiles.map { profile in
            let id = UUID().uuidString
            loaded[id] = profile
            let provider = (profile.protocolConfiguration as? NETunnelProviderProtocol)?.providerBundleIdentifier ?? ""
            return DirectLegacyVPNProfile(id: id, providerIdentifier: provider)
        }
    }
    func disable(_ id: String) async throws {
        let profile = try owned(id)
        profile.isOnDemandEnabled = false
        profile.onDemandRules = []
        profile.isEnabled = false
        try await profile.saveToPreferences()
    }
    func stop(_ id: String) { try? owned(id).connection.stopVPNTunnel() }
    func waitUntilStopped(_ id: String) async throws {
        let profile = try owned(id)
        for _ in 0..<50 {
            if profile.connection.status == .disconnected || profile.connection.status == .invalid { return }
            try await Task.sleep(for: .milliseconds(100))
        }
        throw DirectAgentError.migrationRequired
    }
    func remove(_ id: String) async throws { try await owned(id).removeFromPreferences() }
    private func owned(_ id: String) throws -> NETunnelProviderManager {
        guard let profile = loaded[id], (profile.protocolConfiguration as? NETunnelProviderProtocol)?.providerBundleIdentifier == providerIdentifier else { throw DirectAgentError.migrationRequired }
        return profile
    }
}
