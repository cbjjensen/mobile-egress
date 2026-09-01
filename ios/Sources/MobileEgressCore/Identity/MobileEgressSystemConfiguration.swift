import Foundation

public enum MobileEgressConfigurationError: Error, Equatable, Sendable {
    case missingProviderBundleIdentifier
    case missingAppGroupIdentifier
    case missingKeychainAccessGroup
    case unresolvedBuildSetting
    case invalidProviderBundleIdentifier
    case invalidAppGroupIdentifier
    case invalidKeychainAccessGroup
}

public struct MobileEgressSystemConfiguration: Equatable, Sendable {
    public let providerBundleIdentifier: String
    public let appGroupIdentifier: String
    public let keychainAccessGroup: String

    public init(
        providerBundleIdentifier: String,
        appGroupIdentifier: String,
        keychainAccessGroup: String
    ) throws {
        let provider = providerBundleIdentifier.trimmingCharacters(in: .whitespacesAndNewlines)
        let appGroup = appGroupIdentifier.trimmingCharacters(in: .whitespacesAndNewlines)
        let keychainGroup = keychainAccessGroup.trimmingCharacters(in: .whitespacesAndNewlines)

        guard !provider.isEmpty else { throw MobileEgressConfigurationError.missingProviderBundleIdentifier }
        guard !appGroup.isEmpty else { throw MobileEgressConfigurationError.missingAppGroupIdentifier }
        guard !keychainGroup.isEmpty else { throw MobileEgressConfigurationError.missingKeychainAccessGroup }
        guard ![provider, appGroup, keychainGroup].contains(where: hasUnresolvedBuildSetting) else {
            throw MobileEgressConfigurationError.unresolvedBuildSetting
        }
        guard isIdentifier(provider) else {
            throw MobileEgressConfigurationError.invalidProviderBundleIdentifier
        }
        guard appGroup.hasPrefix("group."), isIdentifier(String(appGroup.dropFirst("group.".count))) else {
            throw MobileEgressConfigurationError.invalidAppGroupIdentifier
        }
        guard isIdentifier(keychainGroup) else {
            throw MobileEgressConfigurationError.invalidKeychainAccessGroup
        }

        self.providerBundleIdentifier = provider
        self.appGroupIdentifier = appGroup
        self.keychainAccessGroup = keychainGroup
    }
}

private func hasUnresolvedBuildSetting(_ value: String) -> Bool {
    value.contains("$(") || value.contains("${")
}

private func isIdentifier(_ value: String) -> Bool {
    guard value.utf8.count <= 255 else { return false }
    let components = value.split(separator: ".", omittingEmptySubsequences: false)
    guard components.count >= 2 else { return false }
    return components.allSatisfy { component in
        !component.isEmpty && component.utf8.allSatisfy { byte in
            (0x30 ... 0x39).contains(byte) ||
                (0x41 ... 0x5A).contains(byte) ||
                (0x61 ... 0x7A).contains(byte) ||
                byte == 0x2D
        }
    }
}
