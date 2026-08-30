import Foundation

public struct EndpointMigration: Equatable {
    public let version: Int
    public let relayOrigin: String
    public let certificateAuthority: CertificateAuthority
    public let capability: String
    public let expiresAt: Date
}

public final class EndpointMigrationParser {
    private let now: () -> Date
    private let certificateValidator: CertificateAuthorityValidating

    public init(
        now: @escaping () -> Date = Date.init,
        certificateValidator: CertificateAuthorityValidating = CertificateAuthorityValidator()
    ) {
        self.now = now
        self.certificateValidator = certificateValidator
    }

    public func recognizes(_ input: String) -> Bool {
        guard let data = try? StrictQRCodeDecoder.decode(input),
              StrictJSONObject.hasIntegerLiteral(1, forKey: "version", in: data),
              let recognition = try? JSONDecoder().decode(MigrationRecognitionWire.self, from: data)
        else {
            return false
        }
        return recognition.version == 1 && recognition.type == "agent-endpoint-migration"
    }

    public func parse(_ input: String) throws -> EndpointMigration {
        let data = try StrictQRCodeDecoder.decode(input)
        try StrictJSONObject.exactKeys(in: data, expected: ["version", "type", "relayUrl", "caCertificatePem", "capability", "expiresAt"])
        guard StrictJSONObject.hasIntegerLiteral(1, forKey: "version", in: data) else {
            throw CoreValidationError.invalidMigration
        }
        let wire = try JSONDecoder().decode(MigrationWire.self, from: data)
        guard wire.version == 1,
              wire.type == "agent-endpoint-migration",
              !wire.capability.allSatisfy(\.isWhitespace),
              wire.capability.utf8.count <= 4 * 1024
        else {
            throw CoreValidationError.invalidMigration
        }
        return EndpointMigration(
            version: wire.version,
            relayOrigin: try RelayOrigin.parse(wire.relayURL),
            certificateAuthority: try certificateValidator.validate(wire.certificateAuthorityPEM, at: now()),
            capability: wire.capability,
            expiresAt: try Expiry.parseFuture(wire.expiresAt, now: now())
        )
    }
}

public enum MigrationAuthorityMatcher {
    public static func requireSameAuthority(stored: CertificateAuthority, migration: CertificateAuthority) throws {
        guard stored == migration else { throw CoreValidationError.certificateAuthorityMismatch }
    }
}

private struct MigrationWire: Decodable {
    let version: Int
    let type: String
    let relayURL: String
    let certificateAuthorityPEM: String
    let capability: String
    let expiresAt: String

    enum CodingKeys: String, CodingKey {
        case version, type, relayURL = "relayUrl", certificateAuthorityPEM = "caCertificatePem", capability, expiresAt
    }
}

private struct MigrationRecognitionWire: Decodable {
    let version: Int
    let type: String
}
