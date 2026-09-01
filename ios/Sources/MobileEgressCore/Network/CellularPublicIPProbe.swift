#if canImport(Network) && canImport(Security)
import Foundation
import Network
import Security

struct CellularPublicIPProbeRequest: Equatable, Sendable {
    let family: PublicIPFamily
    let hostname: String
    let port: Int
    let timeout: TimeInterval
    let maximumBodyBytes: Int
    let httpRequest: Data
}

protocol CellularPublicIPFamilyRequesting: Sendable {
    func execute(_ request: CellularPublicIPProbeRequest) async throws -> Data
}

struct AppleCellularPublicIPProbeRequestBuilder: Sendable {
    let timeout: TimeInterval
    let maximumBodyBytes: Int

    init(timeout: TimeInterval = 8, maximumBodyBytes: Int = 128) {
        self.timeout = timeout
        self.maximumBodyBytes = maximumBodyBytes
    }

    func makeRequest(for family: PublicIPFamily) -> CellularPublicIPProbeRequest {
        let hostname = switch family {
        case .ipv4: "api.ipify.org"
        case .ipv6: "api6.ipify.org"
        }
        return CellularPublicIPProbeRequest(
            family: family,
            hostname: hostname,
            port: 443,
            timeout: timeout,
            maximumBodyBytes: maximumBodyBytes,
            httpRequest: Data(
                "GET / HTTP/1.1\r\nHost: \(hostname)\r\nAccept: text/plain\r\nConnection: close\r\n\r\n".utf8
            )
        )
    }

    func makeParameters() -> NWParameters {
        let tls = NWProtocolTLS.Options()
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
        if #available(iOS 17.0, macOS 14.0, *) {
            parameters.preferNoProxies = true
        }
        return parameters
    }
}

public final class CellularPublicIPProbe: CellularPublicIPProbing, @unchecked Sendable {
    private let requester: any CellularPublicIPFamilyRequesting
    private let logger: any CellularNetworkDiagnosticLogging
    private let requestBuilder: AppleCellularPublicIPProbeRequestBuilder

    public convenience init() {
        let builder = AppleCellularPublicIPProbeRequestBuilder()
        self.init(
            requester: NWCellularPublicIPFamilyRequester(requestBuilder: builder),
            logger: AppleUnifiedNetworkDiagnosticLogger(),
            requestBuilder: builder
        )
    }

    init(
        requester: any CellularPublicIPFamilyRequesting,
        logger: any CellularNetworkDiagnosticLogging,
        requestBuilder: AppleCellularPublicIPProbeRequestBuilder = .init()
    ) {
        self.requester = requester
        self.logger = logger
        self.requestBuilder = requestBuilder
    }

    public func probe() async -> PublicIPSnapshot {
        await withTaskGroup(of: (PublicIPFamily, String?).self) { group in
            for family in PublicIPFamily.allCases {
                group.addTask { [requester, logger, requestBuilder] in
                    let request = requestBuilder.makeRequest(for: family)
                    do {
                        let response = try await requester.execute(request)
                        let value = try CellularPublicIPHTTPResponseParser.parse(
                            response,
                            family: family,
                            maximumBodyBytes: request.maximumBodyBytes
                        )
                        return (family, value)
                    } catch {
                        let failure: PublicIPProbeFailure
                        if error is CancellationError || Task.isCancelled {
                            failure = PublicIPProbeFailure(.cancelled)
                        } else if let classified = error as? PublicIPProbeFailure {
                            failure = classified
                        } else {
                            failure = PublicIPProbeFailure(.unavailable)
                        }
                        logger.record(
                            SafeNetworkDiagnostic(
                                component: .publicIPProbe,
                                family: family,
                                failure: failure.classification,
                                httpStatus: failure.httpStatus
                            )
                        )
                        return (family, nil)
                    }
                }
            }

            var ipv4: String?
            var ipv6: String?
            for await (family, value) in group {
                switch family {
                case .ipv4: ipv4 = value
                case .ipv6: ipv6 = value
                }
            }
            return PublicIPSnapshot(ipv4: ipv4, ipv6: ipv6)
        }
    }
}

private struct NWCellularPublicIPFamilyRequester: CellularPublicIPFamilyRequesting {
    let requestBuilder: AppleCellularPublicIPProbeRequestBuilder

    func execute(_ request: CellularPublicIPProbeRequest) async throws -> Data {
        guard let port = NWEndpoint.Port(rawValue: UInt16(request.port)) else {
            throw PublicIPProbeFailure(.unavailable)
        }
        let connection = NWConnection(
            host: NWEndpoint.Host(request.hostname),
            port: port,
            using: requestBuilder.makeParameters()
        )
        let exchange = NWCellularPublicIPExchange(connection: connection, request: request)
        return try await exchange.execute()
    }
}

private final class NWCellularPublicIPExchange: @unchecked Sendable {
    private let connection: NWConnection
    private let request: CellularPublicIPProbeRequest
    private let queue = DispatchQueue(label: "com.mobileegress.agent.public-ip-probe")
    private var continuation: CheckedContinuation<Data, any Error>?
    private var timer: DispatchSourceTimer?
    private var response = Data()
    private var started = false
    private var finished = false
    private var requestSent = false

    init(connection: NWConnection, request: CellularPublicIPProbeRequest) {
        self.connection = connection
        self.request = request
    }

    func execute() async throws -> Data {
        try await withTaskCancellationHandler {
            try await withCheckedThrowingContinuation { continuation in
                queue.async { self.start(continuation: continuation) }
            }
        } onCancel: {
            queue.async { self.finish(.failure(PublicIPProbeFailure(.cancelled))) }
        }
    }

    private func start(continuation: CheckedContinuation<Data, any Error>) {
        guard !started else {
            continuation.resume(throwing: PublicIPProbeFailure(.unavailable))
            return
        }
        started = true
        self.continuation = continuation
        connection.stateUpdateHandler = { [weak self] state in
            self?.handle(state)
        }
        let timer = DispatchSource.makeTimerSource(queue: queue)
        timer.schedule(deadline: .now() + request.timeout)
        timer.setEventHandler { [weak self] in
            self?.finish(.failure(PublicIPProbeFailure(.timedOut)))
        }
        self.timer = timer
        timer.resume()
        connection.start(queue: queue)
    }

    private func handle(_ state: NWConnection.State) {
        guard !finished else { return }
        switch state {
        case .ready:
            sendRequest()
        case let .failed(error):
            finish(.failure(Self.classify(error)))
        case .cancelled:
            finish(.failure(PublicIPProbeFailure(.cancelled)))
        default:
            break
        }
    }

    private func sendRequest() {
        guard !requestSent else { return }
        requestSent = true
        connection.send(
            content: request.httpRequest,
            contentContext: .defaultMessage,
            isComplete: true,
            completion: .contentProcessed { [weak self] error in
                guard let self else { return }
                if let error {
                    self.finish(.failure(Self.classify(error)))
                } else {
                    self.receiveResponse()
                }
            }
        )
    }

    private func receiveResponse() {
        connection.receive(minimumIncompleteLength: 1, maximumLength: 1024) {
            [weak self] content, _, isComplete, error in
            guard let self, !self.finished else { return }
            if let content {
                self.response.append(content)
            }
            let maximumResponseBytes = CellularPublicIPHTTPResponseParser.maximumHeaderBytes
                + 4 + self.request.maximumBodyBytes
            guard self.response.count <= maximumResponseBytes else {
                self.finish(.failure(PublicIPProbeFailure(.responseTooLarge)))
                return
            }
            if let error {
                self.finish(.failure(Self.classify(error)))
                return
            }
            if isComplete {
                do {
                    _ = try CellularPublicIPHTTPResponseParser.parse(
                        self.response,
                        family: self.request.family,
                        maximumBodyBytes: self.request.maximumBodyBytes
                    )
                    self.finish(.success(self.response))
                } catch {
                    self.finish(.failure(error))
                }
            } else {
                self.receiveResponse()
            }
        }
    }

    private func finish(_ result: Result<Data, any Error>) {
        guard !finished else { return }
        finished = true
        timer?.cancel()
        timer = nil
        connection.stateUpdateHandler = nil
        connection.cancel()
        continuation?.resume(with: result)
        continuation = nil
    }

    private static func classify(_ error: NWError) -> PublicIPProbeFailure {
        if case .tls = error {
            return PublicIPProbeFailure(.tls)
        }
        return PublicIPProbeFailure(.unavailable)
    }
}
#endif
