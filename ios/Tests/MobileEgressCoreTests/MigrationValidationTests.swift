import Foundation
import XCTest
@testable import MobileEgressCore

final class MigrationValidationTests: XCTestCase {
    func testMigrationRecognitionPrecedesFullValidation() throws {
        let parser = EndpointMigrationParser(now: { TestFixtures.now }, certificateValidator: FixtureCertificateValidator())

        XCTAssertTrue(parser.recognizes(try TestFixtures.migrationQR(expiresAt: "2026-05-30T18:00:00Z")))
        XCTAssertTrue(parser.recognizes(try TestFixtures.migrationQR(extraFields: ["unexpected": true])))
        XCTAssertFalse(parser.recognizes(try TestFixtures.migrationQR(type: "agent-enrollment")))
    }

    func testMigrationRecognitionRequiresAnIntegerVersion() throws {
        let parser = EndpointMigrationParser(now: { TestFixtures.now }, certificateValidator: FixtureCertificateValidator())
        let payload = try TestFixtures.encodeQR([
            "version": true,
            "type": "agent-endpoint-migration",
        ])

        XCTAssertFalse(parser.recognizes(payload))
    }

    func testMigrationRejectsDifferentCertificateAuthority() throws {
        let migration = try EndpointMigrationParser(now: { TestFixtures.now }, certificateValidator: FixtureCertificateValidator())
            .parse(TestFixtures.migrationQR())

        XCTAssertThrowsError(try MigrationAuthorityMatcher.requireSameAuthority(
            stored: CertificateAuthority(der: Data([0x01])),
            migration: migration.certificateAuthority
        ))
    }
}

private struct FixtureCertificateValidator: CertificateAuthorityValidating {
    func validate(_ pem: String, at date: Date) throws -> CertificateAuthority {
        CertificateAuthority(der: Data([0xCA, 0xFE]))
    }
}
