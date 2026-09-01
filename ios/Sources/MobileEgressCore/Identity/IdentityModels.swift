import Foundation

public enum IdentityError: Error, Equatable {
    case invalidIdentity
    case identityUnavailable
    case keyCreationFailed
    case keyDeletionFailed
    case persistenceFailed
    case certificatePersistenceFailed
    case identityLookupFailed
}

public struct IdentityKeyMaterial: Equatable, Sendable {
    public let keyTag: String
    public let publicKeyPEM: String
    public let publicKeySPKIDER: Data

    public init(keyTag: String, publicKeyPEM: String, publicKeySPKIDER: Data) {
        self.keyTag = keyTag
        self.publicKeyPEM = publicKeyPEM
        self.publicKeySPKIDER = publicKeySPKIDER
    }
}

public struct AgentIdentity: Codable, Equatable, Sendable {
    public let relayOrigin: String
    public let role: String
    public let serial: String
    public let keyTag: String
    public let certificatePEM: String
    public let caCertificatePEM: String
    public let caCertificateDER: Data

    public init(
        relayOrigin: String,
        role: String,
        serial: String,
        keyTag: String,
        certificatePEM: String,
        caCertificatePEM: String,
        caCertificateDER: Data
    ) {
        self.relayOrigin = relayOrigin
        self.role = role
        self.serial = serial
        self.keyTag = keyTag
        self.certificatePEM = certificatePEM
        self.caCertificatePEM = caCertificatePEM
        self.caCertificateDER = caCertificateDER
    }

    public func replacingRelayOrigin(_ relayOrigin: String) -> AgentIdentity {
        AgentIdentity(
            relayOrigin: relayOrigin,
            role: role,
            serial: serial,
            keyTag: keyTag,
            certificatePEM: certificatePEM,
            caCertificatePEM: caCertificatePEM,
            caCertificateDER: caCertificateDER
        )
    }
}

public protocol IdentityKeyManaging: Sendable {
    func createKey() throws -> IdentityKeyMaterial
    func deleteKey(tag: String) throws
}

public protocol AgentIdentityPersisting: Sendable {
    func load() throws -> AgentIdentity?
    func stageCertificate(for identity: AgentIdentity) throws
    func save(_ identity: AgentIdentity) throws
    func removeCertificate(for identity: AgentIdentity) throws
}
