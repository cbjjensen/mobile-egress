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

    func testMigrationRejectsDecimalAndExponentVersionLiterals() throws {
        let parser = EndpointMigrationParser(now: { TestFixtures.now }, certificateValidator: FixtureCertificateValidator())
        let migration = try TestFixtures.migrationQR()

        XCTAssertThrowsError(try parser.parse(TestFixtures.replacingVersionLiteral(in: migration, with: "1.0")))
        XCTAssertThrowsError(try parser.parse(TestFixtures.replacingVersionLiteral(in: migration, with: "1e0")))
    }

    func testMigrationRejectsWhitespaceOnlyCapability() throws {
        let parser = EndpointMigrationParser(now: { TestFixtures.now }, certificateValidator: FixtureCertificateValidator())

        XCTAssertThrowsError(try parser.parse(TestFixtures.migrationQR(capability: " \t\n")))
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
