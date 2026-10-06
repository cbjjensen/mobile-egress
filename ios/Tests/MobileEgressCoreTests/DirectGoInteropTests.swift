#if canImport(Security)
import Foundation
import Security
import XCTest
@testable import MobileEgressCore

final class DirectGoInteropTests: XCTestCase {
    private struct Fixture: Decodable {
        let caCertificatePem: String
        let clientId: String
        let pairingId: String
        let invitation: String
        let invitationExpiresAt: String
        let update: String
        let hostedUpdate: String
        let csrPem: String
        let identity: DirectIssuedIdentity
    }
    private func fixture() throws -> Fixture {
        let url = try XCTUnwrap(Bundle.module.url(forResource: "direct-v2-wire", withExtension: "json", subdirectory: "Fixtures"))
        return try JSONDecoder().decode(Fixture.self, from: Data(contentsOf: url))
    }
    func testGoHostedUpdateVerifiesWithPinnedAuthorityAndProtectsMode() throws {
        let fixture = try fixture()
        let date = try XCTUnwrap(ISO8601DateFormatter().date(from: fixture.invitationExpiresAt)).addingTimeInterval(-30)
        let ca = try CertificateAuthorityValidator().validate(fixture.caCertificatePem, at: date)
        var record = DirectClientRecord(clientID: fixture.clientId, displayName: "Workload", endpoint: "https://client.example")
        record.pairingID = fixture.pairingId; record.generation = 1
        let update = try DirectEndpointUpdate.parse(fixture.hostedUpdate, for: record) { try DirectSecurity.verify($0, signature: $1, authority: ca.der) }
        XCTAssertEqual(update.transport, .hosted)
        var wrapper = try XCTUnwrap(JSONSerialization.jsonObject(with: DirectBundle.decode(fixture.hostedUpdate)) as? [String: Any])
        let payload = try DirectBundle.decode(try XCTUnwrap(wrapper["payload"] as? String))
        let changed = try XCTUnwrap(String(data: payload, encoding: .utf8)).replacingOccurrences(of: "hosted", with: "direct")
        wrapper["payload"] = DirectBundle.encode(Data(changed.utf8))
        XCTAssertThrowsError(try DirectEndpointUpdate.parse(DirectBundle.encode(JSONSerialization.data(withJSONObject: wrapper)), for: record) { try DirectSecurity.verify($0, signature: $1, authority: ca.der) })
    }
    func testGoInvitationAndRealAuthoritySignatureAcceptSkippedGenerationAndRejectTampering() throws {
        let fixture = try fixture()
        let date = try XCTUnwrap(ISO8601DateFormatter().date(from: fixture.invitationExpiresAt)).addingTimeInterval(-30)
        let invitation = try DirectInvitation.parse(fixture.invitation, now: date)
        let ca = try CertificateAuthorityValidator().validate(fixture.caCertificatePem, at: date)
        var record = DirectClientRecord(clientID: fixture.clientId, displayName: invitation.displayName, endpoint: invitation.endpoint)
        record.pairingID = fixture.pairingId; record.generation = 1
        let update = try DirectEndpointUpdate.parse(fixture.update, for: record) { try DirectSecurity.verify($0, signature: $1, authority: ca.der) }
        XCTAssertEqual(update.generation, 3)
        XCTAssertNotEqual(update.endpoint, invitation.endpoint)
        var wrapper = try XCTUnwrap(JSONSerialization.jsonObject(with: DirectBundle.decode(fixture.update)) as? [String: Any])
        var payload = try DirectBundle.decode(try XCTUnwrap(wrapper["payload"] as? String))
        payload.append(0x20)
        wrapper["payload"] = DirectBundle.encode(payload)
        let alteredPayload = DirectBundle.encode(try JSONSerialization.data(withJSONObject: wrapper))
        XCTAssertThrowsError(try DirectEndpointUpdate.parse(alteredPayload, for: record) { try DirectSecurity.verify($0, signature: $1, authority: ca.der) })
        wrapper = try XCTUnwrap(JSONSerialization.jsonObject(with: DirectBundle.decode(fixture.update)) as? [String: Any])
        var signature = try DirectBundle.decode(try XCTUnwrap(wrapper["signature"] as? String)); signature[signature.count - 1] ^= 1
        wrapper["signature"] = DirectBundle.encode(signature)
        let alteredSignature = DirectBundle.encode(try JSONSerialization.data(withJSONObject: wrapper))
        XCTAssertThrowsError(try DirectEndpointUpdate.parse(alteredSignature, for: record) { try DirectSecurity.verify($0, signature: $1, authority: ca.der) })
    }

    func testGoChunkedIssuedCertificateMatchesCSRAndUsesDirectOnlyControlRequests() async throws {
        let fixture = try fixture()
        let date = try XCTUnwrap(ISO8601DateFormatter().date(from: fixture.invitationExpiresAt)).addingTimeInterval(-30)
        let invitation = try DirectInvitation.parse(fixture.invitation, now: date)
        let csrBase64 = fixture.csrPem.split(separator: "\n").filter { !$0.hasPrefix("-----") }.joined()
        let csr = try XCTUnwrap(Data(base64Encoded: csrBase64))
        let outer = try children(csr)
        let requestFields = try children(try children(outer[0].value)[0].value)
        let spki = requestFields[2].encoded
        let raw = try Data(contentsOf: XCTUnwrap(Bundle.module.url(forResource: "direct-v2-wire", withExtension: "json", subdirectory: "Fixtures")))
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: raw) as? [String: Any])
        let identityJSON = try JSONSerialization.data(withJSONObject: XCTUnwrap(object["identity"]))
        // Go's directReply streams this >2-KiB identity as chunked HTTP/1.1,
        // including when the phone requests Connection: close.
        XCTAssertGreaterThan(identityJSON.count, 2_048)
        var response = Data("HTTP/1.1 201 Created\r\nContent-Type: application/json\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n\(String(identityJSON.count, radix: 16))\r\n".utf8)
        response.append(identityJSON)
        response.append(Data("\r\n0\r\n\r\n".utf8))
        let transport = DirectFixtureTransport(response: response)
        let control = DirectControlClient(transport: transport, now: { date })
        var record = DirectClientRecord(clientID: fixture.clientId, displayName: invitation.displayName, endpoint: invitation.endpoint)
        record.invitation = invitation; record.csrPEM = fixture.csrPem
        record.key = IdentityKeyMaterial(keyTag: "fixture-key", publicKeyPEM: "unused", publicKeySPKIDER: spki)
        let issued = try await control.issue(record)
        XCTAssertEqual(issued.serial, fixture.identity.serial)
        XCTAssertEqual(issued.generation, 1)
        let request = try XCTUnwrap(transport.request)
        XCTAssertEqual(request.path, "/v2/direct/enroll")
        XCTAssertEqual(request.method, "POST")
        let body = try XCTUnwrap(JSONSerialization.jsonObject(with: request.body) as? [String: String])
        XCTAssertEqual(Set(body.keys), ["clientId", "invitationId", "code", "role", "csrPem"])
        XCTAssertEqual(body["csrPem"], fixture.csrPem)
        XCTAssertNil(transport.configuration?.localIdentityKeyTag)
    }

    private struct Node { let value: Data; let encoded: Data }
    private func children(_ data: Data) throws -> [Node] {
        let bytes = Array(data); var cursor = 0; var nodes: [Node] = []
        while cursor < bytes.count {
            let start = cursor; cursor += 1
            guard cursor < bytes.count else { throw DirectAgentError.invalidBundle }
            let first = bytes[cursor]; cursor += 1; var length = Int(first)
            if first & 128 != 0 {
                let count = Int(first & 127); guard (1...4).contains(count), cursor + count <= bytes.count else { throw DirectAgentError.invalidBundle }
                length = 0; for _ in 0..<count { length = length * 256 + Int(bytes[cursor]); cursor += 1 }
            }
            guard length <= bytes.count - cursor else { throw DirectAgentError.invalidBundle }
            nodes.append(Node(value: Data(bytes[cursor..<cursor+length]), encoded: Data(bytes[start..<cursor+length]))); cursor += length
        }
        return nodes
    }
}
private final class DirectFixtureTransport: HTTPTransporting, @unchecked Sendable {
    private let lock = NSLock()
    let response: Data
    private var capturedRequest: HTTPRequest?
    private var capturedConfiguration: PinnedCellularTransportConfiguration?
    var request: HTTPRequest? { lock.withLock { capturedRequest } }
    var configuration: PinnedCellularTransportConfiguration? { lock.withLock { capturedConfiguration } }
    init(response: Data) { self.response = response }
    func execute(_ request: HTTPRequest, configuration: PinnedCellularTransportConfiguration) async throws -> HTTPResponse {
        lock.withLock { capturedRequest = request; capturedConfiguration = configuration }
        var accumulator = HTTP1ResponseAccumulator()
        for offset in stride(from: 0, to: response.count, by: 37) {
            _ = try accumulator.receive(response[offset..<min(offset + 37, response.count)], isComplete: false)
        }
        guard case let .complete(decoded) = try accumulator.receive(Data(), isComplete: true) else {
            throw HTTP1Error.truncatedResponse
        }
        return decoded
    }
}
#endif
