import Foundation
import XCTest
@testable import MobileEgressCore

final class PairingValidationTests: XCTestCase {
    func testPairingAcceptsStrictUnpaddedBase64URLAndExactFields() throws {
        let parser = PairingBundleParser(now: { TestFixtures.now }, certificateValidator: AcceptingCertificateValidator())

        let pairing = try parser.parse(TestFixtures.pairingQR())

        XCTAssertEqual(pairing.relayOrigin, "https://relay.example:8443")
        XCTAssertEqual(pairing.role, "agent")
        XCTAssertEqual(pairing.capability, "one-use-high-entropy-capability")
    }

    func testPairingRejectsPaddingAndUnexpectedJSONFields() throws {
        let parser = PairingBundleParser(now: { TestFixtures.now }, certificateValidator: AcceptingCertificateValidator())

        XCTAssertThrowsError(try parser.parse(TestFixtures.pairingQR() + "="))
        XCTAssertThrowsError(try parser.parse(TestFixtures.pairingQR(extraFields: ["unexpected": true])))
    }

    func testPairingRejectsNonHTTPSOriginAndExpiredPayload() throws {
        let parser = PairingBundleParser(now: { TestFixtures.now }, certificateValidator: AcceptingCertificateValidator())

        XCTAssertThrowsError(try parser.parse(TestFixtures.pairingQR(relayURL: "http://relay.example:8443")))
        XCTAssertThrowsError(try parser.parse(TestFixtures.pairingQR(relayURL: "https://relay.example:8443/path")))
        XCTAssertThrowsError(try parser.parse(TestFixtures.pairingQR(relayURL: "https://relay.example:8443?unexpected=true")))
        XCTAssertThrowsError(try parser.parse(TestFixtures.pairingQR(expiresAt: "2026-04-30T18:00:00Z")))
    }

    func testPairingRejectsMissingRequiredFields() throws {
        let parser = PairingBundleParser(now: { TestFixtures.now }, certificateValidator: AcceptingCertificateValidator())
        let payload = try TestFixtures.encodeQR([
            "version": 1,
            "relayUrl": "https://relay.example:8443",
            "caCertificatePem": TestFixtures.validCAPEM,
            "capability": "one-use-high-entropy-capability",
            "expiresAt": "2026-05-30T18:10:00Z",
        ])

        XCTAssertThrowsError(try parser.parse(payload))
    }

    func testPairingRejectsDecimalAndExponentVersionLiterals() throws {
        let parser = PairingBundleParser(now: { TestFixtures.now }, certificateValidator: AcceptingCertificateValidator())
        let pairing = try TestFixtures.pairingQR()

        XCTAssertThrowsError(try parser.parse(TestFixtures.replacingVersionLiteral(in: pairing, with: "1.0")))
        XCTAssertThrowsError(try parser.parse(TestFixtures.replacingVersionLiteral(in: pairing, with: "1e0")))
    }

    func testPairingRejectsDuplicateVersionKeysInEitherOrder() throws {
        let parser = PairingBundleParser(now: { TestFixtures.now }, certificateValidator: AcceptingCertificateValidator())
        let pairing = try TestFixtures.pairingQR()

        for versions in [("1", "1.0"), ("1.0", "1")] {
            XCTAssertThrowsError(try parser.parse(TestFixtures.duplicatingVersionLiteral(in: pairing, first: versions.0, second: versions.1)))
        }
    }

    func testPairingRejectsWhitespaceOnlyCapability() throws {
        let parser = PairingBundleParser(now: { TestFixtures.now }, certificateValidator: AcceptingCertificateValidator())

        XCTAssertThrowsError(try parser.parse(TestFixtures.pairingQR(capability: " \t\n")))
    }

    func testCertificateAuthorityValidatorRejectsParseableNonAuthorities() throws {
        let validator = CertificateAuthorityValidator()

        XCTAssertNoThrow(try validator.validate(TestFixtures.validCAPEM, at: TestFixtures.now))
        XCTAssertThrowsError(try validator.validate(TestFixtures.nonCAPEM, at: TestFixtures.now))
        XCTAssertThrowsError(try validator.validate(TestFixtures.caWithoutKeyCertSignPEM, at: TestFixtures.now))
    }

    #if canImport(Security)
    func testCertificateAuthorityValidatorAcceptsAValidCAAndRejectsMalformedPEM() throws {
        let validator = CertificateAuthorityValidator()

        XCTAssertNoThrow(try validator.validate(TestFixtures.validCAPEM, at: TestFixtures.now))
        XCTAssertThrowsError(try validator.validate("not-a-certificate", at: TestFixtures.now))
    }
    #endif
}

private struct AcceptingCertificateValidator: CertificateAuthorityValidating {
    func validate(_ pem: String, at date: Date) throws -> CertificateAuthority {
        CertificateAuthority(der: Data(pem.utf8))
    }
}
