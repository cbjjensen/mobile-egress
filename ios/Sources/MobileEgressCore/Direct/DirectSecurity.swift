#if canImport(Security)
import Foundation
import Security

/// Single-process registry: the retired extension never reads or writes direct records.
/// A Keychain value update commits the whole bounded document atomically.
public protocol DirectIdentityVault: DirectRegistryPersisting {
    func stage(_ identity: AgentIdentity) throws
    func remove(_ identity: AgentIdentity) throws
}
public protocol DirectIdentityKeyManaging: IdentityKeyManaging {
    func createCSR(key: IdentityKeyMaterial, clientID: String) throws -> String
}
extension SecureEnclaveIdentityKeyManager: DirectIdentityKeyManaging {
    public func createCSR(key: IdentityKeyMaterial, clientID: String) throws -> String {
        try DirectSecurity.csr(key: key, manager: self, clientID: clientID)
    }
}
public final class DirectKeychainStore: DirectIdentityVault, SecurityIdentityResolving, @unchecked Sendable {
    private let accessGroup: String
    private let legacy: SharedKeychainIdentityStore
    private let lock = NSLock()
    public init(accessGroup: String) throws {
        self.accessGroup = accessGroup
        legacy = try SharedKeychainIdentityStore(accessGroup: accessGroup)
    }
    private var query: [CFString: Any] {
        [kSecClass: kSecClassGenericPassword, kSecAttrService: "com.mobileegress.agent.direct", kSecAttrAccount: "registry-v2", kSecAttrAccessGroup: accessGroup]
    }
    public func load() throws -> DirectRegistryDocument {
        try lock.withLock {
            var value: CFTypeRef?
            let status = SecItemCopyMatching(query.merging([kSecReturnData: true, kSecMatchLimit: kSecMatchLimitOne]) { _, b in b } as CFDictionary, &value)
            if status == errSecItemNotFound { return DirectRegistryDocument() }
            guard status == errSecSuccess, let data = value as? Data else { throw DirectAgentError.persistence }
            let document = try JSONDecoder().decode(DirectRegistryDocument.self, from: data)
            try document.validate()
            return document
        }
    }
    public func save(_ document: DirectRegistryDocument) throws {
        try lock.withLock {
            try document.validate()
            let data = try JSONEncoder().encode(document)
            let result = SecItemUpdate(query as CFDictionary, [kSecValueData: data] as CFDictionary)
            if result == errSecSuccess { return }
            guard result == errSecItemNotFound,
                  SecItemAdd(query.merging([kSecValueData: data, kSecAttrAccessible: kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly]) { _, b in b } as CFDictionary, nil) == errSecSuccess else { throw DirectAgentError.persistence }
        }
    }
    public func securityIdentity(forKeyTag keyTag: String) throws -> SecIdentity {
        guard let identity = try load().clients.first(where: { !$0.removing && $0.identity?.keyTag == keyTag })?.identity else { throw IdentityError.identityUnavailable }
        return try securityIdentity(for: identity)
    }
    func securityIdentity(for identity: AgentIdentity) throws -> SecIdentity {
        // A retained direct record may predate origin canonicalization. Adapt only
        // the lookup value; its saved pairing, certificate and key stay unchanged.
        let canonical = identity.replacingRelayOrigin(try RelayOrigin.parse(identity.relayOrigin))
        return try legacy.securityIdentity(for: canonical)
    }
    public func stage(_ identity: AgentIdentity) throws {
        let canonical = identity.replacingRelayOrigin(try RelayOrigin.parse(identity.relayOrigin))
        try legacy.stageCertificate(for: canonical)
    }
    public func remove(_ identity: AgentIdentity) throws { try legacy.removeCertificate(for: identity) }
}

enum DirectSecurity {
    static func verify(_ message: Data, signature: Data, authority: Data) throws -> Bool {
        guard let certificate = SecCertificateCreateWithData(nil, authority as CFData), let key = SecCertificateCopyKey(certificate),
              SecKeyIsAlgorithmSupported(key, .verify, .ecdsaSignatureMessageX962SHA256) else { throw DirectAgentError.trust }
        return SecKeyVerifySignature(key, .ecdsaSignatureMessageX962SHA256, message as CFData, signature as CFData, nil)
    }
    static func csr(key: IdentityKeyMaterial, manager: SecureEnclaveIdentityKeyManager, clientID: String) throws -> String {
        let name = der(0x30, der(0x31, der(0x30, der(0x06, Data([0x55, 0x04, 0x03])) + der(0x0C, Data(clientID.utf8)))))
        let request = der(0x30, der(0x02, Data([0])) + name + key.publicKeySPKIDER + der(0xA0, Data()))
        let privateKey = try manager.privateKey(forTag: key.keyTag)
        guard let signature = SecKeyCreateSignature(privateKey, .ecdsaSignatureMessageX962SHA256, request as CFData, nil) as Data? else { throw IdentityError.keyCreationFailed }
        let algorithm = der(0x30, der(0x06, Data([0x2A, 0x86, 0x48, 0xCE, 0x3D, 0x04, 0x03, 0x02])))
        let encoded = der(0x30, request + algorithm + der(0x03, Data([0]) + signature)).base64EncodedString(options: [.lineLength64Characters, .endLineWithLineFeed])
        return "-----BEGIN CERTIFICATE REQUEST-----\n" + encoded + "\n-----END CERTIFICATE REQUEST-----\n"
    }
    private static func der(_ tag: UInt8, _ bytes: Data) -> Data {
        var header = Data([tag])
        if bytes.count < 128 { header.append(UInt8(bytes.count)) }
        else if bytes.count < 256 { header.append(contentsOf: [0x81, UInt8(bytes.count)]) }
        else { header.append(contentsOf: [0x82, UInt8(bytes.count >> 8), UInt8(bytes.count & 255)]) }
        return header + bytes
    }
}
#endif
