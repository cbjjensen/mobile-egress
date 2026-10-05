import Foundation
import XCTest
@testable import MobileEgressCore
#if canImport(zlib)
import zlib
#endif

final class CompactQRInputTests: XCTestCase {
    private struct Fixture: Decodable {
        struct Valid: Decodable { let name, original, compact: String }
        struct Invalid: Decodable { let name, compact: String }
        let valid: [Valid]
        let invalid: [Invalid]
    }
    private func fixture() throws -> Fixture {
        let url = try XCTUnwrap(Bundle.module.url(forResource: "compact-qr-v1", withExtension: "json", subdirectory: "Fixtures"))
        return try JSONDecoder().decode(Fixture.self, from: Data(contentsOf: url))
    }

    func testExistingCanonicalBundleRemainsUnchanged() throws {
        for item in try fixture().valid {
            XCTAssertTrue(try CompactQRInput.normalize(item.original) == item.original, item.name)
            XCTAssertTrue(try CompactQRInput.normalize(" \n" + item.original + "\t") == item.original, item.name)
        }
    }

    func testRejectsOversizedUnknownOrNoncanonicalInput() throws {
        for text in [String(repeating: "A", count: 87_385), String(repeating: " ", count: 87_385), "MEQR2:AAAA", "MEQR1:", "MEQR1:e30="] {
            XCTAssertThrowsError(try CompactQRInput.normalize(text))
        }
        for text in ["", "e30=", "e31"] {
            XCTAssertEqual(try CompactQRInput.normalize(text), text)
            XCTAssertThrowsError(try DirectBundle.decode(CompactQRInput.normalize(text)))
        }
    }

    func testCompactEnvelopeNeverEntersGenericWireDecode() throws {
        for item in try fixture().valid {
            XCTAssertThrowsError(try DirectBundle.decode(item.compact), item.name)
        }
    }

    #if canImport(zlib)
    func testSharedCompactFixturesRestoreExactOriginalBytes() throws {
        for item in try fixture().valid {
            XCTAssertTrue(try CompactQRInput.normalize(item.compact) == item.original, item.name)
            XCTAssertTrue(try CompactQRInput.normalize(" \n" + item.compact + "\t") == item.original, item.name)
        }
    }

    func testRejectsAllSharedMalformedStreams() throws {
        for item in try fixture().invalid {
            XCTAssertThrowsError(try CompactQRInput.normalize(item.compact), item.name)
        }
    }

    func testExpansionBoundaryIsEnforcedDuringInflation() throws {
        let allowed = Data(repeating: 65, count: 65_536)
        XCTAssertTrue(try CompactQRInput.normalize(compress(allowed)) == DirectBundle.encode(allowed))
        XCTAssertThrowsError(try CompactQRInput.normalize(compress(Data(repeating: 65, count: 65_537))))
        XCTAssertThrowsError(try CompactQRInput.normalize("MEQR1:" + DirectBundle.encode(Data(repeating: 65, count: 65_537))))
    }

    #if canImport(Security)
    func testNormalInvitationAndSignatureValidationStillApply() throws {
        let url = try XCTUnwrap(Bundle.module.url(forResource: "direct-v2-wire", withExtension: "json", subdirectory: "Fixtures"))
        let wire = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(contentsOf: url)) as? [String: Any])
        let caPEM = try XCTUnwrap(wire["caCertificatePem"] as? String)
        let expiry = try XCTUnwrap(wire["invitationExpiresAt"] as? String)
        let date = try XCTUnwrap(ISO8601DateFormatter().date(from: expiry)).addingTimeInterval(-30)
        let ca = try CertificateAuthorityValidator().validate(caPEM, at: date)
        var record = DirectClientRecord(clientID: try XCTUnwrap(wire["clientId"] as? String), displayName: "Workload", endpoint: "https://client.example")
        record.pairingID = try XCTUnwrap(wire["pairingId"] as? String); record.generation = 1
        for item in try fixture().valid {
            let normalized = try CompactQRInput.normalize(item.compact)
            var object = try XCTUnwrap(JSONSerialization.jsonObject(with: DirectBundle.decode(normalized)) as? [String: Any])
            if object["type"] as? String == "mobile-egress-direct-invitation" {
                _ = try DirectInvitation.parse(normalized, now: date)
                XCTAssertThrowsError(try DirectInvitation.parse(normalized, now: date.addingTimeInterval(60)))
            } else {
                _ = try DirectEndpointUpdate.parse(normalized, for: record) { try DirectSecurity.verify($0, signature: $1, authority: ca.der) }
                var signature = try DirectBundle.decode(try XCTUnwrap(object["signature"] as? String))
                signature[signature.count - 1] ^= 1
                object["signature"] = DirectBundle.encode(signature)
                let tampered = try CompactQRInput.normalize(compress(JSONSerialization.data(withJSONObject: object)))
                XCTAssertThrowsError(try DirectEndpointUpdate.parse(tampered, for: record) { try DirectSecurity.verify($0, signature: $1, authority: ca.der) })
            }
        }
    }
    #endif

    private func compress(_ input: Data) throws -> String {
        var count = compressBound(uLong(input.count))
        var output = [UInt8](repeating: 0, count: Int(count))
        let result = input.withUnsafeBytes { bytes in
            compress2(&output, &count, bytes.bindMemory(to: Bytef.self).baseAddress!, uLong(input.count), Z_BEST_COMPRESSION)
        }
        guard result == Z_OK else { throw DirectAgentError.invalidBundle }
        return "MEQR1:" + DirectBundle.encode(Data(output.prefix(Int(count))))
    }
    #endif
}
