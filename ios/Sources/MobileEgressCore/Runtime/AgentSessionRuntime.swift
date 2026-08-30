import Foundation

public actor AgentSessionRuntime {
    private struct TargetHandle {
        let token: UInt64
        let connection: any TargetConnectionIO
    }

    private let relay: any RelayWebSocketIO
    private let targetFactory: any TargetConnectionFactory
    private var machine = AgentSessionStateMachine()
    private var targets: [String: TargetHandle] = [:]
    private var outboundInFlight: OutboundFrame?

    public init(relay: any RelayWebSocketIO, targetFactory: any TargetConnectionFactory) {
        self.relay = relay
        self.targetFactory = targetFactory
    }

    public func start() {
        process(machine.start())
    }

    public func stop() {
        process(machine.stop())
    }

    public func snapshot() -> AgentRuntimeSnapshot {
        machine.snapshot
    }

    private func handleRelay(_ event: RelayWebSocketEvent) {
        switch event {
        case .connected:
            machine.relayConnected()
            pumpOutbound()
        case let .message(message):
            process(machine.receiveRelay(message))
        case .closed:
            process(machine.relayClosed())
        case let .failed(failure):
            process(machine.relayFailed(failure))
        }
    }

    private func handleTarget(
        streamID: String,
        token: UInt64,
        event: TargetConnectionEvent
    ) {
        let effects: [AgentRuntimeEffect]
        switch event {
        case .ready:
            effects = machine.targetConnected(streamID: streamID, token: token)
        case let .data(data):
            effects = machine.targetReceived(streamID: streamID, token: token, data: data)
        case .ended:
            effects = machine.targetEnded(streamID: streamID, token: token)
        case .failed:
            effects = machine.targetFailed(streamID: streamID, token: token)
        }
        process(effects)
    }

    private func handleTargetWrite(
        streamID: String,
        token: UInt64,
        writeID: UInt64,
        result: Result<Void, TargetConnectionFailure>
    ) {
        process(machine.targetWriteCompleted(
            streamID: streamID,
            token: token,
            writeID: writeID,
            succeeded: result.isSuccess
        ))
    }

    private func handleRelaySend(
        frameID: UInt64,
        result: Result<Void, RelayConnectionFailure>
    ) {
        guard let frame = outboundInFlight, frame.id == frameID else { return }
        outboundInFlight = nil
        process(machine.completeOutbound(frame, accepted: result.isSuccess))
    }

    private func process(_ initialEffects: [AgentRuntimeEffect]) {
        var effects = initialEffects
        while !effects.isEmpty {
            let effect = effects.removeFirst()
            switch effect {
            case .startRelay:
                relay.start { [weak self] event in
                    await self?.handleRelay(event)
                }
            case let .createTarget(streamID, token, configuration):
                do {
                    let connection = try targetFactory.makeConnection(configuration: configuration)
                    targets[streamID] = TargetHandle(token: token, connection: connection)
                    machine.targetWasCreated(streamID: streamID, token: token)
                    connection.start { [weak self] event in
                        await self?.handleTarget(streamID: streamID, token: token, event: event)
                    }
                } catch {
                    effects.append(contentsOf: machine.targetCreationFailed(streamID: streamID, token: token))
                }
            case let .writeTarget(streamID, token, writeID, data):
                guard let target = targets[streamID], target.token == token else {
                    effects.append(contentsOf: machine.targetWriteCompleted(
                        streamID: streamID,
                        token: token,
                        writeID: writeID,
                        succeeded: false
                    ))
                    continue
                }
                let accepted = target.connection.send(data) { [weak self] result in
                    await self?.handleTargetWrite(
                        streamID: streamID,
                        token: token,
                        writeID: writeID,
                        result: result
                    )
                }
                if !accepted {
                    effects.append(contentsOf: machine.targetWriteCompleted(
                        streamID: streamID,
                        token: token,
                        writeID: writeID,
                        succeeded: false
                    ))
                }
            case let .cancelTarget(streamID, token):
                guard let target = targets[streamID], target.token == token else { continue }
                targets.removeValue(forKey: streamID)
                target.connection.cancel()
            case let .closeRelay(code, reason):
                relay.close(code: code, reason: reason)
            case .cancelRelay:
                relay.cancel()
            }
        }

        if machine.snapshot.connectionState == .stopping {
            outboundInFlight = nil
            machine.finishStopping()
            return
        }
        pumpOutbound()
    }

    private func pumpOutbound() {
        guard outboundInFlight == nil, let frame = machine.nextOutbound() else { return }
        outboundInFlight = frame
        let accepted = relay.sendBinary(frame.bytes) { [weak self] result in
            await self?.handleRelaySend(frameID: frame.id, result: result)
        }
        if !accepted {
            outboundInFlight = nil
            process(machine.completeOutbound(frame, accepted: false))
        }
    }
}

private extension Result {
    var isSuccess: Bool {
        if case .success = self { return true }
        return false
    }
}
