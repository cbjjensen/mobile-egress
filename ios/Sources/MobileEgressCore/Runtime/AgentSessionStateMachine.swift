import Foundation

struct AgentSessionCapacitySnapshot: Equatable, Sendable {
    let tombstones: Int
    let targetOutstandingFrames: [String: Int]
}

struct AgentSessionStateMachine {
    private enum StreamPhase {
        case creating
        case connecting
        case open
        case gracefulPending
    }

    private struct TargetWrite {
        let id: UInt64
        let data: Data
    }

    private struct Stream {
        let token: UInt64
        var phase: StreamPhase
        var hasTarget: Bool
        let inboundQueue: BoundedDeque<Data>
        var writeInFlight: TargetWrite?
        var writeFailed = false

        init(token: UInt64, phase: StreamPhase, hasTarget: Bool, inboundCapacity: Int) {
            self.token = token
            self.phase = phase
            self.hasTarget = hasTarget
            inboundQueue = BoundedDeque(capacity: inboundCapacity)
        }

        var canWrite: Bool {
            !writeFailed && (phase == .open || phase == .gracefulPending)
        }

        var targetOutstandingFrameCount: Int {
            inboundQueue.count + (writeInFlight == nil ? 0 : 1)
        }
    }

    private enum RelayTermination {
        case close(code: UInt16, reason: String)
        case cancel
        case none
    }

    private let limits: AgentRuntimeLimits
    private let admission: StreamAdmission
    private let outbound: OutboundMailbox
    private var tombstones: TombstoneWindow
    private var streams: [String: Stream] = [:]
    private var connectionState: AgentRuntimeConnectionState = .stopped
    private var errorClass: AgentRuntimeErrorClass = .none
    private var bytesUploaded: UInt64 = 0
    private var bytesDownloaded: UInt64 = 0
    private var nextStreamToken: UInt64 = 1
    private var nextWriteID: UInt64 = 1
    private var terminal = false
    private(set) var terminalFailure: AgentRuntimeErrorClass?

    init(limits: AgentRuntimeLimits = .production) {
        self.limits = limits
        admission = StreamAdmission(limit: limits.maximumStreams)
        outbound = OutboundMailbox(
            controlCapacity: limits.outboundControls,
            dataCapacity: limits.outboundData,
            perStreamDataCapacity: limits.outboundDataPerStream,
            cancellationHistoryCapacity: limits.tombstones
        )
        tombstones = TombstoneWindow(limit: limits.tombstones)
    }

    var snapshot: AgentRuntimeSnapshot {
        AgentRuntimeSnapshot(
            connectionState: connectionState,
            activeStreamCount: admission.count,
            bytesUploaded: bytesUploaded,
            bytesDownloaded: bytesDownloaded,
            errorClass: errorClass
        )
    }

    var capacitySnapshot: AgentSessionCapacitySnapshot {
        AgentSessionCapacitySnapshot(
            tombstones: tombstones.count,
            targetOutstandingFrames: streams.mapValues(\.targetOutstandingFrameCount)
        )
    }

    mutating func start() -> [AgentRuntimeEffect] {
        guard !terminal, connectionState == .stopped else { return [] }
        connectionState = .connecting
        return [.startRelay]
    }

    mutating func relayConnected() {
        guard !terminal, connectionState == .connecting else { return }
        connectionState = .connected
    }

    mutating func receiveRelay(_ message: RelayWebSocketMessage) -> [AgentRuntimeEffect] {
        guard !terminal, connectionState == .connected else { return [] }
        guard message.isComplete else { return protocolFailure() }
        switch message.opcode {
        case .binary:
            guard message.payload.count <= WireProtocol.maximumWebSocketMessageBytes,
                  let envelope = try? WireProtocol.parseAgentInbound(message.payload)
            else {
                return protocolFailure()
            }
            return handle(envelope)
        case .ping, .pong:
            return message.payload.count <= 125 ? [] : protocolFailure()
        case .close:
            guard message.payload.count <= 125 else { return protocolFailure() }
            return terminate(error: .relayUnavailable, relay: .cancel)
        case .text, .continuation, .unknown:
            return protocolFailure()
        }
    }

    mutating func targetWasCreated(streamID: String, token: UInt64) {
        guard var stream = streams[streamID], stream.token == token, stream.phase == .creating else { return }
        stream.hasTarget = true
        stream.phase = .connecting
        streams[streamID] = stream
    }

    mutating func targetCreationFailed(streamID: String, token: UInt64) -> [AgentRuntimeEffect] {
        guard let stream = streams[streamID], stream.token == token, stream.phase == .creating else { return [] }
        streams.removeValue(forKey: streamID)
        admission.release(streamID)
        tombstones.insert(streamID)
        errorClass = .targetConnect
        _ = outbound.cancelStream(streamID)
        outbound.allowData(streamID)
        return reject(streamID: streamID, code: "target_failure")
    }

    mutating func targetConnected(streamID: String, token: UInt64) -> [AgentRuntimeEffect] {
        guard var stream = streams[streamID], stream.token == token, stream.phase == .connecting else { return [] }
        stream.phase = .open
        streams[streamID] = stream
        let controlEffects = enqueueRequiredControl(type: .opened, streamID: streamID)
        guard controlEffects.isEmpty, !terminal else { return controlEffects }
        return startNextTargetWrite(streamID: streamID, token: token)
    }

    mutating func targetReceived(streamID: String, token: UInt64, data: Data) -> [AgentRuntimeEffect] {
        guard let stream = streams[streamID], stream.token == token, stream.phase == .open else { return [] }
        guard !data.isEmpty else { return [] }
        guard data.count <= limits.targetReadChunkBytes,
              let frame = try? WireProtocol.encode(type: .data, streamID: streamID, payload: data)
        else {
            return failStream(streamID: streamID, token: token, code: "target_failure", error: .targetConnect)
        }
        guard outbound.offerData(frame, streamID: streamID) else {
            return failStream(streamID: streamID, token: token, code: "agent_unavailable", error: .backpressure)
        }
        bytesDownloaded = adding(bytesDownloaded, data.count)
        return []
    }

    mutating func targetEnded(streamID: String, token: UInt64) -> [AgentRuntimeEffect] {
        guard var stream = streams[streamID], stream.token == token, stream.phase == .open else { return [] }
        guard let payload = try? WireProtocol.finiteErrorCode("target_closed"),
              let frame = try? WireProtocol.encode(type: .close, streamID: streamID, payload: payload)
        else {
            return terminate(error: .internal, relay: .cancel)
        }
        guard outbound.offerRequiredControlAfterData(frame, streamID: streamID, onSaturated: {}) else {
            return terminate(error: .backpressure, relay: .cancel)
        }
        stream.phase = .gracefulPending
        streams[streamID] = stream
        return []
    }

    mutating func targetFailed(streamID: String, token: UInt64) -> [AgentRuntimeEffect] {
        guard let stream = streams[streamID], stream.token == token,
              stream.phase == .connecting || stream.phase == .open
        else { return [] }
        return failStream(streamID: streamID, token: token, code: "target_failure", error: .targetConnect)
    }

    mutating func targetWriteCompleted(
        streamID: String,
        token: UInt64,
        writeID: UInt64,
        succeeded: Bool
    ) -> [AgentRuntimeEffect] {
        guard var stream = streams[streamID], stream.token == token,
              let write = stream.writeInFlight, write.id == writeID
        else { return [] }
        if !succeeded {
            stream.writeInFlight = nil
            if stream.phase == .gracefulPending {
                stream.inboundQueue.removeAll()
                stream.writeFailed = true
                let shouldCancelTarget = stream.hasTarget
                stream.hasTarget = false
                streams[streamID] = stream
                return shouldCancelTarget ? [.cancelTarget(streamID: streamID, token: token)] : []
            }
            streams[streamID] = stream
            return failStream(streamID: streamID, token: token, code: "target_failure", error: .targetConnect)
        }
        bytesUploaded = adding(bytesUploaded, write.data.count)
        stream.writeInFlight = nil
        streams[streamID] = stream
        return startNextTargetWrite(streamID: streamID, token: token)
    }

    mutating func relayClosed() -> [AgentRuntimeEffect] {
        terminate(error: .relayUnavailable, relay: .cancel)
    }

    mutating func relayFailed(_ failure: RelayConnectionFailure) -> [AgentRuntimeEffect] {
        let error: AgentRuntimeErrorClass
        switch failure {
        case .authentication: error = .relayAuth
        case .tls: error = .relayTLS
        case .unavailable: error = .relayUnavailable
        }
        return terminate(error: error, relay: .cancel)
    }

    mutating func nextOutbound() -> OutboundFrame? {
        guard !terminal else { return nil }
        return outbound.poll()
    }

    mutating func completeOutbound(_ frame: OutboundFrame, accepted: Bool) -> [AgentRuntimeEffect] {
        guard !terminal else { return [] }
        let emission = outbound.emit(frame, sender: { _ in accepted })
        guard accepted else {
            return terminate(error: .relayUnavailable, relay: .cancel)
        }
        switch emission {
        case .canceled:
            return []
        case .failed:
            return terminate(error: .relayUnavailable, relay: .cancel)
        case .emitted:
            guard let streamID = frame.streamID,
                  let stream = streams[streamID], stream.phase == .gracefulPending,
                  let envelope = try? WireProtocol.parseAgentOutbound(frame.bytes), envelope.type == .close
            else { return [] }
            return releaseStream(streamID: streamID, token: stream.token, remember: true)
        }
    }

    mutating func stop() -> [AgentRuntimeEffect] {
        guard !terminal else { return [] }
        guard connectionState != .stopped else {
            terminal = true
            return []
        }
        return terminate(error: .none, relay: .close(code: 1000, reason: "session_closed"))
    }

    mutating func finishStopping() {
        guard terminal, connectionState == .stopping else { return }
        connectionState = .stopped
    }

    private mutating func handle(_ envelope: WireEnvelope) -> [AgentRuntimeEffect] {
        switch envelope.type {
        case .open:
            return openStream(envelope)
        case .data:
            return routeData(envelope)
        case .close:
            return closeFromRelay(envelope)
        case .ping:
            return enqueueRequiredControl(type: .pong, streamID: "")
        case .pong:
            return []
        case .opened, .rejected:
            return protocolFailure()
        }
    }

    private mutating func openStream(_ envelope: WireEnvelope) -> [AgentRuntimeEffect] {
        guard admission.tryReserve(envelope.streamID) else {
            return reject(streamID: envelope.streamID, code: "agent_stream_limit")
        }
        let target: AgentOpenTarget
        do {
            target = try AgentOpenTarget.parse(envelope)
        } catch {
            admission.release(envelope.streamID)
            return reject(streamID: envelope.streamID, code: "invalid_target")
        }
        let configuration: TargetConnectionConfiguration
        do {
            configuration = try TargetConnectionConfiguration(
                ipLiteral: target.ip,
                port: target.port,
                readChunkBytes: limits.targetReadChunkBytes,
                inboundQueueCapacity: limits.targetInbound
            )
        } catch {
            admission.release(envelope.streamID)
            errorClass = .targetPolicy
            return reject(streamID: envelope.streamID, code: "policy_denied")
        }
        let token = nextStreamToken
        nextStreamToken &+= 1
        outbound.allowData(envelope.streamID)
        streams[envelope.streamID] = Stream(
            token: token,
            phase: .creating,
            hasTarget: false,
            inboundCapacity: limits.targetInbound
        )
        return [.createTarget(streamID: envelope.streamID, token: token, configuration: configuration)]
    }

    private mutating func routeData(_ envelope: WireEnvelope) -> [AgentRuntimeEffect] {
        guard let stream = streams[envelope.streamID] else { return protocolFailure() }
        guard let payload = try? envelope.decodedPayload() else { return protocolFailure() }
        guard payload.count <= limits.maximumInboundDataBytes else { return protocolFailure() }
        if stream.phase == .gracefulPending, stream.writeFailed {
            return []
        }
        if stream.canWrite, stream.writeInFlight == nil {
            return beginTargetWrite(streamID: envelope.streamID, token: stream.token, data: payload)
        }
        guard stream.targetOutstandingFrameCount < limits.targetInbound,
              stream.inboundQueue.append(payload)
        else {
            return failStream(
                streamID: envelope.streamID,
                token: stream.token,
                code: "agent_unavailable",
                error: .backpressure
            )
        }
        return []
    }

    private mutating func closeFromRelay(_ envelope: WireEnvelope) -> [AgentRuntimeEffect] {
        guard let payload = try? envelope.decodedPayload(),
              let code = String(data: payload, encoding: .utf8),
              (try? WireProtocol.finiteErrorCode(code)) != nil
        else {
            return protocolFailure()
        }
        guard let stream = streams[envelope.streamID] else {
            let canceledPendingFrame = outbound.cancelStream(envelope.streamID)
            return canceledPendingFrame || tombstones.contains(envelope.streamID) ? [] : protocolFailure()
        }
        _ = outbound.cancelStream(envelope.streamID)
        return releaseStream(streamID: envelope.streamID, token: stream.token, remember: true)
    }

    private mutating func beginTargetWrite(
        streamID: String,
        token: UInt64,
        data: Data
    ) -> [AgentRuntimeEffect] {
        guard var stream = streams[streamID], stream.token == token,
              stream.canWrite, stream.writeInFlight == nil
        else { return [] }
        let writeID = nextWriteID
        nextWriteID &+= 1
        stream.writeInFlight = TargetWrite(id: writeID, data: data)
        streams[streamID] = stream
        return [.writeTarget(streamID: streamID, token: token, writeID: writeID, data: data)]
    }

    private mutating func startNextTargetWrite(streamID: String, token: UInt64) -> [AgentRuntimeEffect] {
        guard let stream = streams[streamID], stream.token == token,
              stream.canWrite, stream.writeInFlight == nil,
              let data = stream.inboundQueue.popFirst()
        else { return [] }
        return beginTargetWrite(streamID: streamID, token: token, data: data)
    }

    private mutating func failStream(
        streamID: String,
        token: UInt64,
        code: String,
        error: AgentRuntimeErrorClass
    ) -> [AgentRuntimeEffect] {
        guard let stream = streams[streamID], stream.token == token,
              stream.phase == .creating || stream.phase == .connecting || stream.phase == .open
        else { return [] }
        guard let payload = try? WireProtocol.finiteErrorCode(code),
              let frame = try? WireProtocol.encode(type: .close, streamID: streamID, payload: payload)
        else {
            return terminate(error: .internal, relay: .cancel)
        }
        outbound.blockAndDiscardData(streamID: streamID)
        guard outbound.offerRequiredControl(frame, streamID: streamID, onSaturated: {}) else {
            return terminate(error: .backpressure, relay: .cancel)
        }
        errorClass = error
        return releaseStream(streamID: streamID, token: token, remember: true)
    }

    private mutating func releaseStream(
        streamID: String,
        token: UInt64,
        remember: Bool
    ) -> [AgentRuntimeEffect] {
        guard let stream = streams[streamID], stream.token == token else { return [] }
        streams.removeValue(forKey: streamID)
        admission.release(streamID)
        if remember { tombstones.insert(streamID) }
        return stream.hasTarget ? [.cancelTarget(streamID: streamID, token: token)] : []
    }

    private mutating func reject(streamID: String, code: String) -> [AgentRuntimeEffect] {
        tombstones.insert(streamID)
        guard let payload = try? WireProtocol.finiteErrorCode(code) else {
            return terminate(error: .internal, relay: .cancel)
        }
        return enqueueRequiredControl(type: .rejected, streamID: streamID, payload: payload)
    }

    private mutating func enqueueRequiredControl(
        type: WireMessageType,
        streamID: String,
        payload: Data = Data()
    ) -> [AgentRuntimeEffect] {
        guard let frame = try? WireProtocol.encode(type: type, streamID: streamID, payload: payload) else {
            return terminate(error: .internal, relay: .cancel)
        }
        guard outbound.offerRequiredControl(
            frame,
            streamID: streamID.isEmpty ? nil : streamID,
            onSaturated: {}
        ) else {
            return terminate(error: .backpressure, relay: .cancel)
        }
        return []
    }

    private mutating func protocolFailure() -> [AgentRuntimeEffect] {
        terminate(error: .protocol, relay: .close(code: 1008, reason: "protocol_error"))
    }

    private mutating func terminate(
        error: AgentRuntimeErrorClass,
        relay: RelayTermination
    ) -> [AgentRuntimeEffect] {
        guard !terminal else { return [] }
        terminal = true
        connectionState = .stopping
        if error != .none {
            errorClass = error
            terminalFailure = error
        }
        var effects: [AgentRuntimeEffect] = []
        switch relay {
        case let .close(code, reason): effects.append(.closeRelay(code: code, reason: reason))
        case .cancel: effects.append(.cancelRelay)
        case .none: break
        }
        for streamID in streams.keys.sorted() {
            guard let stream = streams[streamID], stream.hasTarget else { continue }
            effects.append(.cancelTarget(streamID: streamID, token: stream.token))
        }
        streams.removeAll(keepingCapacity: false)
        _ = admission.clear()
        tombstones.removeAll()
        outbound.close()
        return effects
    }

    private func adding(_ value: UInt64, _ increment: Int) -> UInt64 {
        let (sum, overflow) = value.addingReportingOverflow(UInt64(increment))
        return overflow ? UInt64.max : sum
    }
}

private struct AgentOpenTarget: Decodable {
    let ip: String
    let port: Int

    static func parse(_ envelope: WireEnvelope) throws -> AgentOpenTarget {
        guard envelope.type == .open else { throw CoreValidationError.invalidJSON }
        let payload = try envelope.decodedPayload()
        try StrictJSONObject.exactKeys(in: payload, expected: ["ip", "port"])
        guard let integerPort = StrictJSONObject.integerLiteral(forKey: "port", in: payload) else {
            throw CoreValidationError.invalidJSON
        }
        let target = try JSONDecoder().decode(AgentOpenTarget.self, from: payload)
        guard target.port == integerPort else { throw CoreValidationError.invalidJSON }
        return target
    }
}
