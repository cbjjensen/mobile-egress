import Foundation
import XCTest
@testable import MobileEgressCore
#if canImport(Network)
import Network
#endif

final class AgentSessionRuntimeTests: XCTestCase {
    func testFairTurnWaitOwnsSharedDebtAndStreamCancellationRefundsIt() async throws {
        let budget = DirectPhoneBudget(frameLimit: 1, byteLimit: 4096, controlLimit: 10)
        let blocker = budget.readTurns.makePeer()
        let blocking = await budget.readTurns.acquire(blocker)
        let relay = RecordingRelayWebSocket(automaticallyCompletesSends: false)
        let target = RecordingTargetConnection()
        let runtime = AgentSessionRuntime(relay: relay, targetFactory: RecordingTargetConnectionFactory(target: target), sharedBudget: budget)
        await runtime.start(); await relay.emit(.connected)
        let open = try WireProtocol.encode(type: .open, streamID: "held", payload: Data(#"{"ip":"8.8.8.8","port":443}"#.utf8))
        await relay.emit(.message(.init(opcode: .binary, payload: open, isComplete: true)))
        await target.emit(.ready); await relay.completeNextSend(.success(()))
        let delivery = Task { await target.emit(.data(Data([1, 2, 3]))) }
        await waitUntil { budget.readTurns.waitingCount == 1 }
        XCTAssertNil(budget.acquire(.outbound, bytes: 1), "a chunk waiting for its fair turn must already own shared debt")
        let close = try WireProtocol.encode(type: .close, streamID: "held")
        await relay.emit(.message(.init(opcode: .binary, payload: close, isComplete: true)))
        await delivery.value
        XCTAssertEqual(budget.readTurns.waitingCount, 0)
        XCTAssertNotNil(budget.acquire(.outbound, bytes: 1))
        blocking?.complete()
        await runtime.stop()
    }
    func testTenPeersIsolateSameStreamIDsAndNativeDebtSurvivesStoppedGeneration() async throws {
        let budget = DirectPhoneBudget(frameLimit: 10, byteLimit: 4096, controlLimit: 30)
        var runtimes: [AgentSessionRuntime] = []
        var relays: [RecordingRelayWebSocket] = []
        var targets: [RecordingTargetConnection] = []
        for _ in 0..<10 {
            let relay = RecordingRelayWebSocket(automaticallyCompletesSends: false)
            let target = RecordingTargetConnection(automaticallyCompletesSends: false)
            let runtime = AgentSessionRuntime(relay: relay, targetFactory: RecordingTargetConnectionFactory(target: target), sharedBudget: budget)
            await runtime.start(); await relay.emit(.connected)
            let open = try WireProtocol.encode(type: .open, streamID: "same-id", payload: Data(#"{"ip":"8.8.8.8","port":443}"#.utf8))
            await relay.emit(.message(.init(opcode: .binary, payload: open, isComplete: true)))
            await target.emit(.ready); await relay.completeNextSend(.success(()))
            runtimes.append(runtime); relays.append(relay); targets.append(target)
        }
        for index in 0..<10 {
            await targets[index].emit(.data(Data([UInt8(index)])))
            let frame = try WireProtocol.encode(type: .data, streamID: "same-id", payload: Data([UInt8(index)]))
            await relays[index].emit(.message(.init(opcode: .binary, payload: frame, isComplete: true)))
        }
        XCTAssertNil(budget.acquire(.outbound, bytes: 1))
        XCTAssertNil(budget.acquire(.inbound, bytes: 1))
        await runtimes[0].stop()
        XCTAssertNil(budget.acquire(.outbound, bytes: 1))
        XCTAssertNil(budget.acquire(.inbound, bytes: 1))
        for index in 1..<10 {
            let status = await runtimes[index].snapshot()
            XCTAssertEqual(status.activeStreamCount, 1)
            XCTAssertEqual(targets[index].sentData, [Data([UInt8(index)])])
        }
        await targets[0].completeNextSend(.failure(.failed))
        await relays[0].completeNextSend(.failure(.unavailable))
        XCTAssertNotNil(budget.acquire(.outbound, bytes: 1))
        XCTAssertNotNil(budget.acquire(.inbound, bytes: 1))
        for index in 1..<10 {
            await relays[index].completeNextSend(.success(()))
            await targets[index].completeNextSend(.success(()))
            await runtimes[index].stop()
        }
    }
    #if canImport(Network)
    func testRealNativeTargetDrainsExactDownloadTailThroughStalledRuntimeRelay() async throws {
        let server = try LoopbackTargetServer()
        let port = try await server.start()
        let relay = RecordingRelayWebSocket(automaticallyCompletesSends: false)
        let runtime = AgentSessionRuntime(relay: relay, targetFactory: NativeLoopbackTargetFactory(port: port), sharedBudget: DirectPhoneBudget())
        await runtime.start()
        await relay.emit(.connected)
        let open = try WireProtocol.encode(type: .open, streamID: "native-download", payload: Data(#"{"ip":"8.8.8.8","port":443}"#.utf8))
        await relay.emit(.message(.init(opcode: .binary, payload: open, isComplete: true)))
        await waitUntil { relay.pendingSends > 0 }
        await relay.completeNextSend(.success(()))
        await server.waitForConnection()
        let expected = Data((0 ..< (2 * 1_024 * 1_024 + 37)).map { UInt8($0 % 251) })
        server.send(expected, end: true)
        await waitUntil { await runtime.snapshot().bytesDownloaded > 0 }
        try await Task.sleep(nanoseconds: 50_000_000)
        let paused = await runtime.snapshot()
        XCTAssertEqual(paused.activeStreamCount, 1)
        XCTAssertLessThanOrEqual(paused.bytesDownloaded, 32 * 16 * 1_024)
        XCTAssertLessThan(paused.bytesDownloaded, UInt64(expected.count))
        for _ in 0 ..< 2_000 {
            if relay.pendingSends > 0 { await relay.completeNextSend(.success(())) }
            if await runtime.snapshot().activeStreamCount == 0 { break }
            try await Task.sleep(nanoseconds: 1_000_000)
        }
        var received = Data()
        for bytes in relay.sentBinary {
            let frame = try WireProtocol.parseAgentOutbound(bytes)
            if frame.type == .data { received.append(try frame.decodedPayload()) }
        }
        XCTAssertEqual(received, expected)
        let last = try WireProtocol.parseAgentOutbound(XCTUnwrap(relay.sentBinary.last))
        XCTAssertEqual(last.type, .close)
        XCTAssertEqual(try last.decodedPayload(), Data("target_closed".utf8))
        await runtime.stop()
        server.cancel()
    }
    #endif

    func testPausedStreamAllowsOtherTargetProgressAndRemoteCloseReleasesWaiter() async throws {
        let relay = RecordingRelayWebSocket(automaticallyCompletesSends: false)
        let first = RecordingTargetConnection()
        let second = RecordingTargetConnection()
        let runtime = AgentSessionRuntime(relay: relay, targetFactory: SequencedTargetConnectionFactory(targets: [first, second]))
        await runtime.start()
        await relay.emit(.connected)
        for (id, target) in [("first", first), ("second", second)] {
            let open = try WireProtocol.encode(type: .open, streamID: id, payload: Data(#"{"ip":"8.8.8.8","port":443}"#.utf8))
            await relay.emit(.message(.init(opcode: .binary, payload: open, isComplete: true)))
            await target.emit(.ready)
            await relay.completeNextSend(.success(()))
        }
        let producer = Task {
            for _ in 0 ..< 64 { await first.emit(.data(Data([0x41]))) }
        }
        await waitUntil { await runtime.snapshot().bytesDownloaded == 32 }
        await second.emit(.data(Data([0x42])))
        let concurrent = await runtime.snapshot()
        XCTAssertEqual(concurrent.bytesDownloaded, 33)
        XCTAssertEqual(concurrent.activeStreamCount, 2)
        let close = try WireProtocol.encode(type: .close, streamID: "first", payload: Data("client_closed".utf8))
        await relay.emit(.message(.init(opcode: .binary, payload: close, isComplete: true)))
        await producer.value
        await relay.completeNextSend(.success(()))
        let last = try WireProtocol.parseAgentOutbound(XCTUnwrap(relay.sentBinary.last))
        XCTAssertEqual(last.streamID, "second")
        XCTAssertEqual(last.type, .data)
        XCTAssertEqual(try last.decodedPayload(), Data([0x42]))
        XCTAssertEqual(first.cancelCount, 1)
        XCTAssertEqual(second.cancelCount, 0)
        await runtime.stop()
    }

    func testSlowRelayPausesAndResumesSustainedTargetDownloadWithoutResetInBothModes() async throws {
        for binary in [false, true] {
            let relay = RecordingRelayWebSocket(automaticallyCompletesSends: false)
            let target = RecordingTargetConnection()
            let runtime = AgentSessionRuntime(relay: relay, targetFactory: RecordingTargetConnectionFactory(target: target))
            await runtime.start()
            await relay.emit(.connected)
            if binary {
                let advertisement = try WireProtocol.encode(type: .ping, payload: WireProtocol.transportV2Advertisement)
                await relay.emit(.message(.init(opcode: .binary, payload: advertisement, isComplete: true)))
                await relay.completeNextSend(.success(()))
            }
            let open = try WireProtocol.encode(type: .open, streamID: "download", payload: Data(#"{"ip":"8.8.8.8","port":443}"#.utf8))
            await relay.emit(.message(.init(opcode: .binary, payload: open, isComplete: true)))
            await target.emit(.ready)
            await relay.completeNextSend(.success(()))

            let progress = DownloadProgress()
            let producer = Task {
                for index in 0 ..< 96 {
                    await target.emit(.data(Data(repeating: UInt8(index), count: 16 * 1_024)))
                    await progress.advance()
                }
                await target.emit(.ended)
            }
            await waitUntil { await runtime.snapshot().bytesDownloaded >= 32 * 16 * 1_024 }
            let stalled = await runtime.snapshot()
            let completedCallbacks = await progress.count
            XCTAssertEqual(stalled.bytesDownloaded, 32 * 16 * 1_024)
            XCTAssertEqual(stalled.activeStreamCount, 1)
            XCTAssertLessThanOrEqual(completedCallbacks, 32, "the next native read must wait for capacity")
            XCTAssertEqual(target.cancelCount, 0)

            for _ in 0 ..< 400 {
                if relay.pendingSends > 0 { await relay.completeNextSend(.success(())) }
                if await runtime.snapshot().activeStreamCount == 0 { break }
                try await Task.sleep(nanoseconds: 1_000_000)
            }
            let delivered = try relay.sentBinary.compactMap { bytes -> Data? in
                let frame = try WireProtocol.parseAgentOutbound(bytes, transportV2: binary)
                return frame.type == .data ? try frame.decodedPayload() : nil
            }
            XCTAssertEqual(delivered.count, 96)
            for (index, bytes) in delivered.enumerated() {
                XCTAssertEqual(bytes, Data(repeating: UInt8(index), count: 16 * 1_024))
            }
            let last = try XCTUnwrap(relay.sentBinary.last)
            XCTAssertEqual(try WireProtocol.parseAgentOutbound(last, transportV2: binary).type, .close)
            await runtime.stop()
            await producer.value
        }
    }

    func testStopUnblocksTargetWaitingForRelayCapacity() async throws {
        let relay = RecordingRelayWebSocket(automaticallyCompletesSends: false)
        let target = RecordingTargetConnection()
        let runtime = AgentSessionRuntime(relay: relay, targetFactory: RecordingTargetConnectionFactory(target: target))
        await runtime.start()
        await relay.emit(.connected)
        let open = try WireProtocol.encode(type: .open, streamID: "download", payload: Data(#"{"ip":"8.8.8.8","port":443}"#.utf8))
        await relay.emit(.message(.init(opcode: .binary, payload: open, isComplete: true)))
        await target.emit(.ready)
        await relay.completeNextSend(.success(()))
        let producer = Task {
            for _ in 0 ..< 64 { await target.emit(.data(Data([0x41]))) }
        }
        await waitUntil { await runtime.snapshot().bytesDownloaded >= 32 }
        let before = await runtime.snapshot()
        XCTAssertEqual(before.activeStreamCount, 1)
        XCTAssertEqual(target.cancelCount, 0)
        await runtime.stop()
        await producer.value
        XCTAssertEqual(target.cancelCount, 1)
        let after = await runtime.snapshot()
        XCTAssertEqual(after.activeStreamCount, 0)
        XCTAssertEqual(after.connectionState, .stopped)
    }

    private func waitUntil(_ condition: () async -> Bool) async {
        for _ in 0 ..< 2_000 {
            if await condition() { return }
            try? await Task.sleep(nanoseconds: 1_000_000)
        }
        XCTFail("Timed out waiting for runtime progress")
    }

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

    func testRuntimeCountsCanceledRelaySendAgainstReusedStreamUntilCompletion() async throws {
        let relay = RecordingRelayWebSocket(automaticallyCompletesSends: false)
        let originalTarget = RecordingTargetConnection()
        let replacementTarget = RecordingTargetConnection()
        let runtime = AgentSessionRuntime(
            relay: relay,
            targetFactory: SequencedTargetConnectionFactory(targets: [originalTarget, replacementTarget])
        )
        await runtime.start()
        await relay.emit(.connected)

        let open = try WireProtocol.encode(
            type: .open,
            streamID: "stream",
            payload: Data(#"{"ip":"8.8.8.8","port":443}"#.utf8)
        )
        await relay.emit(.message(.init(opcode: .binary, payload: open, isComplete: true)))
        await originalTarget.emit(.ready)
        await relay.completeNextSend(.success(()))

        await originalTarget.emit(.data(Data([0x41])))
        await originalTarget.emit(.failed)
        await relay.emit(.message(.init(opcode: .binary, payload: open, isComplete: true)))
        await replacementTarget.emit(.ready)

        let producer = Task {
            for byte in 0 ..< 32 { await replacementTarget.emit(.data(Data([UInt8(byte)]))) }
        }
        await waitUntil { await runtime.snapshot().bytesDownloaded == 32 }

        let saturated = await runtime.snapshot()
        XCTAssertEqual(saturated.activeStreamCount, 1)
        XCTAssertEqual(replacementTarget.cancelCount, 0)

        await relay.completeNextSend(.success(()))
        await producer.value
        let resumed = await runtime.snapshot()
        XCTAssertEqual(resumed.bytesDownloaded, 33, "reused stream waits until the old transport-owned frame is refunded")
        await runtime.stop()
    }

    func testRelaySendCompletionRefundsOnlyCompletedFrameWhileQueuedAndInFlightRemainCharged() async throws {
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

        for byte in 0 ..< 32 {
            await target.emit(.data(Data([UInt8(byte)])))
        }
        XCTAssertEqual(relay.sentBinary.count, 2, "one opened control and one in-flight data send")
        XCTAssertEqual(target.cancelCount, 0)

        await relay.completeNextSend(.success(()))
        XCTAssertEqual(relay.sentBinary.count, 3, "completion starts exactly one queued send")
        await target.emit(.data(Data([32])))
        XCTAssertEqual(target.cancelCount, 0, "one completion refunds exactly one frame")
        let waitingRead = Task { await target.emit(.data(Data([33]))) }
        try await Task.sleep(nanoseconds: 20_000_000)
        XCTAssertEqual(target.cancelCount, 0, "31 queued frames plus one in-flight frame pause native reads")
        let saturated = await runtime.snapshot()
        XCTAssertEqual(saturated.connectionState, .connected)
        XCTAssertEqual(saturated.activeStreamCount, 1)
        XCTAssertEqual(saturated.bytesDownloaded, 33)
        await relay.completeNextSend(.success(()))
        await waitingRead.value
        XCTAssertEqual(target.cancelCount, 0)
        let resumed = await runtime.snapshot()
        XCTAssertEqual(resumed.bytesDownloaded, 34)
        await runtime.stop()
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

    func testRuntimeStopProcessesElevenHundredTargetCancellationsExactlyOnce() async throws {
        let relay = RecordingRelayWebSocket()
        let targets = (0 ..< 1_100).map { _ in RecordingTargetConnection() }
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

        let liveSnapshot = await runtime.snapshot()
        XCTAssertEqual(liveSnapshot.activeStreamCount, 1_100)
        XCTAssertTrue(targets.allSatisfy { $0.cancelCount == 0 })
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
    var pendingSends: Int { lock.withLock { sendCompletions.count } }

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

private actor DownloadProgress {
    private(set) var count = 0
    func advance() { count += 1 }
}

#if canImport(Network)
private struct NativeLoopbackTargetFactory: TargetConnectionFactory {
    let port: NWEndpoint.Port

    func makeConnection(configuration: TargetConnectionConfiguration) throws -> any TargetConnectionIO {
        NetworkTargetConnection(
            endpoints: [.hostPort(host: "127.0.0.1", port: port)],
            parameters: .tcp, readChunkBytes: configuration.readChunkBytes, connectTimeout: 5
        )
    }
}
#endif

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
    private var readReadiness: TargetConnectionReadReadiness?
    private var cancellations = 0
    private var sends: [Data] = []
    private var sendCompletions: [TargetConnectionSendCompletion] = []
    private let automaticallyCompletesSends: Bool

    init(automaticallyCompletesSends: Bool = true) {
        self.automaticallyCompletesSends = automaticallyCompletesSends
    }

    var cancelCount: Int { lock.withLock { cancellations } }
    var sentData: [Data] { lock.withLock { sends } }

    func start(eventHandler: @escaping TargetConnectionEventHandler, readReadiness: @escaping TargetConnectionReadReadiness) {
        lock.withLock {
            self.eventHandler = eventHandler
            self.readReadiness = readReadiness
        }
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
        if case .data = event, let readiness = lock.withLock({ readReadiness }), !(await readiness()) { return }
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
