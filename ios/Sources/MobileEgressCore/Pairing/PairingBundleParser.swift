import Foundation

public struct PairingBundle: Equatable {
    public let version: Int
    public let relayOrigin: String
    public let certificateAuthority: CertificateAuthority
    public let capability: String
    public let role: String
    public let expiresAt: Date
}

public final class PairingBundleParser {
    private let now: () -> Date
    private let certificateValidator: CertificateAuthorityValidating

    public init(
        now: @escaping () -> Date = Date.init,
        certificateValidator: CertificateAuthorityValidating = CertificateAuthorityValidator()
    ) {
        self.now = now
        self.certificateValidator = certificateValidator
    }

    public func parse(_ input: String) throws -> PairingBundle {
        let data = try StrictQRCodeDecoder.decode(input)
        try StrictJSONObject.exactKeys(in: data, expected: ["version", "relayUrl", "caCertificatePem", "capability", "role", "expiresAt"])
        let wire = try JSONDecoder().decode(PairingWire.self, from: data)
        guard wire.version == 1,
              wire.role == "agent",
              !wire.capability.isEmpty,
              wire.capability.utf8.count <= 4 * 1024
        else {
            throw CoreValidationError.invalidPairing
        }
        return PairingBundle(
            version: wire.version,
            relayOrigin: try RelayOrigin.parse(wire.relayURL),
            certificateAuthority: try certificateValidator.validate(wire.certificateAuthorityPEM, at: now()),
            capability: wire.capability,
            role: wire.role,
            expiresAt: try Expiry.parseFuture(wire.expiresAt, now: now())
        )
    }
}

private struct PairingWire: Decodable {
    let version: Int
    let relayURL: String
    let certificateAuthorityPEM: String
    let capability: String
    let role: String
    let expiresAt: String

    enum CodingKeys: String, CodingKey {
        case version, relayURL = "relayUrl", certificateAuthorityPEM = "caCertificatePem", capability, role, expiresAt
    }
}
