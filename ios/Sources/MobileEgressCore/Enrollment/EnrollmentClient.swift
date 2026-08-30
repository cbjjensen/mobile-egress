import Foundation

public enum EnrollmentError: Error, Equatable {
    case rejectedStatus(Int)
    case invalidContentType
    case invalidResponse
    case responseTooLarge
    case invalidRole
    case invalidSerial
    case certificateAuthorityMismatch
    case invalidCertificateChain
    case certificateAuthorityMissingFromChain
    case leafNotCurrentlyValid
    case leafNotSignedByPinnedAuthority
    case publicKeyMismatch
    case clientAuthenticationRequired
    case certificateSerialMismatch
    case generatedKeyMismatch
}

public struct EnrollmentCertificateEvidence: Equatable, Sendable {
    public let leafIsCurrentlyValid: Bool
    public let leafIsSignedByPinnedAuthority: Bool
    public let publicKeyMatches: Bool
    public let hasClientAuthenticationEKU: Bool
    public let serial: String

    public init(
        leafIsCurrentlyValid: Bool,
        leafIsSignedByPinnedAuthority: Bool,
        publicKeyMatches: Bool,
        hasClientAuthenticationEKU: Bool,
        serial: String
    ) {
        self.leafIsCurrentlyValid = leafIsCurrentlyValid
        self.leafIsSignedByPinnedAuthority = leafIsSignedByPinnedAuthority
        self.publicKeyMatches = publicKeyMatches
        self.hasClientAuthenticationEKU = hasClientAuthenticationEKU
        self.serial = serial
    }
}

public protocol EnrollmentCertificateValidating: Sendable {
    func validate(
        certificateChainDER: [Data],
        pinnedAuthorityDER: Data,
        expectedPublicKeySPKIDER: Data,
        at date: Date
    ) throws -> EnrollmentCertificateEvidence
}

public protocol EnrollmentPerforming: Sendable {
    func enroll(pairing: PairingBundle, key: IdentityKeyMaterial) async throws -> AgentIdentity
}

public final class EnrollmentHTTPClient: EnrollmentPerforming, @unchecked Sendable {
    private let transport: any HTTPTransporting
    private let certificateAuthorityValidator: any CertificateAuthorityValidating
    private let certificateValidator: any EnrollmentCertificateValidating
    private let now: @Sendable () -> Date

    public init(
        transport: any HTTPTransporting,
        certificateAuthorityValidator: any CertificateAuthorityValidating,
        certificateValidator: any EnrollmentCertificateValidating,
        now: @escaping @Sendable () -> Date = Date.init
    ) {
        self.transport = transport
        self.certificateAuthorityValidator = certificateAuthorityValidator
        self.certificateValidator = certificateValidator
        self.now = now
    }

    public func enroll(pairing: PairingBundle, key: IdentityKeyMaterial) async throws -> AgentIdentity {
        let body = try JSONEncoder().encode(EnrollmentRequest(
            publicKeyPEM: key.publicKeyPEM,
            code: pairing.capability,
            role: "agent"
        ))
        let request = HTTPRequest(relayOrigin: pairing.relayOrigin, path: "/v1/enroll", body: body)
        let configuration = try PinnedCellularTransportConfiguration(
            relayOrigin: pairing.relayOrigin,
            pinnedCertificateAuthorityDER: pairing.certificateAuthority.der,
            identity: nil
        )
        let response = try await transport.execute(request, configuration: configuration)
        guard response.statusCode == 201 else { throw EnrollmentError.rejectedStatus(response.statusCode) }
        guard response.body.count <= HTTP1Limits.maximumBodyBytes else { throw EnrollmentError.responseTooLarge }
        guard isJSONContentType(response.singleHeader(named: "content-type")) else {
            throw EnrollmentError.invalidContentType
        }

        do {
            try StrictJSONObject.exactKeys(
                in: response.body,
                expected: ["certificatePem", "caCertificatePem", "serial", "role"]
            )
        } catch {
            throw EnrollmentError.invalidResponse
        }
        let wire: EnrollmentResponse
        do {
            wire = try JSONDecoder().decode(EnrollmentResponse.self, from: response.body)
        } catch {
            throw EnrollmentError.invalidResponse
        }
        guard wire.role == "agent" else { throw EnrollmentError.invalidRole }
        guard isValidSerial(wire.serial) else { throw EnrollmentError.invalidSerial }

        let returnedAuthority: CertificateAuthority
        do {
            returnedAuthority = try certificateAuthorityValidator.validate(wire.caCertificatePEM, at: now())
        } catch {
            throw EnrollmentError.invalidResponse
        }
        guard returnedAuthority.der == pairing.certificateAuthority.der else {
            throw EnrollmentError.certificateAuthorityMismatch
        }
        let chain = try PEMCertificateChain.parse(wire.certificatePEM)
        guard chain.contains(pairing.certificateAuthority.der) else {
            throw EnrollmentError.certificateAuthorityMissingFromChain
        }
        let evidence = try certificateValidator.validate(
            certificateChainDER: chain,
            pinnedAuthorityDER: pairing.certificateAuthority.der,
            expectedPublicKeySPKIDER: key.publicKeySPKIDER,
            at: now()
        )
        guard evidence.leafIsCurrentlyValid else { throw EnrollmentError.leafNotCurrentlyValid }
        guard evidence.leafIsSignedByPinnedAuthority else { throw EnrollmentError.leafNotSignedByPinnedAuthority }
        guard evidence.publicKeyMatches else { throw EnrollmentError.publicKeyMismatch }
        guard evidence.hasClientAuthenticationEKU else { throw EnrollmentError.clientAuthenticationRequired }
        guard evidence.serial.uppercased() == wire.serial.uppercased() else {
            throw EnrollmentError.certificateSerialMismatch
        }

        return AgentIdentity(
            relayOrigin: pairing.relayOrigin,
            role: "agent",
            serial: wire.serial.uppercased(),
            keyTag: key.keyTag,
            certificatePEM: wire.certificatePEM,
            caCertificatePEM: wire.caCertificatePEM,
            caCertificateDER: returnedAuthority.der
        )
    }

    private func isValidSerial(_ serial: String) -> Bool {
        !serial.isEmpty && serial.utf8.count <= 64 && serial.utf8.allSatisfy {
            ($0 >= 0x30 && $0 <= 0x39) || ($0 >= 0x41 && $0 <= 0x46) || ($0 >= 0x61 && $0 <= 0x66)
        }
    }

    private func isJSONContentType(_ value: String?) -> Bool {
        guard let value else { return false }
        return value.split(separator: ";", maxSplits: 1).first?.trimmingCharacters(in: .whitespaces).lowercased() == "application/json"
    }
}

private struct EnrollmentRequest: Encodable {
    let publicKeyPEM: String
    let code: String
    let role: String

    enum CodingKeys: String, CodingKey {
        case publicKeyPEM = "publicKeyPem"
        case code, role
    }
}

private struct EnrollmentResponse: Decodable {
    let certificatePEM: String
    let caCertificatePEM: String
    let serial: String
    let role: String

    enum CodingKeys: String, CodingKey {
        case certificatePEM = "certificatePem"
        case caCertificatePEM = "caCertificatePem"
        case serial, role
    }
}
