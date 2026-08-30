#if canImport(Network) && canImport(Security)
import Foundation
import Network
import Security

struct AppleRelayWebSocketParameterBuilder {
    let identityResolver: any SecurityIdentityResolving
    let timeout: TimeInterval

    func makeTrustPolicy(
        configuration: RelayWebSocketConfiguration
    ) throws -> ApplePinnedServerTrustPolicy {
        try ApplePinnedServerTrustPolicy(
            hostname: configuration.hostname,
            authorityDER: configuration.pinnedCertificateAuthorityDER
        )
    }

    func makeEndpoint(configuration: RelayWebSocketConfiguration) -> NWEndpoint {
        .url(configuration.url)
    }

    func makeParameters(configuration: RelayWebSocketConfiguration) throws -> NWParameters {
        guard configuration.requiredInterfaceType == .cellular,
              configuration.prohibitedInterfaceTypes == [.wifi, .wiredEthernet],
              configuration.validatesHostname,
              configuration.requiresMutualTLS,
              configuration.requiresTLS13,
              !configuration.allowsSystemTrustFallback,
              !configuration.allowsProxyFallback
        else {
            throw CellularTransportError.invalidConfiguration
        }
        let trustPolicy = try makeTrustPolicy(configuration: configuration)
        let tls = NWProtocolTLS.Options()
        sec_protocol_options_set_min_tls_protocol_version(
            tls.securityProtocolOptions,
            tls_protocol_version_t.TLSv13
        )
        sec_protocol_options_set_max_tls_protocol_version(
            tls.securityProtocolOptions,
            tls_protocol_version_t.TLSv13
        )
        sec_protocol_options_set_tls_server_name(
            tls.securityProtocolOptions,
            configuration.hostname
        )
        sec_protocol_options_set_peer_authentication_required(tls.securityProtocolOptions, true)
        let verificationQueue = DispatchQueue(label: "com.mobileegress.agent.websocket-tls-verify")
        sec_protocol_options_set_verify_block(tls.securityProtocolOptions, { _, trustObject, complete in
            let trust = sec_trust_copy_ref(trustObject).takeRetainedValue()
            complete(trustPolicy.evaluate(trust))
        }, verificationQueue)

        let identity = try identityResolver.securityIdentity(forKeyTag: configuration.localIdentityKeyTag)
        guard let protocolIdentity = sec_identity_create(identity) else {
            throw CellularTransportError.identityUnavailable
        }
        sec_protocol_options_set_local_identity(tls.securityProtocolOptions, protocolIdentity)
        return makePathConstrainedParameters(configuration: configuration, tls: tls)
    }

    func makePathConstrainedParameters(
        configuration: RelayWebSocketConfiguration,
        tls: NWProtocolTLS.Options
    ) -> NWParameters {
        let tcp = NWProtocolTCP.Options()
        tcp.noDelay = true
        tcp.enableKeepalive = true
        tcp.connectionTimeout = Int(timeout.rounded(.up))
        let parameters = NWParameters(tls: tls, tcp: tcp)
        let webSocket = NWProtocolWebSocket.Options(.version13)
        webSocket.autoReplyPing = configuration.automaticallyRepliesToWebSocketPings
        webSocket.maximumMessageSize = configuration.maximumMessageBytes
        parameters.defaultProtocolStack.applicationProtocols.insert(webSocket, at: 0)
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

public final class NetworkRelayWebSocket: RelayWebSocketIO, @unchecked Sendable {
    private let configuration: RelayWebSocketConfiguration
    private let builder: AppleRelayWebSocketParameterBuilder
    private let queue = DispatchQueue(label: "com.mobileegress.agent.relay-websocket")
    private var connection: NWConnection?
    private var eventHandler: RelayWebSocketEventHandler?
    private var receiveGate = ReceiveDeliveryGate()
    private var receiveGeneration: ReceiveDeliveryGate.Generation?
    private var pendingTerminalEvent: RelayWebSocketEvent?
    private var pingTimer: DispatchSourceTimer?
    private var ready = false
    private var started = false
    private var finished = false
    private var cleanedUp = false
    private var protocolPingOutstanding = false

    public init(
        configuration: RelayWebSocketConfiguration,
        identityResolver: any SecurityIdentityResolving,
        timeout: TimeInterval = 10
    ) {
        self.configuration = configuration
        builder = AppleRelayWebSocketParameterBuilder(identityResolver: identityResolver, timeout: timeout)
    }

    public func start(eventHandler: @escaping RelayWebSocketEventHandler) {
        queue.async {
            guard !self.started, !self.cleanedUp else { return }
            self.started = true
            self.eventHandler = eventHandler
            self.receiveGeneration = self.receiveGate.beginGeneration()
            do {
                let parameters = try self.builder.makeParameters(configuration: self.configuration)
                let connection = NWConnection(
                    to: self.builder.makeEndpoint(configuration: self.configuration),
                    using: parameters
                )
                self.connection = connection
                connection.stateUpdateHandler = { [weak self] state in
                    self?.handle(state)
                }
                connection.start(queue: self.queue)
            } catch {
                self.finish(with: .failed(self.classifySetup(error)))
            }
        }
    }

    public func sendBinary(
        _ data: Data,
        completion: @escaping RelayWebSocketSendCompletion
    ) -> Bool {
        queue.sync {
            guard ready, !finished, data.count <= configuration.maximumMessageBytes,
                  let connection
            else { return false }
            let metadata = NWProtocolWebSocket.Metadata(opcode: .binary)
            let context = NWConnection.ContentContext(
                identifier: "mobile-egress-wire",
                metadata: [metadata]
            )
            connection.send(
                content: data,
                contentContext: context,
                isComplete: true,
                completion: .contentProcessed { error in
                    Task {
                        if let error {
                            await completion(.failure(Self.classify(error)))
                        } else {
                            await completion(.success(()))
                        }
                    }
                }
            )
            return true
        }
    }

    public func close(code: UInt16, reason: String) {
        queue.async {
            guard !self.cleanedUp else { return }
            self.cleanedUp = true
            self.finished = true
            self.ready = false
            self.protocolPingOutstanding = false
            self.pingTimer?.cancel()
            self.pingTimer = nil
            if let generation = self.receiveGeneration {
                self.receiveGate.invalidate(generation)
            }
            self.pendingTerminalEvent = nil
            guard let connection = self.connection else { return }
            connection.stateUpdateHandler = nil
            let metadata = NWProtocolWebSocket.Metadata(opcode: .close)
            let closeCode: NWProtocolWebSocket.CloseCode = code == 1000
                ? .protocolCode(.normalClosure)
                : .protocolCode(.policyViolation)
            metadata.closeCode = closeCode
            let context = NWConnection.ContentContext(
                identifier: "mobile-egress-close",
                isFinal: true,
                metadata: [metadata]
            )
            connection.send(
                content: Data(reason.utf8),
                contentContext: context,
                isComplete: true,
                completion: .contentProcessed { _ in
                    self.queue.async { self.cancelUnderlyingConnection() }
                }
            )
            self.queue.asyncAfter(deadline: .now() + 1) {
                self.cancelUnderlyingConnection()
            }
        }
    }

    public func cancel() {
        queue.async {
            guard !self.cleanedUp else { return }
            self.cleanedUp = true
            self.finished = true
            self.ready = false
            self.protocolPingOutstanding = false
            self.pingTimer?.cancel()
            self.pingTimer = nil
            if let generation = self.receiveGeneration {
                self.receiveGate.invalidate(generation)
            }
            self.pendingTerminalEvent = nil
            self.connection?.stateUpdateHandler = nil
            self.cancelUnderlyingConnection()
        }
    }

    private func handle(_ state: NWConnection.State) {
        guard !finished else { return }
        switch state {
        case .ready:
            guard !ready,
                  let generation = receiveGeneration,
                  let connection
            else { return }
            ready = true
            startPingTimer()
            guard let delivery = receiveGate.beginDelivery(
                generation,
                resumeReceiving: true
            ) else {
                finish(with: .failed(.unavailable))
                return
            }
            deliver(
                [.connected],
                generation: generation,
                delivery: delivery,
                invalidateAfterDelivery: false,
                sourceConnectionID: ObjectIdentifier(connection)
            )
        case let .failed(error):
            finish(with: .failed(Self.classify(error)))
        case .cancelled:
            finish(with: .closed)
        default:
            break
        }
    }

    private func receiveNext(_ generation: ReceiveDeliveryGate.Generation) {
        guard ready, !finished,
              receiveGeneration == generation,
              let connection,
              receiveGate.beginReceive(generation)
        else { return }
        let connectionID = ObjectIdentifier(connection)
        connection.receiveMessage { [weak self] content, context, isComplete, error in
            guard let self,
                  !self.finished,
                  self.receiveGeneration == generation,
                  self.connection.map({ ObjectIdentifier($0) }) == connectionID
            else { return }
            if let error {
                self.receiveGate.abandonReceive(generation)
                self.finish(with: .failed(Self.classify(error)))
                return
            }
            let payload = content ?? Data()
            let metadata = context?.protocolMetadata(
                definition: NWProtocolWebSocket.definition
            ) as? NWProtocolWebSocket.Metadata
            let opcode: RelayWebSocketOpcode
            switch metadata?.opcode {
            case .binary: opcode = .binary
            case .text: opcode = .text
            case .cont: opcode = .continuation
            case .ping: opcode = .ping
            case .pong: opcode = .pong
            case .close: opcode = .close
            case nil: opcode = .unknown
            @unknown default: opcode = .unknown
            }
            if opcode == .close {
                self.finished = true
                self.ready = false
                self.protocolPingOutstanding = false
                self.pingTimer?.cancel()
                self.pingTimer = nil
                self.connection?.stateUpdateHandler = nil
                self.receiveGate.stopReceiving(generation)
            }
            guard let delivery = self.receiveGate.completeReceive(
                generation,
                resumeReceiving: opcode != .close
            ) else { return }
            self.deliver(
                [.message(.init(opcode: opcode, payload: payload, isComplete: isComplete))],
                generation: generation,
                delivery: delivery,
                invalidateAfterDelivery: opcode == .close,
                sourceConnectionID: connectionID
            )
        }
    }

    private func startPingTimer() {
        let timer = DispatchSource.makeTimerSource(queue: queue)
        timer.schedule(deadline: .now() + 20, repeating: 20)
        timer.setEventHandler { [weak self] in
            self?.sendProtocolPing()
        }
        pingTimer = timer
        timer.resume()
    }

    private func sendProtocolPing() {
        guard ready, !finished, let connection else { return }
        guard !protocolPingOutstanding else {
            finish(with: .failed(.unavailable))
            return
        }
        protocolPingOutstanding = true
        let metadata = NWProtocolWebSocket.Metadata(opcode: .ping)
        metadata.setPongHandler(queue) { [weak self] error in
            guard let self else { return }
            if let error {
                self.finish(with: .failed(Self.classify(error)))
            } else {
                self.protocolPingOutstanding = false
            }
        }
        let context = NWConnection.ContentContext(
            identifier: "mobile-egress-websocket-ping",
            metadata: [metadata]
        )
        connection.send(
            content: Data(),
            contentContext: context,
            isComplete: true,
            completion: .contentProcessed { [weak self] error in
                if let error { self?.finish(with: .failed(Self.classify(error))) }
            }
        )
    }

    private func finish(with event: RelayWebSocketEvent) {
        guard !finished else { return }
        finished = true
        ready = false
        protocolPingOutstanding = false
        pingTimer?.cancel()
        pingTimer = nil
        connection?.stateUpdateHandler = nil
        if let generation = receiveGeneration {
            receiveGate.stopReceiving(generation)
            receiveGate.abandonReceive(generation)
        }
        queueTerminal(event)
    }

    private func cancelUnderlyingConnection() {
        guard let connection else { return }
        self.connection = nil
        connection.stateUpdateHandler = nil
        connection.cancel()
    }

    private func queueTerminal(_ event: RelayWebSocketEvent) {
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
            invalidateAfterDelivery: true,
            sourceConnectionID: nil
        )
    }

    private func deliver(
        _ events: [RelayWebSocketEvent],
        generation: ReceiveDeliveryGate.Generation,
        delivery: ReceiveDeliveryGate.Delivery,
        invalidateAfterDelivery: Bool,
        sourceConnectionID: ObjectIdentifier?
    ) {
        guard let eventHandler else {
            deliveryCompleted(
                generation: generation,
                delivery: delivery,
                invalidateAfterDelivery: invalidateAfterDelivery,
                sourceConnectionID: sourceConnectionID
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
                    invalidateAfterDelivery: invalidateAfterDelivery,
                    sourceConnectionID: sourceConnectionID
                )
            }
        }
    }

    private func deliveryCompleted(
        generation: ReceiveDeliveryGate.Generation,
        delivery: ReceiveDeliveryGate.Delivery,
        invalidateAfterDelivery: Bool,
        sourceConnectionID: ObjectIdentifier?
    ) {
        let shouldResume = receiveGate.completeDelivery(delivery)
        if invalidateAfterDelivery {
            receiveGate.invalidate(generation)
            pendingTerminalEvent = nil
            if !cleanedUp {
                cleanedUp = true
                cancelUnderlyingConnection()
            }
            return
        }
        if let terminal = pendingTerminalEvent {
            pendingTerminalEvent = nil
            queueTerminal(terminal)
            return
        }
        if shouldResume,
           let sourceConnectionID,
           connection.map({ ObjectIdentifier($0) }) == sourceConnectionID,
           ready,
           !finished,
           receiveGeneration == generation {
            receiveNext(generation)
        }
    }

    private func classifySetup(_ error: Error) -> RelayConnectionFailure {
        if error is IdentityError {
            return .authentication
        }
        if let transportError = error as? CellularTransportError,
           transportError == .identityUnavailable {
            return .authentication
        }
        return .tls
    }

    private static func classify(_ error: NWError) -> RelayConnectionFailure {
        if case .tls = error { return .tls }
        return .unavailable
    }
}
#endif
