#if canImport(Security)
import Foundation
import Security
import XCTest
@testable import MobileEgressCore

final class DirectPeerRecoveryTests: XCTestCase {
    func testKeychainStatusDistinguishesMissingKeyFromUnavailableStorage() {
        XCTAssertNoThrow(try IdentityKeyAccess.requireSuccess(errSecSuccess))
        XCTAssertThrowsError(try IdentityKeyAccess.requireSuccess(errSecItemNotFound)) { XCTAssertEqual($0 as? IdentityError, .keyMissing) }
        for status in [errSecInteractionNotAllowed, errSecNotAvailable, errSecAuthFailed] {
            XCTAssertThrowsError(try IdentityKeyAccess.requireSuccess(status)) { XCTAssertEqual($0 as? IdentityError, .secureStorageUnavailable) }
        }
    }
    func testKnownTrustFailuresRequireAttentionWhileStorageCanRetryWithoutDeletingTrust() {
        let cases: [(any Error, DirectPeerRecovery)] = [
            (DirectAgentError.authorizationDenied, .pairingRequired),
            (DirectAgentError.credentialExpired, .pairingRequired),
            (DirectAgentError.credentialNotYetValid, .clockCheckRequired),
            (IdentityError.keyMissing, .pairingRequired),
            (IdentityError.secureStorageUnavailable, .unlockRequired),
            (DirectAgentError.persistence, .unlockRequired),
            (DirectAgentError.clientStorageUnavailable, .clientStorageUnavailable),
            (DirectAgentError.rejected, .retrying),
            (DirectAgentError.trust, .retrying),
            (URLError(.secureConnectionFailed), .retrying),
            (NSError(domain: "secret endpoint capability", code: 401), .retrying)
        ]
        for (error, expected) in cases {
            let recovery = DirectPeerRecovery.classify(error)
            XCTAssertEqual(recovery, expected)
            XCTAssertEqual(recovery.automaticallyRetries, expected == .retrying || expected == .clientStorageUnavailable)
            XCTAssertFalse(recovery.guidance.contains("secret endpoint capability"))
        }
    }
    func testLocalLeafAndPinnedCAExpiryAndClockErrorsAreDetectedBeforeTransport() throws {
        struct Fixture: Decodable { let identity: DirectIssuedIdentity }
        let url = try XCTUnwrap(Bundle.module.url(forResource: "direct-v2-wire", withExtension: "json", subdirectory: "Fixtures"))
        let issued = try JSONDecoder().decode(Fixture.self, from: Data(contentsOf: url)).identity
        let caDER = try PEMCertificateChain.parse(issued.caCertificatePem)[0]
        let leafExpiry = try DirectCertificateExpiration.date(issued.certificatePem)
        let identity = AgentIdentity(relayOrigin: "https://workload.example", role: "agent", serial: issued.serial, keyTag: "fixture", certificatePEM: issued.certificatePem, caCertificatePEM: issued.caCertificatePem, caCertificateDER: caDER)
        XCTAssertNoThrow(try DirectLocalIdentityReadiness.validate(identity, at: leafExpiry.addingTimeInterval(-60)))
        XCTAssertThrowsError(try DirectLocalIdentityReadiness.validate(identity, at: leafExpiry.addingTimeInterval(1))) { XCTAssertEqual($0 as? DirectAgentError, .credentialExpired) }
        XCTAssertThrowsError(try DirectLocalIdentityReadiness.validate(identity, at: Date(timeIntervalSince1970: 0))) { XCTAssertEqual($0 as? DirectAgentError, .credentialNotYetValid) }
        // A still-valid leaf cannot hide an expired pinned CA.
        let caExpiry = try DirectCertificateExpiration.date(TestFixtures.validCAPEM)
        XCTAssertGreaterThan(try DirectCertificateExpiration.date(issued.caCertificatePem), caExpiry)
        let validLongerLeaf = AgentIdentity(relayOrigin: identity.relayOrigin, role: "agent", serial: "A1", keyTag: "fixture", certificatePEM: issued.caCertificatePem, caCertificatePEM: TestFixtures.validCAPEM, caCertificateDER: try PEMCertificateChain.parse(TestFixtures.validCAPEM)[0])
        XCTAssertThrowsError(try DirectLocalIdentityReadiness.validate(validLongerLeaf, at: caExpiry.addingTimeInterval(1))) { XCTAssertEqual($0 as? DirectAgentError, .credentialExpired) }
    }
}
#endif
