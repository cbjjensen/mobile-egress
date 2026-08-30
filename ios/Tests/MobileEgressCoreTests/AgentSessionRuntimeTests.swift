import Foundation
import XCTest
@testable import MobileEgressCore

final class AgentSessionRuntimeTests: XCTestCase {
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
    private var targets: [RecordingTargetConnection]

    init(targets: [RecordingTargetConnection]) {
        self.targets = targets
    }

    func makeConnection(configuration: TargetConnectionConfiguration) throws -> any TargetConnectionIO {
        let target = lock.withLock { targets.isEmpty ? nil : targets.removeFirst() }
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

    var cancelCount: Int { lock.withLock { cancellations } }

    func start(eventHandler: @escaping TargetConnectionEventHandler) {
        lock.withLock { self.eventHandler = eventHandler }
    }

    func send(_ data: Data, completion: @escaping TargetConnectionSendCompletion) -> Bool {
        Task { await completion(.success(())) }
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
}
