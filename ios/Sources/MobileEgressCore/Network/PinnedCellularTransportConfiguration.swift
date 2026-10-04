import Foundation

public enum TransportInterfaceType: String, Hashable, Sendable {
    case cellular
    case wifi
    case wiredEthernet
}

struct RelayEndpoint: Equatable, Sendable {
    let origin: String
    let hostname: String
    let port: Int
    let hostHeader: String

    init(origin: String) throws {
        let normalized = try RelayOrigin.parse(origin)
        guard let components = URLComponents(string: normalized),
              let host = components.host,
              !host.isEmpty
        else {
            throw CoreValidationError.invalidRelayOrigin
        }
        let port = components.port ?? 443
        guard (1 ... 65_535).contains(port) else { throw CoreValidationError.invalidRelayOrigin }
        // URLComponents.host retains the brackets around an IPv6 literal.
        // Network.framework and certificate hostname policies require the bare IP.
        let hostname = host.first == "[" && host.last == "]" ? String(host.dropFirst().dropLast()) : host
        let headerHost = hostname.contains(":") ? "[\(hostname)]" : hostname
        self.origin = normalized
        self.hostname = hostname
        self.port = port
        hostHeader = port == 443 ? headerHost : "\(headerHost):\(port)"
    }
}

public struct PinnedCellularTransportConfiguration: Equatable, Sendable {
    public let relayOrigin: String
    public let hostname: String
    public let port: Int
    public let pinnedCertificateAuthorityDER: Data
    public let localIdentityKeyTag: String?
    public let requiredInterfaceType: TransportInterfaceType = .cellular
    public let prohibitedInterfaceTypes: Set<TransportInterfaceType> = [.wifi, .wiredEthernet]
    public let validatesHostname = true
    public let allowsSystemTrustFallback = false

    public init(
        relayOrigin: String,
        pinnedCertificateAuthorityDER: Data,
        identity: AgentIdentity?
    ) throws {
        let endpoint = try RelayEndpoint(origin: relayOrigin)
        guard !pinnedCertificateAuthorityDER.isEmpty else {
            throw CoreValidationError.certificateAuthorityInvalid
        }
        self.relayOrigin = endpoint.origin
        hostname = endpoint.hostname
        port = endpoint.port
        self.pinnedCertificateAuthorityDER = pinnedCertificateAuthorityDER
        localIdentityKeyTag = identity?.keyTag
    }
}
