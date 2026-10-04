import Foundation
import MobileEgressCore

struct MobileEgressDependencies: Sendable {
    let configuration: MobileEgressSystemConfiguration
    let repository: DirectClientRepository
    let supervisor: DirectAgentSupervisor
    static func live(bundle: Bundle = .main) throws -> MobileEgressDependencies {
        let configuration = try MobileEgressSystemConfiguration(
            providerBundleIdentifier: bundle.object(forInfoDictionaryKey: "MobileEgressProviderBundleIdentifier") as? String ?? "",
            appGroupIdentifier: bundle.object(forInfoDictionaryKey: "MobileEgressAppGroupIdentifier") as? String ?? "",
            keychainAccessGroup: bundle.object(forInfoDictionaryKey: "MobileEgressKeychainAccessGroup") as? String ?? "")
        let store = try DirectKeychainStore(accessGroup: configuration.keychainAccessGroup)
        let keys = try SecureEnclaveIdentityKeyManager(accessGroup: configuration.keychainAccessGroup)
        let support = try FileManager.default.url(for: .applicationSupportDirectory, in: .userDomainMask, appropriateFor: nil, create: true)
        let removals = FileDirectRemovalIntentStore(url: support.appendingPathComponent("direct-removal-intents-v2.json"))
        let repository = try DirectClientRepository(store: store, keys: keys, control: DirectControlClient(transport: CellularPinnedHTTPTransport(identityResolver: store)), removalIntents: removals)
        return MobileEgressDependencies(configuration: configuration, repository: repository, supervisor: DirectAgentSupervisor(repository: repository, identityResolver: store))
    }
}
