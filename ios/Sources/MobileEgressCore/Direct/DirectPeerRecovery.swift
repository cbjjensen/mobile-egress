import Foundation

/// Finite, secret-free recovery guidance. It never contains transport error text,
/// hostnames, certificates, capabilities or keys.
public enum DirectPeerRecovery: String, Sendable, Equatable {
    case retrying, clientStorageUnavailable, unlockRequired, pairingRequired, clockCheckRequired
    public var automaticallyRetries: Bool { self == .retrying || self == .clientStorageUnavailable }
    public var guidance: String {
        switch self {
        case .retrying: "Retrying — check cellular access, Client address and device clocks. Import an updated address; if Client trust was replaced, remove and pair again."
        case .clientStorageUnavailable: "Client secure storage unavailable — unlock or repair storage on the workload Client. Retrying automatically."
        case .unlockRequired: "Phone secure storage unavailable — unlock this iPhone, then tap Retry. Pairing is saved."
        case .pairingRequired: "Pairing needs attention — check device clocks. If credentials expired, were revoked or lost, remove this record and scan a fresh Client invitation."
        case .clockCheckRequired: "Credentials are not yet valid — correct the phone and Client clocks, then tap Retry."
        }
    }
    public static func classify(_ error: any Error) -> Self {
        switch error {
        case DirectAgentError.authorizationDenied, DirectAgentError.credentialExpired,
             IdentityError.keyMissing, IdentityError.invalidIdentity, IdentityError.identityUnavailable:
            .pairingRequired
        case DirectAgentError.credentialNotYetValid: .clockCheckRequired
        case DirectAgentError.persistence, IdentityError.secureStorageUnavailable,
             IdentityError.persistenceFailed, IdentityError.certificatePersistenceFailed,
             IdentityError.identityLookupFailed, IdentityError.keyCreationFailed: .unlockRequired
        case DirectAgentError.clientStorageUnavailable: .clientStorageUnavailable
        default: .retrying
        }
    }
}

#if canImport(Security)
enum DirectLocalIdentityReadiness {
    static func validate(_ identity: AgentIdentity, at date: Date) throws {
        let certificates: [(notBefore: Date, notAfter: Date)]
        do {
            guard try PEMCertificateChain.parse(identity.caCertificatePEM) == [identity.caCertificateDER] else { throw IdentityError.invalidIdentity }
            certificates = try [identity.certificatePEM, identity.caCertificatePEM].map(DirectCertificateExpiration.validity)
        } catch { throw IdentityError.invalidIdentity }
        for validity in certificates {
            if date < validity.notBefore { throw DirectAgentError.credentialNotYetValid }
            if date >= validity.notAfter { throw DirectAgentError.credentialExpired }
        }
    }
}
#endif
