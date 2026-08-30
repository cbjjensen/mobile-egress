#if canImport(Security)
import Foundation
import Security

public protocol SecurityIdentityResolving: Sendable {
    func securityIdentity(forKeyTag keyTag: String) throws -> SecIdentity
}

public final class SharedKeychainIdentityStore: AgentIdentityPersisting, SecurityIdentityResolving, @unchecked Sendable {
    private static let metadataService = "com.mobileegress.agent.identity"
    private static let activeAccount = "active-v1"
    private static let certificateLabelPrefix = "mobile-egress.agent.certificate."

    private let accessGroup: String
    private let lock = NSLock()

    public init(accessGroup: String) throws {
        let trimmed = accessGroup.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { throw IdentityError.invalidIdentity }
        self.accessGroup = trimmed
    }

    public func load() throws -> AgentIdentity? {
        try lock.withLock {
            var result: CFTypeRef?
            let status = SecItemCopyMatching([
                kSecClass: kSecClassGenericPassword,
                kSecAttrService: Self.metadataService,
                kSecAttrAccount: Self.activeAccount,
                kSecAttrAccessGroup: accessGroup,
                kSecMatchLimit: kSecMatchLimitOne,
                kSecReturnData: true,
            ] as CFDictionary, &result)
            if status == errSecItemNotFound { return nil }
            guard status == errSecSuccess, let data = result as? Data else {
                throw IdentityError.identityUnavailable
            }
            let identity: AgentIdentity
            do {
                identity = try JSONDecoder().decode(AgentIdentity.self, from: data)
            } catch {
                throw IdentityError.identityUnavailable
            }
            try validateStoredIdentity(identity)
            return identity
        }
    }

    public func stageCertificate(for identity: AgentIdentity) throws {
        try lock.withLock {
            try validateStoredIdentity(identity)
            let chain = try PEMCertificateChain.parse(identity.certificatePEM)
            guard let leafDER = chain.first,
                  let certificate = SecCertificateCreateWithData(nil, leafDER as CFData)
            else {
                throw IdentityError.certificatePersistenceFailed
            }
            let label = certificateLabel(for: identity.keyTag)
            let query: [CFString: Any] = [
                kSecClass: kSecClassCertificate,
                kSecAttrLabel: label,
                kSecAttrAccessGroup: accessGroup,
            ]
            var existing: CFTypeRef?
            let existingStatus = SecItemCopyMatching(
                query.merging([kSecReturnData: true, kSecMatchLimit: kSecMatchLimitOne]) { _, new in new } as CFDictionary,
                &existing
            )
            if existingStatus == errSecSuccess, let existingData = existing as? Data, existingData == leafDER {
                _ = try makeSecurityIdentity(leafDER: leafDER, keyTag: identity.keyTag)
                return
            }
            if existingStatus != errSecItemNotFound {
                guard existingStatus == errSecSuccess, SecItemDelete(query as CFDictionary) == errSecSuccess else {
                    throw IdentityError.certificatePersistenceFailed
                }
            }
            let status = SecItemAdd([
                kSecClass: kSecClassCertificate,
                kSecValueRef: certificate,
                kSecAttrLabel: label,
                kSecAttrAccessGroup: accessGroup,
                kSecAttrAccessible: kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly,
            ] as CFDictionary, nil)
            guard status == errSecSuccess else { throw IdentityError.certificatePersistenceFailed }
            do {
                _ = try makeSecurityIdentity(leafDER: leafDER, keyTag: identity.keyTag)
            } catch {
                _ = SecItemDelete(query as CFDictionary)
                throw error
            }
        }
    }

    public func save(_ identity: AgentIdentity) throws {
        try lock.withLock {
            try validateStoredIdentity(identity)
            let encoded: Data
            do {
                let encoder = JSONEncoder()
                encoder.outputFormatting = [.sortedKeys]
                encoded = try encoder.encode(identity)
            } catch {
                throw IdentityError.persistenceFailed
            }
            let query: [CFString: Any] = [
                kSecClass: kSecClassGenericPassword,
                kSecAttrService: Self.metadataService,
                kSecAttrAccount: Self.activeAccount,
                kSecAttrAccessGroup: accessGroup,
            ]
            let updateStatus = SecItemUpdate(query as CFDictionary, [kSecValueData: encoded] as CFDictionary)
            if updateStatus == errSecSuccess { return }
            guard updateStatus == errSecItemNotFound else { throw IdentityError.persistenceFailed }
            let addStatus = SecItemAdd(query.merging([
                kSecValueData: encoded,
                kSecAttrAccessible: kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly,
            ]) { _, new in new } as CFDictionary, nil)
            guard addStatus == errSecSuccess else { throw IdentityError.persistenceFailed }
        }
    }

    public func removeCertificate(for identity: AgentIdentity) throws {
        try lock.withLock {
            let status = SecItemDelete([
                kSecClass: kSecClassCertificate,
                kSecAttrLabel: certificateLabel(for: identity.keyTag),
                kSecAttrAccessGroup: accessGroup,
            ] as CFDictionary)
            guard status == errSecSuccess || status == errSecItemNotFound else {
                throw IdentityError.certificatePersistenceFailed
            }
        }
    }

    public func securityIdentity(forKeyTag keyTag: String) throws -> SecIdentity {
        try lock.withLock {
            guard let identity = try loadWithoutLock(), identity.keyTag == keyTag,
                  let leafDER = try PEMCertificateChain.parse(identity.certificatePEM).first
            else {
                throw IdentityError.identityLookupFailed
            }
            return try makeSecurityIdentity(leafDER: leafDER, keyTag: keyTag)
        }
    }

    private func loadWithoutLock() throws -> AgentIdentity? {
        var result: CFTypeRef?
        let status = SecItemCopyMatching([
            kSecClass: kSecClassGenericPassword,
            kSecAttrService: Self.metadataService,
            kSecAttrAccount: Self.activeAccount,
            kSecAttrAccessGroup: accessGroup,
            kSecMatchLimit: kSecMatchLimitOne,
            kSecReturnData: true,
        ] as CFDictionary, &result)
        if status == errSecItemNotFound { return nil }
        guard status == errSecSuccess, let data = result as? Data,
              let identity = try? JSONDecoder().decode(AgentIdentity.self, from: data)
        else {
            throw IdentityError.identityUnavailable
        }
        try validateStoredIdentity(identity)
        return identity
    }

    private func makeSecurityIdentity(leafDER: Data, keyTag: String) throws -> SecIdentity {
        guard let certificate = SecCertificateCreateWithData(nil, leafDER as CFData) else {
            throw IdentityError.identityLookupFailed
        }
        var keyResult: CFTypeRef?
        let status = SecItemCopyMatching([
            kSecClass: kSecClassKey,
            kSecAttrKeyType: kSecAttrKeyTypeECSECPrimeRandom,
            kSecAttrApplicationTag: Data(keyTag.utf8),
            kSecAttrAccessGroup: accessGroup,
            kSecMatchLimit: kSecMatchLimitOne,
            kSecReturnRef: true,
        ] as CFDictionary, &keyResult)
        guard status == errSecSuccess, let keyResult else {
            throw IdentityError.identityLookupFailed
        }
        guard CFGetTypeID(keyResult) == SecKeyGetTypeID() else {
            throw IdentityError.identityLookupFailed
        }
        let privateKey = unsafeBitCast(keyResult, to: SecKey.self)
        guard let identity = SecIdentityCreate(nil, certificate, privateKey) else {
            throw IdentityError.identityLookupFailed
        }
        return identity
    }

    private func validateStoredIdentity(_ identity: AgentIdentity) throws {
        guard identity.role == "agent",
              !identity.keyTag.isEmpty,
              !identity.serial.isEmpty,
              identity.serial.utf8.count <= 64,
              identity.serial.utf8.allSatisfy({
                  ($0 >= 0x30 && $0 <= 0x39) || ($0 >= 0x41 && $0 <= 0x46) || ($0 >= 0x61 && $0 <= 0x66)
              }),
              !identity.caCertificateDER.isEmpty,
              try RelayOrigin.parse(identity.relayOrigin) == identity.relayOrigin
        else {
            throw IdentityError.invalidIdentity
        }
        let chain = try PEMCertificateChain.parse(identity.certificatePEM)
        let authorities = try PEMCertificateChain.parse(identity.caCertificatePEM)
        guard authorities == [identity.caCertificateDER],
              chain.contains(identity.caCertificateDER)
        else {
            throw IdentityError.invalidIdentity
        }
    }

    private func certificateLabel(for keyTag: String) -> String {
        Self.certificateLabelPrefix + keyTag
    }
}
#endif
