#if canImport(Security)
import Foundation
import Security

public final class SecureEnclaveIdentityKeyManager: IdentityKeyManaging, @unchecked Sendable {
    private static let keyTagPrefix = "mobile-egress.agent.key."
    private let accessGroup: String

    public init(accessGroup: String) throws {
        let trimmed = accessGroup.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { throw IdentityError.invalidIdentity }
        self.accessGroup = trimmed
    }

    public func createKey() throws -> IdentityKeyMaterial {
        let keyTag = Self.keyTagPrefix + UUID().uuidString.lowercased()
        let applicationTag = Data(keyTag.utf8)
        var accessControlError: Unmanaged<CFError>?
        guard let accessControl = SecAccessControlCreateWithFlags(
            nil,
            kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly,
            [.privateKeyUsage],
            &accessControlError
        ) else {
            throw IdentityError.keyCreationFailed
        }
        let privateAttributes: [CFString: Any] = [
            kSecAttrIsPermanent: true,
            kSecAttrIsExtractable: false,
            kSecAttrApplicationTag: applicationTag,
            kSecAttrAccessGroup: accessGroup,
            kSecAttrAccessControl: accessControl,
            kSecAttrCanSign: true,
        ]
        let attributes: [CFString: Any] = [
            kSecAttrKeyType: kSecAttrKeyTypeECSECPrimeRandom,
            kSecAttrKeySizeInBits: 256,
            kSecAttrTokenID: kSecAttrTokenIDSecureEnclave,
            kSecPrivateKeyAttrs: privateAttributes,
        ]
        var creationError: Unmanaged<CFError>?
        guard let privateKey = SecKeyCreateRandomKey(attributes as CFDictionary, &creationError),
              let publicKey = SecKeyCopyPublicKey(privateKey)
        else {
            throw IdentityError.keyCreationFailed
        }
        var exportError: Unmanaged<CFError>?
        guard let x963 = SecKeyCopyExternalRepresentation(publicKey, &exportError) as Data? else {
            try? deleteKey(tag: keyTag)
            throw IdentityError.keyCreationFailed
        }
        do {
            let spki = try P256PublicKeyEncoding.subjectPublicKeyInfo(fromX963: x963)
            return IdentityKeyMaterial(
                keyTag: keyTag,
                publicKeyPEM: P256PublicKeyEncoding.pem(subjectPublicKeyInfo: spki),
                publicKeySPKIDER: spki
            )
        } catch {
            try? deleteKey(tag: keyTag)
            throw error
        }
    }

    public func deleteKey(tag: String) throws {
        guard tag.hasPrefix(Self.keyTagPrefix) else { throw IdentityError.keyDeletionFailed }
        let status = SecItemDelete([
            kSecClass: kSecClassKey,
            kSecAttrKeyType: kSecAttrKeyTypeECSECPrimeRandom,
            kSecAttrApplicationTag: Data(tag.utf8),
            kSecAttrAccessGroup: accessGroup,
        ] as CFDictionary)
        guard status == errSecSuccess || status == errSecItemNotFound else {
            throw IdentityError.keyDeletionFailed
        }
    }

    func privateKey(forTag tag: String) throws -> SecKey {
        guard tag.hasPrefix(Self.keyTagPrefix) else { throw IdentityError.identityLookupFailed }
        var result: CFTypeRef?
        let status = SecItemCopyMatching([
            kSecClass: kSecClassKey,
            kSecAttrKeyType: kSecAttrKeyTypeECSECPrimeRandom,
            kSecAttrApplicationTag: Data(tag.utf8),
            kSecAttrAccessGroup: accessGroup,
            kSecMatchLimit: kSecMatchLimitOne,
            kSecReturnRef: true,
        ] as CFDictionary, &result)
        guard status == errSecSuccess, let result, CFGetTypeID(result) == SecKeyGetTypeID() else {
            throw IdentityError.identityLookupFailed
        }
        return unsafeBitCast(result, to: SecKey.self)
    }
}
#endif
