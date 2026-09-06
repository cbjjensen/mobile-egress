#if canImport(Network)
import Foundation
import Network

enum NetworkTargetConfigurationError: Error {
    case invalidConfiguration
}

struct AppleTargetConnectionParameterBuilder {
    func makeEndpoint(configuration: TargetConnectionConfiguration, ipLiteral: String? = nil) throws -> NWEndpoint {
        guard let port = NWEndpoint.Port(rawValue: UInt16(configuration.port)) else {
            throw NetworkTargetConfigurationError.invalidConfiguration
        }
        let host: NWEndpoint.Host
        if let address = IPv4Address(ipLiteral ?? configuration.ipLiteral) {
            host = .ipv4(address)
        } else if let address = IPv6Address(ipLiteral ?? configuration.ipLiteral) {
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
            endpoints: try configuration.ipLiterals.map { try builder.makeEndpoint(configuration: configuration, ipLiteral: $0) },
            parameters: builder.makeParameters(configuration: configuration),
            readChunkBytes: configuration.readChunkBytes,
            connectTimeout: configuration.connectTimeout
        )
    }
}

private final class NetworkTargetConnection: TargetConnectionIO, @unchecked Sendable {
    private var connection: NWConnection
    private let endpoints: [NWEndpoint]
    private let parameters: NWParameters
    private var dialSequence: TargetDialSequence
    private var attemptIndex = 0
    private let readChunkBytes: Int
    private let queue = DispatchQueue(label: "com.mobileegress.agent.target-connection")
    private var eventHandler: TargetConnectionEventHandler?
    private var receiveGate = ReceiveDeliveryGate()
    private var receiveGeneration: ReceiveDeliveryGate.Generation?
    private var pendingTerminalEvent: TargetConnectionEvent?
    private var lifecycle = TargetDuplexLifecycle()
    private var timeoutTimer: DispatchSourceTimer?
    private var started = false

    init(
        endpoints: [NWEndpoint],
        parameters: NWParameters,
        readChunkBytes: Int,
        connectTimeout: TimeInterval
    ) {
        self.endpoints = endpoints
        self.parameters = parameters
        connection = NWConnection(to: endpoints[0], using: parameters)
        dialSequence = TargetDialSequence(candidateCount: endpoints.count, timeout: connectTimeout)
        self.readChunkBytes = readChunkBytes
    }

    func start(eventHandler: @escaping TargetConnectionEventHandler) {
        queue.async {
            guard !self.started, !self.lifecycle.isTerminal else { return }
            self.started = true
            self.eventHandler = eventHandler
            self.receiveGeneration = self.receiveGate.beginGeneration()
            guard let attempt = self.dialSequence.start(now: ProcessInfo.processInfo.systemUptime) else {
                self.fail()
                return
            }
            self.startAttempt(attempt)
        }
    }

    private func startAttempt(_ attempt: TargetDialSequence.Attempt) {
        attemptIndex = attempt.index
        connection.stateUpdateHandler = { [weak self] state in
            guard let self, self.attemptIndex == attempt.index else { return }
            self.handle(state)
        }
        let timer = DispatchSource.makeTimerSource(queue: queue)
        timer.schedule(deadline: .now() + attempt.timeout)
        timer.setEventHandler { [weak self] in
            guard let self, self.attemptIndex == attempt.index, self.dialSequence.isConnecting else { return }
            self.retryOrFail()
        }
        timeoutTimer = timer
        timer.resume()
        connection.start(queue: queue)
    }

    private func retryOrFail() {
        guard !lifecycle.isTerminal else { return }
        guard let next = dialSequence.failed(attemptIndex, now: ProcessInfo.processInfo.systemUptime) else {
            fail()
            return
        }
        timeoutTimer?.cancel()
        timeoutTimer = nil
        connection.stateUpdateHandler = nil
        connection.cancel()
        connection = NWConnection(to: endpoints[next.index], using: parameters)
        startAttempt(next)
    }

    func send(_ data: Data, completion: @escaping TargetConnectionSendCompletion) -> Bool {
        queue.sync {
            guard lifecycle.canWrite else { return false }
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
            guard self.lifecycle.cancel() else { return }
            self.dialSequence.cancel()
            if let generation = self.receiveGeneration {
                self.receiveGate.invalidate(generation)
            }
            self.pendingTerminalEvent = nil
            self.timeoutTimer?.cancel()
            self.timeoutTimer = nil
            self.connection.stateUpdateHandler = nil
            self.connection.cancel()
        }
    }

    private func handle(_ state: NWConnection.State) {
        guard !lifecycle.isTerminal else { return }
        switch state {
        case .ready:
            guard dialSequence.isConnecting else { return }
            guard dialSequence.ready(attemptIndex, now: ProcessInfo.processInfo.systemUptime) else {
                retryOrFail()
                return
            }
            guard lifecycle.markReady(), let generation = receiveGeneration else { return }
            timeoutTimer?.cancel()
            timeoutTimer = nil
            guard let delivery = receiveGate.beginDelivery(
                generation,
                resumeReceiving: true
            ) else {
                fail()
                return
            }
            deliver(
                [.ready],
                generation: generation,
                delivery: delivery,
                invalidateAfterDelivery: false
            )
        case .failed, .cancelled:
            retryOrFail()
        default:
            break
        }
    }

    private func receiveNext(_ generation: ReceiveDeliveryGate.Generation) {
        guard receiveGeneration == generation,
              lifecycle.canRead,
              receiveGate.beginReceive(generation)
        else { return }
        connection.receive(minimumIncompleteLength: 1, maximumLength: readChunkBytes) {
            [weak self] content, _, isComplete, error in
            guard let self,
                  self.receiveGeneration == generation,
                  !self.lifecycle.isTerminal
            else { return }
            if error != nil {
                self.receiveGate.abandonReceive(generation)
                self.fail()
                return
            }
            let events = self.lifecycle.receive(content: content, isComplete: isComplete)
            let resumeReceiving = self.lifecycle.canRead
            if !resumeReceiving {
                self.receiveGate.stopReceiving(generation)
            }
            guard let delivery = self.receiveGate.completeReceive(
                generation,
                resumeReceiving: resumeReceiving
            ) else { return }
            self.deliver(
                events,
                generation: generation,
                delivery: delivery,
                invalidateAfterDelivery: false
            )
        }
    }

    private func fail() {
        guard let event = lifecycle.fail() else { return }
        dialSequence.cancel()
        timeoutTimer?.cancel()
        timeoutTimer = nil
        connection.stateUpdateHandler = nil
        if let generation = receiveGeneration {
            receiveGate.stopReceiving(generation)
            receiveGate.abandonReceive(generation)
        }
        queueTerminal(event)
    }

    private func queueTerminal(_ event: TargetConnectionEvent) {
        guard let generation = receiveGeneration else { return }
        if receiveGate.hasDeliveryOutstanding {
            pendingTerminalEvent = event
            return
        }
        guard let delivery = receiveGate.beginDelivery(
            generation,
            resumeReceiving: false
        ) else { return }
        deliver(
            [event],
            generation: generation,
            delivery: delivery,
            invalidateAfterDelivery: true
        )
    }

    private func deliver(
        _ events: [TargetConnectionEvent],
        generation: ReceiveDeliveryGate.Generation,
        delivery: ReceiveDeliveryGate.Delivery,
        invalidateAfterDelivery: Bool
    ) {
        guard let eventHandler else {
            deliveryCompleted(
                generation: generation,
                delivery: delivery,
                invalidateAfterDelivery: invalidateAfterDelivery
            )
            return
        }
        Task { [weak self] in
            for event in events {
                await eventHandler(event)
            }
            self?.queue.async { [weak self] in
                self?.deliveryCompleted(
                    generation: generation,
                    delivery: delivery,
                    invalidateAfterDelivery: invalidateAfterDelivery
                )
            }
        }
    }

    private func deliveryCompleted(
        generation: ReceiveDeliveryGate.Generation,
        delivery: ReceiveDeliveryGate.Delivery,
        invalidateAfterDelivery: Bool
    ) {
        let shouldResume = receiveGate.completeDelivery(delivery)
        if invalidateAfterDelivery {
            receiveGate.invalidate(generation)
            pendingTerminalEvent = nil
            if lifecycle.cancel() {
                connection.stateUpdateHandler = nil
                connection.cancel()
            }
            return
        }
        if let terminal = pendingTerminalEvent {
            pendingTerminalEvent = nil
            queueTerminal(terminal)
            return
        }
        if shouldResume,
           receiveGeneration == generation,
           lifecycle.canRead {
            receiveNext(generation)
        }
    }
}
#endif
