#if canImport(Network)
import Foundation
import Network
import XCTest
@testable import MobileEgressCore

final class NetworkTargetBackpressureTests: XCTestCase {
    func testNativeReadGateLeavesIdleSocketUnreservedAndDrainsExactTailAfterResume() async throws {
        let server = try LoopbackTargetServer()
        let port = try await server.start()
        let admission = NativeReadAdmission()
        let recorder = NativeTargetEvents()
        let ready = expectation(description: "target ready")
        let ended = expectation(description: "target EOF")
        let target = NetworkTargetConnection(
            endpoints: [.hostPort(host: "127.0.0.1", port: port)],
            parameters: .tcp, readChunkBytes: 16 * 1_024, connectTimeout: 5
        )
        target.start(eventHandler: { event in
            await recorder.record(event)
            if event == .ready { ready.fulfill() }
            if event == .ended { ended.fulfill() }
        }, readReadiness: { await admission.acquire() })
        await fulfillment(of: [ready], timeout: 5)
        await server.waitForConnection()
        try await Task.sleep(nanoseconds: 20_000_000)
        let idleRequests = await admission.requests
        XCTAssertEqual(idleRequests, 1, "one readiness waiter contains no native payload or reserved lane capacity")
        let expected = Data((0 ..< (2 * 1_024 * 1_024 + 37)).map { UInt8($0 % 251) })
        server.send(expected, end: true)
        await waitUntil { await admission.requests == 1 }
        let stalled = await recorder.bytes
        XCTAssertTrue(stalled.isEmpty, "no native receive is issued while this stream is paused")
        let started = ContinuousClock.now
        await admission.resume()
        await fulfillment(of: [ended], timeout: 10)
        let elapsed = started.duration(to: .now)
        let received = await recorder.bytes
        let largest = await recorder.largestChunk
        let failures = await recorder.failures
        XCTAssertEqual(received, expected)
        XCTAssertLessThanOrEqual(largest, 16 * 1_024)
        XCTAssertEqual(failures, 0)
        print("Native gated loopback: \(received.count) bytes in \(elapsed)")
        target.cancel()
        server.cancel()
    }

    func testNativeCancellationWhileWaitingDoesNotDeliverPayloadOrEOF() async throws {
        let server = try LoopbackTargetServer()
        let port = try await server.start()
        let admission = NativeReadAdmission()
        let recorder = NativeTargetEvents()
        let ready = expectation(description: "target ready")
        let target = NetworkTargetConnection(
            endpoints: [.hostPort(host: "127.0.0.1", port: port)],
            parameters: .tcp, readChunkBytes: 16 * 1_024, connectTimeout: 5
        )
        target.start(eventHandler: { event in
            await recorder.record(event)
            if event == .ready { ready.fulfill() }
        }, readReadiness: { await admission.acquire() })
        await fulfillment(of: [ready], timeout: 5)
        await server.waitForConnection()
        server.send(Data(repeating: 0x41, count: 64 * 1_024), end: true)
        await waitUntil { await admission.requests == 1 }
        target.cancel()
        await admission.cancel()
        try await Task.sleep(nanoseconds: 20_000_000)
        let received = await recorder.bytes
        let ends = await recorder.ends
        XCTAssertTrue(received.isEmpty)
        XCTAssertEqual(ends, 0)
        server.cancel()
    }

    private func waitUntil(_ condition: () async -> Bool) async {
        for _ in 0 ..< 2_000 {
            if await condition() { return }
            try? await Task.sleep(nanoseconds: 1_000_000)
        }
        XCTFail("Timed out waiting for native input admission")
    }
}

private actor NativeReadAdmission {
    private(set) var requests = 0
    private var enabled = false
    private var waiters: [CheckedContinuation<Bool, Never>] = []

    func acquire() async -> Bool {
        requests += 1
        if enabled { return true }
        return await withCheckedContinuation { waiters.append($0) }
    }

    func resume() {
        enabled = true
        let waiting = waiters
        waiters = []
        waiting.forEach { $0.resume(returning: true) }
    }

    func cancel() {
        let waiting = waiters
        waiters = []
        waiting.forEach { $0.resume(returning: false) }
    }
}

private actor NativeTargetEvents {
    private(set) var bytes = Data()
    private(set) var largestChunk = 0
    private(set) var failures = 0
    private(set) var ends = 0

    func record(_ event: TargetConnectionEvent) {
        switch event {
        case let .data(data): bytes.append(data); largestChunk = max(largestChunk, data.count)
        case .ended: ends += 1
        case .failed: failures += 1
        case .ready: break
        }
    }
}

final class LoopbackTargetServer: @unchecked Sendable {
    private let listener: NWListener
    private let queue = DispatchQueue(label: "test.mobile-egress.target-server")
    private var connection: NWConnection?

    init() throws { listener = try NWListener(using: .tcp, on: .any) }

    func start() async throws -> NWEndpoint.Port {
        try await withCheckedThrowingContinuation { continuation in
            listener.stateUpdateHandler = { [weak self] state in
                guard let self else { return }
                switch state {
                case .ready:
                    self.listener.stateUpdateHandler = nil
                    continuation.resume(returning: self.listener.port!)
                case let .failed(error):
                    self.listener.stateUpdateHandler = nil
                    continuation.resume(throwing: error)
                default: break
                }
            }
            listener.newConnectionHandler = { [weak self] connection in
                guard let self else { return }
                self.connection = connection
                connection.start(queue: self.queue)
            }
            listener.start(queue: queue)
        }
    }

    func send(_ data: Data, end: Bool) {
        queue.async {
            guard let connection = self.connection else { XCTFail("Server has no connection"); return }
            connection.send(content: data, contentContext: end ? .finalMessage : .defaultMessage, isComplete: true, completion: .contentProcessed { error in
                XCTAssertNil(error)
            })
        }
    }

    func waitForConnection() async {
        for _ in 0 ..< 2_000 {
            if queue.sync(execute: { connection != nil }) { return }
            try? await Task.sleep(nanoseconds: 1_000_000)
        }
        XCTFail("Server did not accept connection")
    }

    func cancel() { queue.async { self.connection?.cancel(); self.listener.cancel() } }
}
#endif
