import Foundation

public enum EndpointMigrationError: Error, Equatable {
    case identityRequired
    case identityRoleInvalid
    case certificateAuthorityMismatch
    case rejectedStatus(Int)
    case invalidContentType
    case invalidResponse
    case responseTooLarge
    case relayOriginMismatch
}

public protocol EndpointMigrationPerforming: Sendable {
    func consume(migration: EndpointMigration, identity: AgentIdentity) async throws -> String
}

public final class EndpointMigrationRepository: @unchecked Sendable {
    private let identityStore: any AgentIdentityPersisting
    private let performer: any EndpointMigrationPerforming

    public init(identityStore: any AgentIdentityPersisting, performer: any EndpointMigrationPerforming) {
        self.identityStore = identityStore
        self.performer = performer
    }

    public func consume(_ migration: EndpointMigration) async throws -> AgentIdentity {
        guard let identity = try identityStore.load() else { throw EndpointMigrationError.identityRequired }
        guard identity.role == "agent" else { throw EndpointMigrationError.identityRoleInvalid }
        guard identity.caCertificateDER == migration.certificateAuthority.der else {
            throw EndpointMigrationError.certificateAuthorityMismatch
        }
        let confirmed = try RelayOrigin.parse(try await performer.consume(migration: migration, identity: identity))
        guard confirmed == migration.relayOrigin else { throw EndpointMigrationError.relayOriginMismatch }
        let migrated = identity.replacingRelayOrigin(migration.relayOrigin)
        try identityStore.save(migrated)
        return migrated
    }
}

public final class EndpointMigrationHTTPClient: EndpointMigrationPerforming, @unchecked Sendable {
    private let transport: any HTTPTransporting

    public init(transport: any HTTPTransporting) {
        self.transport = transport
    }

    public func consume(migration: EndpointMigration, identity: AgentIdentity) async throws -> String {
        guard identity.role == "agent" else { throw EndpointMigrationError.identityRoleInvalid }
        guard identity.caCertificateDER == migration.certificateAuthority.der else {
            throw EndpointMigrationError.certificateAuthorityMismatch
        }
        let body = try JSONEncoder().encode(MigrationConsumeRequest(capability: migration.capability))
        let request = HTTPRequest(
            relayOrigin: migration.relayOrigin,
            path: "/v1/endpoint-migrations/consume",
            body: body
        )
        let configuration = try PinnedCellularTransportConfiguration(
            relayOrigin: migration.relayOrigin,
            pinnedCertificateAuthorityDER: migration.certificateAuthority.der,
            identity: identity
        )
        let response = try await transport.execute(request, configuration: configuration)
        guard response.statusCode == 200 else { throw EndpointMigrationError.rejectedStatus(response.statusCode) }
        guard response.body.count <= HTTP1Limits.maximumBodyBytes else { throw EndpointMigrationError.responseTooLarge }
        guard isJSONContentType(response.singleHeader(named: "content-type")) else {
            throw EndpointMigrationError.invalidContentType
        }
        do {
            try StrictJSONObject.exactKeys(in: response.body, expected: ["relayUrl"])
            let wire = try JSONDecoder().decode(MigrationConsumeResponse.self, from: response.body)
            return try RelayOrigin.parse(wire.relayURL)
        } catch {
            throw EndpointMigrationError.invalidResponse
        }
    }

    private func isJSONContentType(_ value: String?) -> Bool {
        guard let value else { return false }
        return value.split(separator: ";", maxSplits: 1).first?.trimmingCharacters(in: .whitespaces).lowercased() == "application/json"
    }
}

private struct MigrationConsumeRequest: Encodable {
    let capability: String
}

private struct MigrationConsumeResponse: Decodable {
    let relayURL: String

    enum CodingKeys: String, CodingKey {
        case relayURL = "relayUrl"
    }
}
