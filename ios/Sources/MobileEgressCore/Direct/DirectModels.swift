import Foundation

public enum ClientTransport: String, Codable, Sendable {
    case direct, hosted
    public var displayName: String { self == .hosted ? "Inevitable Gateway" : "Direct connection" }
}

/// Older v2 records omit the field. Present values must be valid, never null.
@propertyWrapper public struct ClientTransportValue: Codable, Equatable, Sendable {
    public var wrappedValue: ClientTransport
    public init(wrappedValue: ClientTransport = .direct) { self.wrappedValue = wrappedValue }
    public init(from decoder: any Decoder) throws {
        wrappedValue = try decoder.singleValueContainer().decode(ClientTransport.self)
    }
    public func encode(to encoder: any Encoder) throws {
        var container = encoder.singleValueContainer()
        try container.encode(wrappedValue)
    }
}

extension KeyedDecodingContainer {
    func decode(_ type: ClientTransportValue.Type, forKey key: Key) throws -> ClientTransportValue {
        contains(key) ? try ClientTransportValue(from: superDecoder(forKey: key)) : ClientTransportValue()
    }
}

public enum DirectAgentError: Error, Equatable, Sendable {
    case invalidBundle, expiredInvitation, invitationInvalid, capacity, duplicateClient, unavailable, persistence, removalIntentPersistence
    case rejected, trust, staleUpdate, identityMismatch, migrationRequired, cancelled
    case authorizationDenied, clientStorageUnavailable, credentialExpired, credentialNotYetValid
}

public struct DirectSharingLifecycle: Equatable, Sendable {
    public var sceneActive = false
    public var migrationReady = false
    public var rotationPaused = false
    public var rotationRecoveryPending = false
    public var keepAwake = true
    public private(set) var startIntent = false
    public init(startIntent: Bool = false, keepAwake: Bool = true) {
        self.startIntent = startIntent
        self.keepAwake = keepAwake
    }
    public var shouldServe: Bool { sceneActive && migrationReady && startIntent && !rotationPaused && !rotationRecoveryPending }
    public var idleTimerDisabled: Bool { sceneActive && startIntent && keepAwake }
    public mutating func start() throws {
        guard migrationReady else { throw DirectAgentError.migrationRequired }
        startIntent = true
    }
    public mutating func stop() { startIntent = false }
    public mutating func terminalFailure() { stop() }
}

public struct DirectInvitation: Codable, Equatable, Sendable {
    public let version: Int
    public let type: String
    public let clientId: String
    public let displayName: String
    public let endpoint: String
    public let caCertificatePem: String
    public let invitationId: String
    public let capability: String
    public let expiresAt: String
    public let role: String
    @ClientTransportValue public var transport: ClientTransport = .direct

    public static func parse(_ encoded: String, now: Date = Date()) throws -> Self {
        let data = try DirectBundle.decode(encoded)
        try StrictJSONObject.exactKeys(in: data, expected: ["version", "type", "clientId", "displayName", "endpoint", "caCertificatePem", "invitationId", "capability", "expiresAt", "role"], optional: ["transport"])
        let value = try JSONDecoder().decode(Self.self, from: data)
        guard StrictJSONObject.hasIntegerLiteral(2, forKey: "version", in: data), value.type == "mobile-egress-direct-invitation", value.role == "agent",
              UUID(uuidString: value.clientId) != nil, UUID(uuidString: value.invitationId) != nil,
              !value.displayName.isEmpty, value.displayName.utf8.count <= 160,
              try DirectBundle.decode(value.capability).count == 32 else { throw DirectAgentError.invalidBundle }
        _ = try RelayOrigin.parse(value.endpoint)
        guard let expiry = ISO8601DateFormatter().date(from: value.expiresAt), expiry > now else { throw DirectAgentError.expiredInvitation }
        _ = try CertificateAuthorityValidator().validate(value.caCertificatePem, at: now)
        return value
    }
}

enum DirectBundle {
    static func decode(_ text: String) throws -> Data {
        guard !text.isEmpty, text.utf8.count <= 90_000,
              text.utf8.allSatisfy({ (65...90).contains($0) || (97...122).contains($0) || (48...57).contains($0) || $0 == 45 || $0 == 95 }) else { throw DirectAgentError.invalidBundle }
        let base = text.replacingOccurrences(of: "-", with: "+").replacingOccurrences(of: "_", with: "/")
        guard let data = Data(base64Encoded: base + String(repeating: "=", count: (4 - base.count % 4) % 4)), data.count <= 65_536,
              encode(data) == text else { throw DirectAgentError.invalidBundle }
        return data
    }
    static func encode(_ data: Data) -> String {
        data.base64EncodedString().replacingOccurrences(of: "+", with: "-").replacingOccurrences(of: "/", with: "_").replacingOccurrences(of: "=", with: "")
    }
}

public struct DirectClientRecord: Codable, Equatable, Sendable, Identifiable {
    public var id: String { clientID }
    public let clientID: String
    public var displayName: String
    public var endpoint: String
    public var enabled: Bool
    public var invitation: DirectInvitation?
    public var key: IdentityKeyMaterial?
    public var csrPEM: String?
    public var identity: AgentIdentity?
    public var pairingID: String?
    public var generation: Int64 = 0
    public var needsAcknowledgement = false
    /// Set durably before the first RPC. Nil/false means no credentials can have
    /// been issued; true preserves same-key recovery after an unknown outcome.
    public var enrollmentAttempted: Bool? = nil
    public var removing = false
    @ClientTransportValue public var transport: ClientTransport = .direct
    public init(clientID: String, displayName: String, endpoint: String, enabled: Bool = true) {
        self.clientID = clientID; self.displayName = displayName; self.endpoint = endpoint; self.enabled = enabled
    }
    public var isPaired: Bool { identity != nil && pairingID != nil && !needsAcknowledgement }
}

public struct DirectRegistryDocument: Codable, Equatable, Sendable {
    public var version = 2
    public var clients: [DirectClientRecord] = []
    public var startIntent = false
    public var keepAwake = true
    public init() {}
    public mutating func insert(_ client: DirectClientRecord) throws {
        guard clients.count < 10 else { throw DirectAgentError.capacity }
        guard !clients.contains(where: { $0.clientID == client.clientID }) else { throw DirectAgentError.duplicateClient }
        clients.append(client)
    }
    public func validate() throws {
        guard version == 2, clients.count <= 10, Set(clients.map(\.clientID)).count == clients.count else { throw DirectAgentError.persistence }
        for client in clients {
            guard UUID(uuidString: client.clientID) != nil, client.generation >= 0 else { throw DirectAgentError.persistence }
            _ = try RelayOrigin.parse(client.endpoint)
            if let identity = client.identity {
                guard identity.keyTag == client.key?.keyTag, identity.relayOrigin == client.endpoint,
                      UUID(uuidString: client.pairingID ?? "") != nil, client.generation > 0 else { throw DirectAgentError.persistence }
            }
        }
    }
}

public protocol DirectRegistryPersisting: Sendable {
    func load() throws -> DirectRegistryDocument
    func save(_ document: DirectRegistryDocument) throws
}
