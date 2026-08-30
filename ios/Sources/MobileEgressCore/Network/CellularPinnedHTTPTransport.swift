#if canImport(Network) && canImport(Security)
import Foundation
import Network
import Security

public enum CellularTransportError: Error, Equatable {
    case invalidConfiguration
    case identityUnavailable
    case connectionFailed
    case timedOut
}

public final class CellularPinnedHTTPTransport: HTTPTransporting, @unchecked Sendable {
    private let identityResolver: (any SecurityIdentityResolving)?
    private let timeout: TimeInterval

    public init(
        identityResolver: (any SecurityIdentityResolving)? = nil,
        timeout: TimeInterval = 30
    ) {
        self.identityResolver = identityResolver
        self.timeout = timeout
    }

    public func execute(
        _ request: HTTPRequest,
        configuration: PinnedCellularTransportConfiguration
    ) async throws -> HTTPResponse {
        guard try RelayOrigin.parse(request.relayOrigin) == configuration.relayOrigin,
              configuration.requiredInterfaceType == .cellular,
              configuration.prohibitedInterfaceTypes == [.wifi, .wiredEthernet],
              configuration.validatesHostname,
              !configuration.allowsSystemTrustFallback
        else {
            throw CellularTransportError.invalidConfiguration
        }
        let encoded = try HTTP1Codec.encodeRequest(request)
        let parameters = try makeParameters(configuration: configuration)
        guard let port = NWEndpoint.Port(rawValue: UInt16(configuration.port)) else {
            throw CellularTransportError.invalidConfiguration
        }
        let connection = NWConnection(
            host: NWEndpoint.Host(configuration.hostname),
            port: port,
            using: parameters
        )
        return try await withCheckedThrowingContinuation { continuation in
            let exchange = NWHTTPExchange(
                connection: connection,
                request: encoded,
                timeout: timeout,
                continuation: continuation
            )
            exchange.start()
        }
    }

    private func makeParameters(
        configuration: PinnedCellularTransportConfiguration
    ) throws -> NWParameters {
        guard SecCertificateCreateWithData(nil, configuration.pinnedCertificateAuthorityDER as CFData) != nil else {
            throw CellularTransportError.invalidConfiguration
        }
        let tls = NWProtocolTLS.Options()
        sec_protocol_options_set_min_tls_protocol_version(
            tls.securityProtocolOptions,
            tls_protocol_version_t.TLSv13
        )
        sec_protocol_options_set_max_tls_protocol_version(
            tls.securityProtocolOptions,
            tls_protocol_version_t.TLSv13
        )
        sec_protocol_options_set_tls_server_name(tls.securityProtocolOptions, configuration.hostname)
        sec_protocol_options_set_peer_authentication_required(tls.securityProtocolOptions, true)
        let verificationQueue = DispatchQueue(label: "com.mobileegress.agent.tls-verify")
        let authorityDER = configuration.pinnedCertificateAuthorityDER
        let hostname = configuration.hostname
        sec_protocol_options_set_verify_block(tls.securityProtocolOptions, { _, trustObject, complete in
            let trust = sec_trust_copy_ref(trustObject).takeRetainedValue()
            guard let authority = SecCertificateCreateWithData(nil, authorityDER as CFData) else {
                complete(false)
                return
            }
            let policy = SecPolicyCreateSSL(true, hostname as CFString)
            let configured = SecTrustSetPolicies(trust, policy) == errSecSuccess &&
                SecTrustSetAnchorCertificates(trust, [authority] as CFArray) == errSecSuccess &&
                SecTrustSetAnchorCertificatesOnly(trust, true) == errSecSuccess &&
                SecTrustSetNetworkFetchAllowed(trust, false) == errSecSuccess
            complete(configured && SecTrustEvaluateWithError(trust, nil))
        }, verificationQueue)

        if let keyTag = configuration.localIdentityKeyTag {
            guard let identityResolver else { throw CellularTransportError.identityUnavailable }
            let identity = try identityResolver.securityIdentity(forKeyTag: keyTag)
            guard let protocolIdentity = sec_identity_create(identity) else {
                throw CellularTransportError.identityUnavailable
            }
            sec_protocol_options_set_local_identity(tls.securityProtocolOptions, protocolIdentity)
        }

        let tcp = NWProtocolTCP.Options()
        tcp.noDelay = true
        tcp.enableKeepalive = false
        tcp.connectionTimeout = Int(timeout.rounded(.up))
        let parameters = NWParameters(tls: tls, tcp: tcp)
        parameters.requiredInterfaceType = .cellular
        parameters.prohibitedInterfaceTypes = [.wifi, .wiredEthernet]
        parameters.includePeerToPeer = false
        parameters.allowLocalEndpointReuse = false
        parameters.multipathServiceType = .disabled
        parameters.preferNoProxies = true
        return parameters
    }
}

private final class NWHTTPExchange: @unchecked Sendable {
    private let connection: NWConnection
    private let request: Data
    private let timeout: TimeInterval
    private let continuation: CheckedContinuation<HTTPResponse, Error>
    private let queue = DispatchQueue(label: "com.mobileegress.agent.http-exchange")
    private var timer: DispatchSourceTimer?
    private var response = Data()
    private var finished = false
    private var requestSent = false

    init(
        connection: NWConnection,
        request: Data,
        timeout: TimeInterval,
        continuation: CheckedContinuation<HTTPResponse, Error>
    ) {
        self.connection = connection
        self.request = request
        self.timeout = timeout
        self.continuation = continuation
    }

    func start() {
        connection.stateUpdateHandler = { state in
            switch state {
            case .ready:
                self.sendRequest()
            case .failed:
                self.finish(.failure(CellularTransportError.connectionFailed))
            case .cancelled where !self.finished:
                self.finish(.failure(CellularTransportError.connectionFailed))
            default:
                break
            }
        }
        let timer = DispatchSource.makeTimerSource(queue: queue)
        timer.schedule(deadline: .now() + timeout)
        timer.setEventHandler { self.finish(.failure(CellularTransportError.timedOut)) }
        self.timer = timer
        timer.resume()
        connection.start(queue: queue)
    }

    private func sendRequest() {
        guard !requestSent else { return }
        requestSent = true
        connection.send(content: request, contentContext: .defaultMessage, isComplete: true, completion: .contentProcessed { error in
            if error != nil {
                self.finish(.failure(CellularTransportError.connectionFailed))
            } else {
                self.receiveResponse()
            }
        })
    }

    private func receiveResponse() {
        connection.receive(minimumIncompleteLength: 1, maximumLength: 64 * 1024) { content, _, isComplete, error in
            if let content { self.response.append(content) }
            do {
                if let expected = try HTTP1Codec.expectedResponseBytes(in: self.response) {
                    guard self.response.count <= expected else { throw HTTP1Error.ambiguousResponse }
                    if self.response.count == expected {
                        self.finish(.success(try HTTP1Codec.parseResponse(self.response)))
                        return
                    }
                }
            } catch {
                self.finish(.failure(error))
                return
            }
            if error != nil {
                self.finish(.failure(CellularTransportError.connectionFailed))
            } else if isComplete {
                self.finish(.failure(HTTP1Error.truncatedResponse))
            } else {
                self.receiveResponse()
            }
        }
    }

    private func finish(_ result: Result<HTTPResponse, Error>) {
        guard !finished else { return }
        finished = true
        timer?.cancel()
        timer = nil
        connection.stateUpdateHandler = nil
        connection.cancel()
        continuation.resume(with: result)
    }
}
#endif
