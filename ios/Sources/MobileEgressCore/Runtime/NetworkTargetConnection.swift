#if canImport(Network)
import Foundation
import Network

enum NetworkTargetConfigurationError: Error {
    case invalidConfiguration
}

struct AppleTargetConnectionParameterBuilder {
    func makeEndpoint(configuration: TargetConnectionConfiguration) throws -> NWEndpoint {
        guard let port = NWEndpoint.Port(rawValue: UInt16(configuration.port)) else {
            throw NetworkTargetConfigurationError.invalidConfiguration
        }
        let host: NWEndpoint.Host
        if let address = IPv4Address(configuration.ipLiteral) {
            host = .ipv4(address)
        } else if let address = IPv6Address(configuration.ipLiteral) {
            host = .ipv6(address)
        } else {
            throw NetworkTargetConfigurationError.invalidConfiguration
        }
        return .hostPort(host: host, port: port)
    }

    func makeParameters(configuration: TargetConnectionConfiguration) -> NWParameters {
        let tcp = NWProtocolTCP.Options()
        tcp.noDelay = true
        tcp.enableKeepalive = false
        tcp.connectionTimeout = Int(configuration.connectTimeout.rounded(.up))
        let parameters = NWParameters(tls: nil, tcp: tcp)
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

public struct NetworkTargetConnectionFactory: TargetConnectionFactory, Sendable {
    public init() {}

    public func makeConnection(
        configuration: TargetConnectionConfiguration
    ) throws -> any TargetConnectionIO {
        guard configuration.requiredInterfaceType == .cellular,
              configuration.prohibitedInterfaceTypes == [.wifi, .wiredEthernet],
              !configuration.allowsProxyFallback
        else {
            throw NetworkTargetConfigurationError.invalidConfiguration
        }
        let builder = AppleTargetConnectionParameterBuilder()
        return NetworkTargetConnection(
            endpoint: try builder.makeEndpoint(configuration: configuration),
            parameters: builder.makeParameters(configuration: configuration),
            readChunkBytes: configuration.readChunkBytes,
            connectTimeout: configuration.connectTimeout
        )
    }
}

private final class NetworkTargetConnection: TargetConnectionIO, @unchecked Sendable {
    private let connection: NWConnection
    private let readChunkBytes: Int
    private let connectTimeout: TimeInterval
    private let queue = DispatchQueue(label: "com.mobileegress.agent.target-connection")
    private var eventHandler: TargetConnectionEventHandler?
    private var deliveryTask: Task<Void, Never>?
    private var timeoutTimer: DispatchSourceTimer?
    private var started = false
    private var ready = false
    private var finished = false
    private var canceled = false

    init(
        endpoint: NWEndpoint,
        parameters: NWParameters,
        readChunkBytes: Int,
        connectTimeout: TimeInterval
    ) {
        connection = NWConnection(to: endpoint, using: parameters)
        self.readChunkBytes = readChunkBytes
        self.connectTimeout = connectTimeout
    }

    func start(eventHandler: @escaping TargetConnectionEventHandler) {
        queue.async {
            guard !self.started, !self.canceled else { return }
            self.started = true
            self.eventHandler = eventHandler
            self.connection.stateUpdateHandler = { [weak self] state in
                self?.handle(state)
            }
            let timer = DispatchSource.makeTimerSource(queue: self.queue)
            timer.schedule(deadline: .now() + self.connectTimeout)
            timer.setEventHandler { [weak self] in
                self?.fail()
            }
            self.timeoutTimer = timer
            timer.resume()
            self.connection.start(queue: self.queue)
        }
    }

    func send(_ data: Data, completion: @escaping TargetConnectionSendCompletion) -> Bool {
        queue.sync {
            guard ready, !finished, !canceled else { return false }
            connection.send(
                content: data,
                contentContext: .defaultMessage,
                isComplete: true,
                completion: .contentProcessed { error in
                    Task {
                        if error == nil {
                            await completion(.success(()))
                        } else {
                            await completion(.failure(.failed))
                        }
                    }
                }
            )
            return true
        }
    }

    func cancel() {
        queue.async {
            guard !self.canceled else { return }
            self.canceled = true
            self.ready = false
            self.finished = true
            self.timeoutTimer?.cancel()
            self.timeoutTimer = nil
            self.connection.stateUpdateHandler = nil
            self.connection.cancel()
        }
    }

    private func handle(_ state: NWConnection.State) {
        guard !finished, !canceled else { return }
        switch state {
        case .ready:
            guard !ready else { return }
            ready = true
            timeoutTimer?.cancel()
            timeoutTimer = nil
            emit(.ready)
            receiveNext()
        case .failed, .cancelled:
            fail()
        default:
            break
        }
    }

    private func receiveNext() {
        guard ready, !finished, !canceled else { return }
        connection.receive(minimumIncompleteLength: 1, maximumLength: readChunkBytes) {
            [weak self] content, _, isComplete, error in
            guard let self, !self.finished, !self.canceled else { return }
            if error != nil {
                self.fail()
                return
            }
            if let content, !content.isEmpty {
                self.emit(.data(content))
            }
            if isComplete {
                self.finished = true
                self.ready = false
                self.emit(.ended)
            } else {
                self.receiveNext()
            }
        }
    }

    private func fail() {
        guard !finished, !canceled else { return }
        finished = true
        ready = false
        timeoutTimer?.cancel()
        timeoutTimer = nil
        emit(.failed)
    }

    private func emit(_ event: TargetConnectionEvent) {
        guard let eventHandler else { return }
        let preceding = deliveryTask
        deliveryTask = Task {
            await preceding?.value
            await eventHandler(event)
        }
    }
}
#endif
