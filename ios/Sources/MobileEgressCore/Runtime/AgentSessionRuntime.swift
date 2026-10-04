import Foundation

public actor AgentSessionRuntime {
    private struct TargetHandle {
        let token: UInt64
        let connection: any TargetConnectionIO
    }

    private let relay: any RelayWebSocketIO
    private let targetFactory: any TargetConnectionFactory
    private let terminalFailureHandler: @Sendable (AgentRuntimeErrorClass) -> Void
    private let endpointUpdateHandler: (@Sendable (String) async throws -> Void)?
    private var machine = AgentSessionStateMachine()
    private var targets: [String: TargetHandle] = [:]
    private var outboundInFlight: OutboundFrame?
    private var terminalFailureNotified = false
    private struct WaitingTargetRead {
        let token: UInt64
        let continuation: CheckedContinuation<Bool, Never>
    }
    private var waitingTargetReads: [String: WaitingTargetRead] = [:]
    private let readTurns: DirectReadTurns?
    private let readPeer: DirectReadTurns.Peer?

    public init(
        relay: any RelayWebSocketIO,
        targetFactory: any TargetConnectionFactory,
        terminalFailureHandler: @escaping @Sendable (AgentRuntimeErrorClass) -> Void = { _ in },
        sharedBudget: DirectPhoneBudget? = nil,
        endpointUpdateHandler: (@Sendable (String) async throws -> Void)? = nil
    ) {
        self.relay = relay
        self.targetFactory = targetFactory
        self.terminalFailureHandler = terminalFailureHandler
        self.endpointUpdateHandler = endpointUpdateHandler
        self.machine = AgentSessionStateMachine(sharedBudget: sharedBudget, endpointUpdates: endpointUpdateHandler != nil)
        self.readTurns = sharedBudget?.readTurns
        self.readPeer = sharedBudget?.readTurns.makePeer()
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
            process(machine.relayConnected())
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
    ) async {
        let effects: [AgentRuntimeEffect]
        switch event {
        case .ready:
            effects = machine.targetConnected(streamID: streamID, token: token)
        case let .data(data):
            let (prepared, rejected) = machine.prepareTargetData(streamID: streamID, token: token, data: data)
            guard let prepared else { process(rejected); return }
            if let readTurns, let readPeer {
                guard let turn = await readTurns.acquire(readPeer, key: "\(streamID)/\(token)") else { return }
                effects = machine.acceptTargetData(streamID: streamID, token: token, prepared: prepared)
                process(effects)
                turn.complete()
                return
            }
            effects = machine.acceptTargetData(streamID: streamID, token: token, prepared: prepared)
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
        if let streamID = frame.streamID { resumeTargetRead(streamID: streamID) }
    }

    private func process(_ initialEffects: [AgentRuntimeEffect]) {
        var effects = initialEffects
        var nextEffectIndex = 0
        while nextEffectIndex < effects.count {
            let effect = effects[nextEffectIndex]
            nextEffectIndex += 1
            switch effect {
            case let .applyEndpointUpdate(bundle):
                // The state machine admits one bounded update per session. Stop
                // after durable import (or rejection); the supervisor owns retry.
                Task { [weak self, endpointUpdateHandler] in
                    try? await endpointUpdateHandler?(bundle)
                    await self?.stop()
                }
            case .startRelay:
                relay.start { [weak self] event in
                    await self?.handleRelay(event)
                }
            case let .createTarget(streamID, token, configuration):
                do {
                    let connection = try targetFactory.makeConnection(configuration: configuration)
                    targets[streamID] = TargetHandle(token: token, connection: connection)
                    machine.targetWasCreated(streamID: streamID, token: token)
                    connection.start(eventHandler: { [weak self] event in
                        await self?.handleTarget(streamID: streamID, token: token, event: event)
                    }, readReadiness: { [weak self] in
                        await self?.waitUntilTargetReadable(streamID: streamID, token: token) ?? false
                    })
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
                let lease = machine.targetWriteLease(streamID: streamID, token: token, writeID: writeID)
                let accepted = target.connection.send(data) { [weak self, lease] result in
                    lease?.complete()
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
                if let readTurns, let readPeer { readTurns.cancelWaiter(readPeer, key: "\(streamID)/\(token)") }
                if waitingTargetReads[streamID]?.token == token {
                    waitingTargetReads.removeValue(forKey: streamID)?.continuation.resume(returning: false)
                }
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
            if let readTurns, let readPeer { readTurns.cancel(readPeer) }
            outboundInFlight = nil
            machine.finishStopping()
            notifyTerminalFailureIfNeeded()
            return
        }
        pumpOutbound()
    }

    private func waitUntilTargetReadable(streamID: String, token: UInt64) async -> Bool {
        guard machine.targetReadIsPossible(streamID: streamID, token: token) else { return false }
        if machine.targetReadCanProceed(streamID: streamID, token: token) { return true }
        // Each native target has one receive/delivery chain. Waiting takes no
        // payload or aggregate reservation, so idle sockets cannot starve it.
        guard waitingTargetReads[streamID] == nil else { return false }
        return await withCheckedContinuation { continuation in
            waitingTargetReads[streamID] = WaitingTargetRead(token: token, continuation: continuation)
        }
    }

    private func resumeTargetRead(streamID: String) {
        guard let waiting = waitingTargetReads[streamID] else { return }
        let possible = machine.targetReadIsPossible(streamID: streamID, token: waiting.token)
        guard !possible || machine.targetReadCanProceed(streamID: streamID, token: waiting.token) else { return }
        waitingTargetReads.removeValue(forKey: streamID)
        waiting.continuation.resume(returning: possible)
    }

    private func notifyTerminalFailureIfNeeded() {
        guard !terminalFailureNotified, let failure = machine.terminalFailure else { return }
        terminalFailureNotified = true
        terminalFailureHandler(failure)
    }

    private func pumpOutbound() {
        guard outboundInFlight == nil, let frame = machine.nextOutbound() else { return }
        outboundInFlight = frame
        let accepted = relay.sendBinary(frame.bytes) { [weak self, lease = frame.budgetLease] result in
            lease?.complete()
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
