#if canImport(Security)
import Foundation
import XCTest
@testable import MobileEgressCore

final class DirectEnrollmentRejectionTests: XCTestCase {
    func testExpiredLocalCredentialsCannotReachAuthenticatedTransport() async throws {
        let transport = CountingRejectedTransport()
        var paired = try record()
        paired.identity = AgentIdentity(relayOrigin: paired.endpoint, role: "agent", serial: "A1", keyTag: "key", certificatePEM: TestFixtures.validCAPEM, caCertificatePEM: TestFixtures.validCAPEM, caCertificateDER: try PEMCertificateChain.parse(TestFixtures.validCAPEM)[0])
        let expiry = try DirectCertificateExpiration.date(TestFixtures.validCAPEM)
        let control = DirectControlClient(transport: transport, now: { expiry.addingTimeInterval(1) })
        do { _ = try await control.configuration(paired); XCTFail("expired credentials must fail before transport") }
        catch { XCTAssertEqual(error as? DirectAgentError, .credentialExpired) }
        let requests = await transport.requests
        XCTAssertEqual(requests, 0)
    }
    func testAuthenticatedRecoveryOnlyTrustsExactPinnedAuthorizationAndStorageResponses() async throws {
        let cases: [(Int, String, String, DirectAgentError)] = [
            (401, #"{"error":"unauthorized"}"#, "application/json", .authorizationDenied),
            (503, #"{"error":"storage_unavailable"}"#, "application/json", .clientStorageUnavailable),
            (503, #"{"error":"unauthorized"}"#, "application/json", .rejected),
            (401, #"{"error":"storage_unavailable"}"#, "application/json", .rejected),
            (401, #"{"error":"unauthorized","secret":"ignored"}"#, "application/json", .rejected),
            (401, #"{"error":"unauthorized","error":"unauthorized"}"#, "application/json", .rejected),
            (401, #"{"error":"unauthorized"}"#, "text/plain", .rejected),
            (401, "sensitive raw server response", "application/json", .rejected)
        ]
        for (status, body, contentType, expected) in cases {
            let control = DirectControlClient(transport: RejectionHTTPTransport(response: HTTPResponse(statusCode: status, headers: ["content-type": [contentType]], body: Data(body.utf8))))
            var paired = try record()
            paired.pairingID = UUID().uuidString
            paired.identity = AgentIdentity(relayOrigin: paired.endpoint, role: "agent", serial: "A1", keyTag: "key", certificatePEM: TestFixtures.validCAPEM, caCertificatePEM: TestFixtures.validCAPEM, caCertificateDER: try PEMCertificateChain.parse(TestFixtures.validCAPEM)[0])
            do { _ = try await control.configuration(paired); XCTFail("expected rejection") }
            catch { XCTAssertEqual(error as? DirectAgentError, expected) }
        }
    }
    func testOnlyExactPinnedEnrollmentRejectionsAreDefinitive() async throws {
        let cases: [(Int, String, String, DirectAgentError)] = [
            (401, #"{"error":"invitation_invalid"}"#, "application/json", .invitationInvalid),
            (410, #"{"error":"invitation_expired"}"#, "application/json", .expiredInvitation),
            (401, #"{"error":"invitation_expired"}"#, "application/json", .rejected),
            (410, #"{"error":"invitation_invalid"}"#, "application/json", .rejected),
            (403, #"{"error":"invitation_invalid"}"#, "application/json", .rejected),
            (401, #"{"error":"invitation_invalid","other":"unknown"}"#, "application/json", .rejected),
            (401, #"{"error":"invitation_invalid","error":"invitation_invalid"}"#, "application/json", .rejected),
            (401, #"{"error":"invitation_invalid"} {}"#, "application/json", .rejected),
            (401, #"{"error":"invitation_invalid"}"#, "text/plain", .rejected),
            (410, "not JSON", "application/json", .rejected),
        ]
        for (status, body, contentType, expected) in cases {
            let control = DirectControlClient(transport: RejectionHTTPTransport(response: HTTPResponse(statusCode: status, headers: ["content-type": [contentType]], body: Data(body.utf8))))
            do { _ = try await control.issue(record()); XCTFail("expected rejection") }
            catch { XCTAssertEqual(error as? DirectAgentError, expected) }
        }
    }
    func testSameResponseNeverExpiresRenewalAckOrConfiguration() async throws {
        let control = DirectControlClient(transport: RejectionHTTPTransport(response: HTTPResponse(statusCode: 401, headers: ["content-type": ["application/json"]], body: Data(#"{"error":"invitation_invalid"}"#.utf8))))
        var record = try record()
        record.pairingID = UUID().uuidString
        record.identity = AgentIdentity(relayOrigin: record.endpoint, role: "agent", serial: "A1", keyTag: "key", certificatePEM: TestFixtures.validCAPEM, caCertificatePEM: TestFixtures.validCAPEM, caCertificateDER: try PEMCertificateChain.parse(TestFixtures.validCAPEM)[0])
        do { _ = try await control.issue(record, renewal: true); XCTFail("expected rejection") } catch { XCTAssertEqual(error as? DirectAgentError, .rejected) }
        do { try await control.acknowledge(record); XCTFail("expected rejection") } catch { XCTAssertEqual(error as? DirectAgentError, .rejected) }
        do { _ = try await control.configuration(record); XCTFail("expected rejection") } catch { XCTAssertEqual(error as? DirectAgentError, .rejected) }
    }
    private func record() throws -> DirectClientRecord {
        let invitation = try DirectInvitation.parse(DirectBundle.encode(JSONSerialization.data(withJSONObject: ["version": 2, "type": "mobile-egress-direct-invitation", "clientId": UUID().uuidString, "displayName": "Workload", "endpoint": "https://workload.example", "caCertificatePem": TestFixtures.validCAPEM, "invitationId": UUID().uuidString, "capability": DirectBundle.encode(Data(repeating: 7, count: 32)), "expiresAt": "2029-01-01T00:00:00Z", "role": "agent"])))
        var record = DirectClientRecord(clientID: invitation.clientId, displayName: invitation.displayName, endpoint: invitation.endpoint)
        record.invitation = invitation; record.key = IdentityKeyMaterial(keyTag: "key", publicKeyPEM: "unused", publicKeySPKIDER: Data([1])); record.csrPEM = "saved-csr"
        return record
    }
}
private struct RejectionHTTPTransport: HTTPTransporting {
    let response: HTTPResponse
    func execute(_ request: HTTPRequest, configuration: PinnedCellularTransportConfiguration) async throws -> HTTPResponse { response }
}
private actor CountingRejectedTransport: HTTPTransporting {
    var requests = 0
    func execute(_ request: HTTPRequest, configuration: PinnedCellularTransportConfiguration) async throws -> HTTPResponse {
        requests += 1
        throw URLError(.cannotConnectToHost)
    }
}
#endif
