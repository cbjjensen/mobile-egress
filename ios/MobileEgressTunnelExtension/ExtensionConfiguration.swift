import Foundation
import MobileEgressCore

enum ExtensionConfiguration {
    static func load(bundle: Bundle = .main) throws -> MobileEgressSystemConfiguration {
        try MobileEgressSystemConfiguration(
            providerBundleIdentifier: bundle.object(
                forInfoDictionaryKey: "MobileEgressProviderBundleIdentifier"
            ) as? String ?? "",
            appGroupIdentifier: bundle.object(
                forInfoDictionaryKey: "MobileEgressAppGroupIdentifier"
            ) as? String ?? "",
            keychainAccessGroup: bundle.object(
                forInfoDictionaryKey: "MobileEgressKeychainAccessGroup"
            ) as? String ?? ""
        )
    }
}
