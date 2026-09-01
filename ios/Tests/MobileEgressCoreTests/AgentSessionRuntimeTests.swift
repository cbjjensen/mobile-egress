import Foundation
import XCTest
@testable import MobileEgressCore

final class AgentSessionRuntimeTests: XCTestCase {
    func testRuntimeNotifiesTerminalFailureExactlyOnce() async {
        let relay = RecordingRelayWebSocket()
        let failures = RecordingTerminalFailures()
        let runtime = AgentSessionRuntime(
            relay: relay,
            targetFactory: RecordingTargetConnectionFactory(),
            terminalFailureHandler: { failures.record($0) }
        )
        await runtime.start()

        await relay.emit(.failed(.tls))
        await relay.emit(.failed(.authentication))

        XCTAssertEqual(failures.values, [.relayTLS])
    }

    func testRuntimeExplicitStopDoesNotNotifyTerminalFailure() async {
        let relay = RecordingRelayWebSocket()
        let failures = RecordingTerminalFailures()
        let runtime = AgentSessionRuntime(
            relay: relay,
            targetFactory: RecordingTargetConnectionFactory(),
            terminalFailureHandler: { failures.record($0) }
        )
        await runtime.start()
        await relay.emit(.connected)

        await runtime.stop()
        await runtime.stop()
        await relay.emit(.failed(.unavailable))

        XCTAssertEqual(failures.values, [])
    }

    func testRuntimeRejectsPolicyBeforeFactoryCanCreateTarget() async throws {
        let relay = RecordingRelayWebSocket()
        let factory = RecordingTargetConnectionFactory()
        let runtime = AgentSessionRuntime(relay: relay, targetFactory: factory)
        await runtime.start()
        await relay.emit(.connected)

        let payload = Data(#"{"ip":"10.0.0.1","port":443}"#.utf8)
        let wire = try WireProtocol.encode(type: .open, streamID: "blocked", payload: payload)
        await relay.emit(.message(.init(opcode: .binary, payload: wire, isComplete: true)))

        XCTAssertEqual(factory.makeCount, 0)
        let rejection = try XCTUnwrap(relay.sentBinary.first)
        let envelope = try WireProtocol.parseAgentOutbound(rejection)
        XCTAssertEqual(envelope.type, .rejected)
        XCTAssertEqual(try envelope.decodedPayload(), Data("policy_denied".utf8))
        let snapshot = await runtime.snapshot()
        XCTAssertEqual(snapshot.errorClass, .targetPolicy)
    }

    func testRuntimeTerminalStopClosesRelayAndCancelsTargetExactlyOnce() async throws {
        let relay = RecordingRelayWebSocket()
        let target = RecordingTargetConnection()
        let factory = RecordingTargetConnectionFactory(target: target)
        let runtime = AgentSessionRuntime(relay: relay, targetFactory: factory)
        await runtime.start()
        await relay.emit(.connected)

        let payload = Data(#"{"ip":"8.8.8.8","port":443}"#.utf8)
        let wire = try WireProtocol.encode(type: .open, streamID: "stream", payload: payload)
        await relay.emit(.message(.init(opcode: .binary, payload: wire, isComplete: true)))
        await target.emit(.ready)

        await runtime.stop()
        await runtime.stop()
        await target.emit(.failed)
        await relay.emit(.failed(.unavailable))

        XCTAssertEqual(factory.makeCount, 1)
        XCTAssertEqual(target.cancelCount, 1)
        XCTAssertEqual(relay.closeCalls, [.init(code: 1000, reason: "session_closed")])
        XCTAssertEqual(relay.cancelCount, 0)
        let snapshot = await runtime.snapshot()
        XCTAssertEqual(snapshot.connectionState, .stopped)
        XCTAssertEqual(snapshot.activeStreamCount, 0)
    }

    func testRuntimeMapsFiniteRelayFailureAndCancelsOnce() async {
        let relay = RecordingRelayWebSocket()
        let runtime = AgentSessionRuntime(relay: relay, targetFactory: RecordingTargetConnectionFactory())
        await runtime.start()

        await relay.emit(.failed(.tls))
        await relay.emit(.failed(.authentication))

        XCTAssertEqual(relay.cancelCount, 1)
        let snapshot = await runtime.snapshot()
        XCTAssertEqual(snapshot.connectionState, .stopped)
        XCTAssertEqual(snapshot.errorClass, .relayTLS)
    }

    func testRuntimeFailedSendAfterInFlightStreamCloseCancelsRelayAndTargetsOnce() async throws {
        let relay = RecordingRelayWebSocket(automaticallyCompletesSends: false)
        let firstTarget = RecordingTargetConnection()
        let secondTarget = RecordingTargetConnection()
        let factory = SequencedTargetConnectionFactory(targets: [firstTarget, secondTarget])
        let runtime = AgentSessionRuntime(relay: relay, targetFactory: factory)
        await runtime.start()
        await relay.emit(.connected)

        for (streamID, target) in [("first", firstTarget), ("second", secondTarget)] {
            let payload = Data(#"{"ip":"8.8.8.8","port":443}"#.utf8)
            let open = try WireProtocol.encode(type: .open, streamID: streamID, payload: payload)
            await relay.emit(.message(.init(opcode: .binary, payload: open, isComplete: true)))
            await target.emit(.ready)
            await relay.completeNextSend(.success(()))
        }

        await firstTarget.emit(.data(Data([0x41])))
        let close = try WireProtocol.encode(
            type: .close,
            streamID: "first",
            payload: Data("client_closed".utf8)
        )
        await relay.emit(.message(.init(opcode: .binary, payload: close, isComplete: true)))

        XCTAssertEqual(firstTarget.cancelCount, 1)
        XCTAssertEqual(secondTarget.cancelCount, 0)
        await relay.completeNextSend(.failure(.unavailable))

        XCTAssertEqual(relay.cancelCount, 1)
        XCTAssertEqual(firstTarget.cancelCount, 1)
        XCTAssertEqual(secondTarget.cancelCount, 1)
        let snapshot = await runtime.snapshot()
        XCTAssertEqual(snapshot.connectionState, .stopped)
        XCTAssertEqual(snapshot.activeStreamCount, 0)
        XCTAssertEqual(snapshot.errorClass, .relayUnavailable)
    }

    func testRuntimeStopDuringTargetWriteCancelsOnceAndIgnoresLateCompletion() async throws {
        let relay = RecordingRelayWebSocket(automaticallyCompletesSends: false)
        let target = RecordingTargetConnection(automaticallyCompletesSends: false)
        let runtime = AgentSessionRuntime(
            relay: relay,
            targetFactory: RecordingTargetConnectionFactory(target: target)
        )
        await runtime.start()
        await relay.emit(.connected)
        let open = try WireProtocol.encode(
            type: .open,
            streamID: "stream",
            payload: Data(#"{"ip":"8.8.8.8","port":443}"#.utf8)
        )
        await relay.emit(.message(.init(opcode: .binary, payload: open, isComplete: true)))
        await target.emit(.ready)
        let data = try WireProtocol.encode(
            type: .data,
            streamID: "stream",
            payload: Data([0x41])
        )
        await relay.emit(.message(.init(opcode: .binary, payload: data, isComplete: true)))
        XCTAssertEqual(target.sentData, [Data([0x41])])

        await runtime.stop()
        await target.completeNextSend(.success(()))
        await relay.completeNextSend(.success(()))
        await target.emit(.failed)

        XCTAssertEqual(target.cancelCount, 1)
        XCTAssertEqual(relay.closeCalls, [.init(code: 1000, reason: "session_closed")])
        let snapshot = await runtime.snapshot()
        XCTAssertEqual(snapshot.connectionState, .stopped)
        XCTAssertEqual(snapshot.activeStreamCount, 0)
        XCTAssertEqual(snapshot.bytesUploaded, 0)
    }

    func testRuntimeCloseReopenAndLateOldCallbacksCannotAffectReplacementStream() async throws {
        let relay = RecordingRelayWebSocket()
        let oldTarget = RecordingTargetConnection(automaticallyCompletesSends: false)
        let replacementTarget = RecordingTargetConnection(automaticallyCompletesSends: false)
        let runtime = AgentSessionRuntime(
            relay: relay,
            targetFactory: SequencedTargetConnectionFactory(targets: [oldTarget, replacementTarget])
        )
        await runtime.start()
        await relay.emit(.connected)
        let open = try WireProtocol.encode(
            type: .open,
            streamID: "stream",
            payload: Data(#"{"ip":"8.8.8.8","port":443}"#.utf8)
        )
        await relay.emit(.message(.init(opcode: .binary, payload: open, isComplete: true)))
        await oldTarget.emit(.ready)
        let oldData = try WireProtocol.encode(
            type: .data,
            streamID: "stream",
            payload: Data([0x41])
        )
        await relay.emit(.message(.init(opcode: .binary, payload: oldData, isComplete: true)))
        let close = try WireProtocol.encode(
            type: .close,
            streamID: "stream",
            payload: Data("client_closed".utf8)
        )
        await relay.emit(.message(.init(opcode: .binary, payload: close, isComplete: true)))
        await relay.emit(.message(.init(opcode: .binary, payload: open, isComplete: true)))
        await replacementTarget.emit(.ready)

        await oldTarget.completeNextSend(.success(()))
        await oldTarget.emit(.data(Data([0x55])))
        await oldTarget.emit(.failed)

        let replacementData = try WireProtocol.encode(
            type: .data,
            streamID: "stream",
            payload: Data([0x42])
        )
        await relay.emit(.message(.init(opcode: .binary, payload: replacementData, isComplete: true)))
        await replacementTarget.completeNextSend(.success(()))

        XCTAssertEqual(oldTarget.cancelCount, 1)
        XCTAssertEqual(replacementTarget.cancelCount, 0)
        XCTAssertEqual(replacementTarget.sentData, [Data([0x42])])
        let beforeStop = await runtime.snapshot()
        XCTAssertEqual(beforeStop.activeStreamCount, 1)
        XCTAssertEqual(beforeStop.bytesUploaded, 1)

        await runtime.stop()
        XCTAssertEqual(oldTarget.cancelCount, 1)
        XCTAssertEqual(replacementTarget.cancelCount, 1)
    }

    func testRuntimeEOFFollowedByStopAndLateRelayCompletionCancelsTargetExactlyOnce() async throws {
        let relay = RecordingRelayWebSocket(automaticallyCompletesSends: false)
        let target = RecordingTargetConnection()
        let runtime = AgentSessionRuntime(
            relay: relay,
            targetFactory: RecordingTargetConnectionFactory(target: target)
        )
        await runtime.start()
        await relay.emit(.connected)
        let open = try WireProtocol.encode(
            type: .open,
            streamID: "stream",
            payload: Data(#"{"ip":"8.8.8.8","port":443}"#.utf8)
        )
        await relay.emit(.message(.init(opcode: .binary, payload: open, isComplete: true)))
        await target.emit(.ready)
        await relay.completeNextSend(.success(()))
        await target.emit(.data(Data([0x41])))
        await target.emit(.ended)

        await runtime.stop()
        await relay.completeNextSend(.success(()))
        await target.emit(.failed)

        XCTAssertEqual(target.cancelCount, 1)
        XCTAssertEqual(relay.closeCalls, [.init(code: 1000, reason: "session_closed")])
        let snapshot = await runtime.snapshot()
        XCTAssertEqual(snapshot.connectionState, .stopped)
        XCTAssertEqual(snapshot.activeStreamCount, 0)
    }

    func testRuntimeTargetEOFWaitsForAcceptedTargetWritesBeforeClosingStream() async throws {
        let relay = RecordingRelayWebSocket(automaticallyCompletesSends: false)
        let target = RecordingTargetConnection(automaticallyCompletesSends: false)
        let runtime = AgentSessionRuntime(
            relay: relay,
            targetFactory: RecordingTargetConnectionFactory(target: target)
        )
        await runtime.start()
        await relay.emit(.connected)

        let open = try WireProtocol.encode(
            type: .open,
            streamID: "stream",
            payload: Data(#"{"ip":"8.8.8.8","port":443}"#.utf8)
        )
        await relay.emit(.message(.init(opcode: .binary, payload: open, isComplete: true)))
        await target.emit(.ready)
        await relay.completeNextSend(.success(()))

        for byte in [UInt8(0x41), UInt8(0x42)] {
            let data = try WireProtocol.encode(
                type: .data,
                streamID: "stream",
                payload: Data([byte])
            )
            await relay.emit(.message(.init(opcode: .binary, payload: data, isComplete: true)))
        }
        await target.emit(.ended)

        XCTAssertEqual(relay.sentBinary.count, 1, "target_closed must wait for accepted target writes")
        XCTAssertEqual(target.sentData, [Data([0x41])])
        XCTAssertEqual(target.cancelCount, 0)

        await target.completeNextSend(.success(()))
        XCTAssertEqual(target.sentData, [Data([0x41]), Data([0x42])])
        XCTAssertEqual(relay.sentBinary.count, 1)

        await target.completeNextSend(.success(()))
        XCTAssertEqual(relay.sentBinary.count, 2)
        let terminal = try WireProtocol.parseAgentOutbound(try XCTUnwrap(relay.sentBinary.last))
        XCTAssertEqual(terminal.type, .close)
        XCTAssertEqual(terminal.streamID, "stream")
        XCTAssertEqual(try terminal.decodedPayload(), Data("target_closed".utf8))
        XCTAssertEqual(target.cancelCount, 0)

        await relay.completeNextSend(.success(()))
        XCTAssertEqual(target.cancelCount, 1)
        let snapshot = await runtime.snapshot()
        XCTAssertEqual(snapshot.activeStreamCount, 0)
        XCTAssertEqual(snapshot.bytesUploaded, 2)
    }

    func testRuntimeTargetAndRelayFailureRaceNotifiesAndCancelsExactlyOnce() async throws {
        let relay = RecordingRelayWebSocket()
        let target = RecordingTargetConnection()
        let failures = RecordingTerminalFailures()
        let runtime = AgentSessionRuntime(
            relay: relay,
            targetFactory: RecordingTargetConnectionFactory(target: target),
            terminalFailureHandler: { failures.record($0) }
        )
        await runtime.start()
        await relay.emit(.connected)
        let open = try WireProtocol.encode(
            type: .open,
            streamID: "stream",
            payload: Data(#"{"ip":"8.8.8.8","port":443}"#.utf8)
        )
        await relay.emit(.message(.init(opcode: .binary, payload: open, isComplete: true)))
        await target.emit(.ready)

        await target.emit(.failed)
        await relay.emit(.failed(.tls))
        await relay.emit(.failed(.authentication))
        await target.emit(.ended)

        XCTAssertEqual(target.cancelCount, 1)
        XCTAssertEqual(relay.cancelCount, 1)
        XCTAssertEqual(failures.values, [.relayTLS])
        let snapshot = await runtime.snapshot()
        XCTAssertEqual(snapshot.connectionState, .stopped)
        XCTAssertEqual(snapshot.activeStreamCount, 0)
    }

    func testRuntimeRelayCloseDuringTargetWriteCancelsOnceAndIgnoresLateTargetCallbacks() async throws {
        let relay = RecordingRelayWebSocket()
        let target = RecordingTargetConnection(automaticallyCompletesSends: false)
        let failures = RecordingTerminalFailures()
        let runtime = AgentSessionRuntime(
            relay: relay,
            targetFactory: RecordingTargetConnectionFactory(target: target),
            terminalFailureHandler: { failures.record($0) }
        )
        await runtime.start()
        await relay.emit(.connected)
        let open = try WireProtocol.encode(
            type: .open,
            streamID: "stream",
            payload: Data(#"{"ip":"8.8.8.8","port":443}"#.utf8)
        )
        await relay.emit(.message(.init(opcode: .binary, payload: open, isComplete: true)))
        await target.emit(.ready)
        let data = try WireProtocol.encode(
            type: .data,
            streamID: "stream",
            payload: Data([0x41])
        )
        await relay.emit(.message(.init(opcode: .binary, payload: data, isComplete: true)))

        await relay.emit(.closed)
        await target.completeNextSend(.success(()))
        await target.emit(.ended)
        await target.emit(.failed)

        XCTAssertEqual(target.cancelCount, 1)
        XCTAssertEqual(relay.cancelCount, 1)
        XCTAssertEqual(failures.values, [.relayUnavailable])
        let snapshot = await runtime.snapshot()
        XCTAssertEqual(snapshot.connectionState, .stopped)
        XCTAssertEqual(snapshot.activeStreamCount, 0)
        XCTAssertEqual(snapshot.bytesUploaded, 0)
    }

    func testRuntimeStopProcessesTwoHundredFiftySixTargetCancellationsExactlyOnce() async throws {
        let relay = RecordingRelayWebSocket()
        let targets = (0 ..< 256).map { _ in RecordingTargetConnection() }
        let runtime = AgentSessionRuntime(
            relay: relay,
            targetFactory: SequencedTargetConnectionFactory(targets: targets)
        )
        await runtime.start()
        await relay.emit(.connected)

        for (index, target) in targets.enumerated() {
            let open = try WireProtocol.encode(
                type: .open,
                streamID: "stream-\(index)",
                payload: Data(#"{"ip":"8.8.8.8","port":443}"#.utf8)
            )
            await relay.emit(.message(.init(opcode: .binary, payload: open, isComplete: true)))
            await target.emit(.ready)
        }

        await runtime.stop()
        await runtime.stop()

        XCTAssertTrue(targets.allSatisfy { $0.cancelCount == 1 })
        XCTAssertEqual(relay.closeCalls, [.init(code: 1000, reason: "session_closed")])
        let snapshot = await runtime.snapshot()
        XCTAssertEqual(snapshot.connectionState, .stopped)
        XCTAssertEqual(snapshot.activeStreamCount, 0)
    }
}

private final class RecordingTerminalFailures: @unchecked Sendable {
    private let lock = NSLock()
    private var recorded: [AgentRuntimeErrorClass] = []

    var values: [AgentRuntimeErrorClass] { lock.withLock { recorded } }

    func record(_ failure: AgentRuntimeErrorClass) {
        lock.withLock { recorded.append(failure) }
    }
}

private final class RecordingRelayWebSocket: RelayWebSocketIO, @unchecked Sendable {
    struct CloseCall: Equatable {
        let code: UInt16
        let reason: String
    }

    private let lock = NSLock()
    private var eventHandler: RelayWebSocketEventHandler?
    private var binary: [Data] = []
    private var closes: [CloseCall] = []
    private var cancellations = 0
    private var sendCompletions: [RelayWebSocketSendCompletion] = []
    private let automaticallyCompletesSends: Bool

    init(automaticallyCompletesSends: Bool = true) {
        self.automaticallyCompletesSends = automaticallyCompletesSends
    }

    var sentBinary: [Data] { lock.withLock { binary } }
    var closeCalls: [CloseCall] { lock.withLock { closes } }
    var cancelCount: Int { lock.withLock { cancellations } }

    func start(eventHandler: @escaping RelayWebSocketEventHandler) {
        lock.withLock { self.eventHandler = eventHandler }
    }

    func sendBinary(
        _ data: Data,
        completion: @escaping RelayWebSocketSendCompletion
    ) -> Bool {
        lock.withLock {
            binary.append(data)
            if !automaticallyCompletesSends {
                sendCompletions.append(completion)
            }
        }
        if automaticallyCompletesSends {
            Task { await completion(.success(())) }
        }
        return true
    }

    func close(code: UInt16, reason: String) {
        lock.withLock { closes.append(.init(code: code, reason: reason)) }
    }

    func cancel() {
        lock.withLock { cancellations += 1 }
    }

    func emit(_ event: RelayWebSocketEvent) async {
        guard let handler = lock.withLock({ eventHandler }) else {
            XCTFail("Relay was not started")
            return
        }
        await handler(event)
    }

    func completeNextSend(_ result: Result<Void, RelayConnectionFailure>) async {
        let completion = lock.withLock {
            sendCompletions.isEmpty ? nil : sendCompletions.removeFirst()
        }
        guard let completion else {
            XCTFail("No relay send completion is pending")
            return
        }
        await completion(result)
    }
}

private enum RuntimeTestError: Error {
    case noTargetAvailable
}

private final class SequencedTargetConnectionFactory: TargetConnectionFactory, @unchecked Sendable {
    private let lock = NSLock()
    private let targets: [RecordingTargetConnection]
    private var nextTargetIndex = 0

    init(targets: [RecordingTargetConnection]) {
        self.targets = targets
    }

    func makeConnection(configuration: TargetConnectionConfiguration) throws -> any TargetConnectionIO {
        let target = lock.withLock { () -> RecordingTargetConnection? in
            guard nextTargetIndex < targets.count else { return nil }
            defer { nextTargetIndex += 1 }
            return targets[nextTargetIndex]
        }
        guard let target else { throw RuntimeTestError.noTargetAvailable }
        return target
    }
}

private final class RecordingTargetConnectionFactory: TargetConnectionFactory, @unchecked Sendable {
    private let lock = NSLock()
    private let target: RecordingTargetConnection
    private var configurations: [TargetConnectionConfiguration] = []

    init(target: RecordingTargetConnection = RecordingTargetConnection()) {
        self.target = target
    }

    var makeCount: Int { lock.withLock { configurations.count } }

    func makeConnection(configuration: TargetConnectionConfiguration) throws -> any TargetConnectionIO {
        lock.withLock { configurations.append(configuration) }
        return target
    }
}

private final class RecordingTargetConnection: TargetConnectionIO, @unchecked Sendable {
    private let lock = NSLock()
    private var eventHandler: TargetConnectionEventHandler?
    private var cancellations = 0
    private var sends: [Data] = []
    private var sendCompletions: [TargetConnectionSendCompletion] = []
    private let automaticallyCompletesSends: Bool

    init(automaticallyCompletesSends: Bool = true) {
        self.automaticallyCompletesSends = automaticallyCompletesSends
    }

    var cancelCount: Int { lock.withLock { cancellations } }
    var sentData: [Data] { lock.withLock { sends } }

    func start(eventHandler: @escaping TargetConnectionEventHandler) {
        lock.withLock { self.eventHandler = eventHandler }
    }

    func send(_ data: Data, completion: @escaping TargetConnectionSendCompletion) -> Bool {
        lock.withLock {
            sends.append(data)
            if !automaticallyCompletesSends {
                sendCompletions.append(completion)
            }
        }
        if automaticallyCompletesSends {
            Task { await completion(.success(())) }
        }
        return true
    }

    func cancel() {
        lock.withLock { cancellations += 1 }
    }

    func emit(_ event: TargetConnectionEvent) async {
        guard let handler = lock.withLock({ eventHandler }) else {
            XCTFail("Target was not started")
            return
        }
        await handler(event)
    }

    func completeNextSend(_ result: Result<Void, TargetConnectionFailure>) async {
        let completion = lock.withLock {
            sendCompletions.isEmpty ? nil : sendCompletions.removeFirst()
        }
        guard let completion else {
            XCTFail("No target send completion is pending")
            return
        }
        await completion(result)
    }
}
