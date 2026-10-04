import Foundation

public struct DirectLegacyVPNProfile: Sendable, Equatable {
    public let id: String
    public let providerIdentifier: String
    public init(id: String, providerIdentifier: String) { self.id = id; self.providerIdentifier = providerIdentifier }
}

@MainActor public protocol DirectLegacyVPNProfileManaging: AnyObject {
    func listProfiles() async throws -> [DirectLegacyVPNProfile]
    func disable(_ id: String) async throws
    func stop(_ id: String)
    func waitUntilStopped(_ id: String) async throws
    func remove(_ id: String) async throws
}

@MainActor public enum DirectLegacyVPNCleanup {
    public static func run(providerIdentifier: String, store: any DirectLegacyVPNProfileManaging) async throws {
        guard !providerIdentifier.isEmpty else { throw DirectAgentError.migrationRequired }
        let profiles = try await store.listProfiles()
        for profile in profiles where profile.providerIdentifier == providerIdentifier {
            do { try await store.disable(profile.id) }
            catch { store.stop(profile.id); throw error }
            store.stop(profile.id)
            try await store.waitUntilStopped(profile.id)
            try await store.remove(profile.id)
        }
        guard try await store.listProfiles().allSatisfy({ $0.providerIdentifier != providerIdentifier }) else { throw DirectAgentError.migrationRequired }
    }
}
