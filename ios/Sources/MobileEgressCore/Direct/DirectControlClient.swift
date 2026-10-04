#if canImport(Security)
import Foundation

public struct DirectIssuedIdentity: Decodable, Sendable {
    let certificatePem: String
    let caCertificatePem: String
    let serial: String
    let role: String
    let clientId: String
    let pairingId: String
    let generation: Int64
}

public protocol DirectControlServing: Sendable {
    func issue(_ record: DirectClientRecord, renewal: Bool) async throws -> DirectIssuedIdentity
    func acknowledge(_ record: DirectClientRecord) async throws
    func configuration(_ record: DirectClientRecord) async throws -> String
}
public final class DirectControlClient: DirectControlServing {
    private let transport: any HTTPTransporting
    private let now: @Sendable () -> Date
    public init(transport: any HTTPTransporting, now: @escaping @Sendable () -> Date = Date.init) { self.transport = transport; self.now = now }

    public func issue(_ record: DirectClientRecord, renewal: Bool = false) async throws -> DirectIssuedIdentity {
        guard let key = record.key, let csr = record.csrPEM else { throw DirectAgentError.unavailable }
        var request: [String: String] = ["clientId": record.clientID, "csrPem": csr]
        if renewal {
            guard let pairing = record.pairingID else { throw DirectAgentError.unavailable }
            request["pairingId"] = pairing
        } else {
            guard let invitation = record.invitation else { throw DirectAgentError.unavailable }
            request["invitationId"] = invitation.invitationId
            request["code"] = invitation.capability
            request["role"] = "agent"
        }
        let body = try await exchange(record, path: renewal ? "/v2/direct/renew" : "/v2/direct/enroll", body: JSONEncoder().encode(request), authenticated: renewal, status: 201)
        try StrictJSONObject.exactKeys(in: body, expected: ["certificatePem", "caCertificatePem", "serial", "role", "clientId", "pairingId", "generation"])
        let result = try JSONDecoder().decode(DirectIssuedIdentity.self, from: body)
        guard result.role == "agent", result.clientId == record.clientID, UUID(uuidString: result.pairingId) != nil,
              result.generation > 0, StrictJSONObject.integerLiteral(forKey: "generation", in: body).map(Int64.init) == result.generation,
              !result.serial.isEmpty, result.serial.count <= 64, result.serial.utf8.allSatisfy({ (48...57).contains($0) || (65...70).contains($0) }),
              !renewal || result.pairingId == record.pairingID else { throw DirectAgentError.rejected }
        let pinned = try authority(record)
        let returned = try CertificateAuthorityValidator().validate(result.caCertificatePem, at: now())
        guard returned.der == pinned else { throw DirectAgentError.trust }
        let evidence = try SecurityEnrollmentCertificateValidator().validate(certificateChainDER: PEMCertificateChain.parse(result.certificatePem), pinnedAuthorityDER: pinned, expectedPublicKeySPKIDER: key.publicKeySPKIDER, at: now())
        guard evidence.leafIsCurrentlyValid, evidence.leafIsSignedByPinnedAuthority, evidence.publicKeyMatches, evidence.hasClientAuthenticationEKU,
              evidence.serial == result.serial else { throw DirectAgentError.trust }
        return result
    }

    public func acknowledge(_ record: DirectClientRecord) async throws {
        struct Ack: Encodable { let clientId: String; let pairingId: String; let generation: Int64 }
        guard let pairingID = record.pairingID else { throw DirectAgentError.unavailable }
        let body = try await exchange(record, path: "/v2/direct/ack", body: JSONEncoder().encode(Ack(clientId: record.clientID, pairingId: pairingID, generation: record.generation)), authenticated: true)
        try StrictJSONObject.exactKeys(in: body, expected: ["status"])
        guard try JSONDecoder().decode([String: String].self, from: body)["status"] == "paired" else { throw DirectAgentError.rejected }
    }

    public func configuration(_ record: DirectClientRecord) async throws -> String {
        let body = try await exchange(record, path: "/v2/direct/config", body: Data(), authenticated: true, method: "GET")
        try StrictJSONObject.exactKeys(in: body, expected: ["update"])
        guard let value = try JSONDecoder().decode([String: String].self, from: body)["update"] else { throw DirectAgentError.rejected }
        return value
    }

    private func exchange(_ record: DirectClientRecord, path: String, body: Data, authenticated: Bool, status: Int = 200, method: String = "POST") async throws -> Data {
        guard body.count <= 65_536 else { throw DirectAgentError.invalidBundle }
        try Task.checkCancellation()
        if authenticated, let identity = record.identity { try DirectLocalIdentityReadiness.validate(identity, at: now()) }
        let configuration = try PinnedCellularTransportConfiguration(relayOrigin: record.endpoint, pinnedCertificateAuthorityDER: authority(record), identity: authenticated ? record.identity : nil)
        if authenticated, record.identity == nil { throw DirectAgentError.unavailable }
        let response = try await transport.execute(HTTPRequest(relayOrigin: record.endpoint, path: path, body: body, method: method), configuration: configuration)
        try Task.checkCancellation()
        guard response.body.count <= 256 * 1024,
              response.singleHeader(named: "content-type")?.split(separator: ";").first?.trimmingCharacters(in: .whitespaces).lowercased() == "application/json" else { throw DirectAgentError.rejected }
        guard response.statusCode == status else {
            if authenticated,
               (try? StrictJSONObject.exactKeys(in: response.body, expected: ["error"])) != nil,
               let error = try? JSONDecoder().decode([String: String].self, from: response.body)["error"] {
                if response.statusCode == 401, error == "unauthorized" { throw DirectAgentError.authorizationDenied }
                if response.statusCode == 503, error == "storage_unavailable" { throw DirectAgentError.clientStorageUnavailable }
            }
            // Only this pinned enrollment response proves the capability can no
            // longer issue credentials. ACK/renew/config rejection is not proof.
            if !authenticated, path == "/v2/direct/enroll",
               (try? StrictJSONObject.exactKeys(in: response.body, expected: ["error"])) != nil,
               let error = try? JSONDecoder().decode([String: String].self, from: response.body)["error"] {
                if response.statusCode == 401, error == "invitation_invalid" { throw DirectAgentError.invitationInvalid }
                if response.statusCode == 410, error == "invitation_expired" { throw DirectAgentError.expiredInvitation }
            }
            throw DirectAgentError.rejected
        }
        return response.body
    }
    private func authority(_ record: DirectClientRecord) throws -> Data {
        if let identity = record.identity { return identity.caCertificateDER }
        guard let invitation = record.invitation else { throw DirectAgentError.unavailable }
        return try CertificateAuthorityValidator().validate(invitation.caCertificatePem, at: now()).der
    }
}
#endif
