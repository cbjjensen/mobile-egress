#if canImport(Security)
import Foundation
import Security

public struct SecurityEnrollmentCertificateValidator: EnrollmentCertificateValidating {
    public init() {}

    public func validate(
        certificateChainDER: [Data],
        pinnedAuthorityDER: Data,
        expectedPublicKeySPKIDER: Data,
        at date: Date
    ) throws -> EnrollmentCertificateEvidence {
        guard let leafDER = certificateChainDER.first,
              certificateChainDER.contains(pinnedAuthorityDER),
              let authority = SecCertificateCreateWithData(nil, pinnedAuthorityDER as CFData)
        else {
            throw EnrollmentError.invalidCertificateChain
        }
        guard let leaf = SecCertificateCreateWithData(nil, leafDER as CFData) else {
            throw EnrollmentError.invalidCertificateChain
        }
        let fields = try X509CertificateFields(der: leafDER)
        var trust: SecTrust?
        guard SecTrustCreateWithCertificates(
            [leaf, authority] as CFArray,
            SecPolicyCreateBasicX509(),
            &trust
        ) == errSecSuccess, let trust else {
            throw EnrollmentError.invalidCertificateChain
        }
        guard SecTrustSetAnchorCertificates(trust, [authority] as CFArray) == errSecSuccess,
              SecTrustSetAnchorCertificatesOnly(trust, true) == errSecSuccess,
              SecTrustSetNetworkFetchAllowed(trust, false) == errSecSuccess,
              SecTrustSetVerifyDate(trust, date as CFDate) == errSecSuccess
        else {
            throw EnrollmentError.invalidCertificateChain
        }
        let trustedByPinnedAuthority = SecTrustEvaluateWithError(trust, nil)
        return EnrollmentCertificateEvidence(
            leafIsCurrentlyValid: trustedByPinnedAuthority,
            leafIsSignedByPinnedAuthority: trustedByPinnedAuthority,
            publicKeyMatches: fields.subjectPublicKeyInfoDER == expectedPublicKeySPKIDER,
            hasClientAuthenticationEKU: fields.hasClientAuthenticationEKU,
            serial: fields.serial
        )
    }
}
#endif
