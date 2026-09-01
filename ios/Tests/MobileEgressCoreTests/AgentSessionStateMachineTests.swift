import Foundation
import XCTest
@testable import MobileEgressCore

final class AgentSessionStateMachineTests: XCTestCase {
    func testWebSocketControlPingDoesNotReplaceApplicationWirePingPong() throws {
        var machine = connectedMachine()

        XCTAssertTrue(machine.receiveRelay(.init(opcode: .ping, payload: Data([0x01]), isComplete: true)).isEmpty)
        XCTAssertTrue(machine.receiveRelay(.init(opcode: .pong, payload: Data([0x01]), isComplete: true)).isEmpty)
        XCTAssertNil(machine.nextOutbound())

        XCTAssertTrue(machine.receiveRelay(try binary(type: .ping)).isEmpty)
        let frame = try XCTUnwrap(machine.nextOutbound())
        let envelope = try WireProtocol.parseAgentOutbound(frame.bytes)

        XCTAssertEqual(envelope.type, .pong)
        XCTAssertEqual(envelope.streamID, "")
        XCTAssertEqual(try envelope.decodedPayload(), Data())
        XCTAssertTrue(machine.completeOutbound(frame, accepted: true).isEmpty)
        XCTAssertEqual(machine.snapshot.connectionState, .connected)
    }

    func testTextContinuationOversizeAndMalformedBinaryTerminateAsProtocolFailures() throws {
        let invalidMessages: [RelayWebSocketMessage] = [
            .init(opcode: .text, payload: Data("text".utf8), isComplete: true),
            .init(opcode: .continuation, payload: Data(), isComplete: true),
            .init(opcode: .unknown, payload: Data(), isComplete: true),
            .init(opcode: .ping, payload: Data(repeating: 0, count: 126), isComplete: true),
            .init(opcode: .binary, payload: Data(repeating: 0, count: WireProtocol.maximumWebSocketMessageBytes + 1), isComplete: true),
            .init(opcode: .binary, payload: Data("not-json".utf8), isComplete: true),
        ]

        for message in invalidMessages {
            var machine = connectedMachine()
            let effects = machine.receiveRelay(message)

            XCTAssertEqual(effects, [.closeRelay(code: 1008, reason: "protocol_error")])
            XCTAssertEqual(machine.snapshot.connectionState, .stopping)
            XCTAssertEqual(machine.snapshot.errorClass, .protocol)
        }
    }

    func testOpenOpenedDataAndGracefulCloseFlowPreservesCountersAndOrder() throws {
        var machine = connectedMachine()
        let opened = try openTarget(&machine, streamID: "stream", ip: "8.8.8.8", port: 443)
        machine.targetWasCreated(streamID: "stream", token: opened.token)

        XCTAssertTrue(machine.targetConnected(streamID: "stream", token: opened.token).isEmpty)
        try assertOutbound(&machine, type: .opened, streamID: "stream", payload: Data())

        let upload = Data("upload".utf8)
        let writeEffects = machine.receiveRelay(try binary(type: .data, streamID: "stream", payload: upload))
        let write = try XCTUnwrap(writeEffects.singleTargetWrite)
        XCTAssertEqual(write.data, upload)
        XCTAssertTrue(machine.targetWriteCompleted(
            streamID: "stream",
            token: opened.token,
            writeID: write.writeID,
            succeeded: true
        ).isEmpty)

        let download = Data("download".utf8)
        XCTAssertTrue(machine.targetReceived(streamID: "stream", token: opened.token, data: download).isEmpty)
        XCTAssertTrue(machine.targetEnded(streamID: "stream", token: opened.token).isEmpty)
        XCTAssertEqual(machine.snapshot.activeStreamCount, 1)
        XCTAssertEqual(machine.snapshot.bytesUploaded, UInt64(upload.count))
        XCTAssertEqual(machine.snapshot.bytesDownloaded, UInt64(download.count))

        try assertOutbound(&machine, type: .data, streamID: "stream", payload: download)
        let terminal = try popOutbound(&machine, type: .close, streamID: "stream", payload: Data("target_closed".utf8))
        let releaseEffects = machine.completeOutbound(terminal, accepted: true)

        XCTAssertEqual(releaseEffects, [.cancelTarget(streamID: "stream", token: opened.token)])
        XCTAssertEqual(machine.snapshot.activeStreamCount, 0)
        XCTAssertEqual(machine.snapshot.connectionState, .connected)
    }

    func testSuccessfulTargetWritesContinueWhileReadEOFIsGracefullyClosing() throws {
        var machine = connectedMachine()
        let opened = try openReadyTarget(&machine, streamID: "stream")
        let first = try XCTUnwrap(machine.receiveRelay(try binary(
            type: .data,
            streamID: "stream",
            payload: Data([0x41])
        )).singleTargetWrite)
        XCTAssertTrue(machine.receiveRelay(try binary(
            type: .data,
            streamID: "stream",
            payload: Data([0x42])
        )).isEmpty)
        XCTAssertTrue(machine.targetEnded(streamID: "stream", token: opened.token).isEmpty)
        XCTAssertNil(machine.nextOutbound())

        let next = try XCTUnwrap(machine.targetWriteCompleted(
            streamID: "stream",
            token: opened.token,
            writeID: first.writeID,
            succeeded: true
        ).singleTargetWrite)

        XCTAssertEqual(next.data, Data([0x42]))
        XCTAssertNil(machine.nextOutbound())
        XCTAssertTrue(machine.targetWriteCompleted(
            streamID: "stream",
            token: opened.token,
            writeID: next.writeID,
            succeeded: true
        ).isEmpty)
        XCTAssertEqual(machine.snapshot.bytesUploaded, 2)
        let terminal = try popOutbound(
            &machine,
            type: .close,
            streamID: "stream",
            payload: Data("target_closed".utf8)
        )
        XCTAssertEqual(
            machine.completeOutbound(terminal, accepted: true),
            [.cancelTarget(streamID: "stream", token: opened.token)]
        )
    }

    func testFailedTargetWriteAfterReadEOFDisablesLateWritesAndCancelsTargetOnce() throws {
        var machine = connectedMachine()
        let opened = try openReadyTarget(&machine, streamID: "stream")
        let inFlight = try XCTUnwrap(machine.receiveRelay(try binary(
            type: .data,
            streamID: "stream",
            payload: Data([0x41])
        )).singleTargetWrite)
        XCTAssertTrue(machine.targetEnded(streamID: "stream", token: opened.token).isEmpty)

        XCTAssertEqual(
            machine.targetWriteCompleted(
                streamID: "stream",
                token: opened.token,
                writeID: inFlight.writeID,
                succeeded: false
            ),
            [.cancelTarget(streamID: "stream", token: opened.token)]
        )
        XCTAssertTrue(machine.receiveRelay(try binary(
            type: .data,
            streamID: "stream",
            payload: Data([0x42])
        )).isEmpty)

        let terminal = try popOutbound(
            &machine,
            type: .close,
            streamID: "stream",
            payload: Data("target_closed".utf8)
        )
        XCTAssertTrue(machine.completeOutbound(terminal, accepted: true).isEmpty)
        XCTAssertEqual(machine.snapshot.activeStreamCount, 0)
        XCTAssertEqual(machine.snapshot.bytesUploaded, 0)
    }

    func testStrictOpenPolicyRejectsBeforeTargetCreation() throws {
        let cases: [(Data, String, AgentRuntimeErrorClass)] = [
            (Data(#"{"ip":"8.8.8.8","port":443.0}"#.utf8), "invalid_target", .none),
            (Data(#"{"ip":"8.8.8.8","port":443,"extra":true}"#.utf8), "invalid_target", .none),
            (Data(#"{"ip":"example.com","port":443}"#.utf8), "policy_denied", .targetPolicy),
            (Data(#"{"ip":"10.0.0.1","port":443}"#.utf8), "policy_denied", .targetPolicy),
        ]

        for (payload, code, errorClass) in cases {
            var machine = connectedMachine()
            let effects = machine.receiveRelay(try binary(type: .open, streamID: "blocked", payload: payload))

            XCTAssertFalse(effects.containsTargetCreation)
            try assertOutbound(&machine, type: .rejected, streamID: "blocked", payload: Data(code.utf8))
            XCTAssertEqual(machine.snapshot.activeStreamCount, 0)
            XCTAssertEqual(machine.snapshot.errorClass, errorClass)
        }
    }

    func testInjectedStateMachineLimitsFlowIntoCreatedTargetConfiguration() throws {
        let limits = AgentRuntimeLimits(
            maximumStreams: 7,
            tombstones: 11,
            outboundControls: 13,
            outboundData: 5,
            outboundDataPerStream: 2,
            targetInbound: 1,
            targetReadChunkBytes: 4 * 1_024,
            maximumInboundDataBytes: 8 * 1_024
        )
        var machine = connectedMachine(limits: limits)

        let target = try openTarget(&machine, streamID: "stream", ip: "8.8.8.8", port: 443)

        XCTAssertEqual(target.configuration.readChunkBytes, 4 * 1_024)
        XCTAssertEqual(target.configuration.inboundQueueCapacity, 1)
        XCTAssertEqual(target.configuration.connectTimeout, 30)
    }

    func testProductionAdmissionAcceptsTwoHundredFiftySixRejectsNextAndImmediatelyReusesReleasedSlot() throws {
        var machine = connectedMachine()
        var firstToken: UInt64?

        for index in 0 ..< 256 {
            let effects = machine.receiveRelay(try openMessage(streamID: "stream-\(index)", ip: "8.8.8.8", port: 443))
            XCTAssertTrue(effects.containsTargetCreation)
            if index == 0 {
                firstToken = effects.singleTargetCreation?.token
            }
        }

        let overflow = machine.receiveRelay(try openMessage(streamID: "stream-256", ip: "8.8.8.8", port: 443))
        XCTAssertFalse(overflow.containsTargetCreation)
        try assertOutbound(&machine, type: .rejected, streamID: "stream-256", payload: Data("agent_stream_limit".utf8))
        XCTAssertEqual(machine.snapshot.activeStreamCount, 256)

        let close = try binary(type: .close, streamID: "stream-0", payload: Data("client_closed".utf8))
        XCTAssertTrue(machine.receiveRelay(close).isEmpty)
        XCTAssertEqual(machine.snapshot.activeStreamCount, 255)

        let reused = machine.receiveRelay(try openMessage(streamID: "stream-0", ip: "1.1.1.1", port: 443))
        let reusedTarget = try XCTUnwrap(reused.singleTargetCreation)
        let originalToken = try XCTUnwrap(firstToken)
        XCTAssertNotEqual(reusedTarget.token, originalToken)
        XCTAssertEqual(machine.snapshot.activeStreamCount, 256)

        machine.targetWasCreated(streamID: "stream-0", token: originalToken)
        XCTAssertTrue(machine.targetCreationFailed(
            streamID: "stream-0",
            token: originalToken
        ).isEmpty)
        XCTAssertEqual(machine.snapshot.activeStreamCount, 256)
        machine.targetWasCreated(streamID: "stream-0", token: reusedTarget.token)
        XCTAssertTrue(machine.targetConnected(streamID: "stream-0", token: reusedTarget.token).isEmpty)
        try assertOutbound(&machine, type: .opened, streamID: "stream-0", payload: Data())
    }

    func testTargetBoundOutstandingLimitCountsInFlightAndQueuedFrames() throws {
        var machine = connectedMachine()
        let opened = try openReadyTarget(&machine, streamID: "stream")

        let first = machine.receiveRelay(try binary(type: .data, streamID: "stream", payload: Data([0])))
        XCTAssertNotNil(first.singleTargetWrite)
        XCTAssertTrue(machine.receiveRelay(try binary(type: .data, streamID: "stream", payload: Data([1]))).isEmpty)
        XCTAssertEqual(machine.capacitySnapshot.targetOutstandingFrames["stream"], 2)

        let overflow = machine.receiveRelay(try binary(type: .data, streamID: "stream", payload: Data([2])))

        XCTAssertEqual(overflow, [.cancelTarget(streamID: "stream", token: opened.token)])
        try assertOutbound(&machine, type: .close, streamID: "stream", payload: Data("agent_unavailable".utf8))
        XCTAssertEqual(machine.snapshot.activeStreamCount, 0)
        XCTAssertEqual(machine.snapshot.errorClass, .backpressure)
        XCTAssertEqual(machine.snapshot.connectionState, .connected)
        XCTAssertEqual(machine.capacitySnapshot.targetOutstandingFrames["stream"] ?? 0, 0)
    }

    func testPerStreamOutboundOverflowDiscardsDataAndPrioritizesForcedClose() throws {
        var machine = connectedMachine()
        let opened = try openReadyTarget(&machine, streamID: "stream")

        for value in 0 ..< 2 {
            XCTAssertTrue(machine.targetReceived(streamID: "stream", token: opened.token, data: Data([UInt8(value)])).isEmpty)
        }
        let overflow = machine.targetReceived(streamID: "stream", token: opened.token, data: Data([2]))

        XCTAssertEqual(overflow, [.cancelTarget(streamID: "stream", token: opened.token)])
        try assertOutbound(&machine, type: .close, streamID: "stream", payload: Data("agent_unavailable".utf8))
        XCTAssertNil(machine.nextOutbound())
        XCTAssertEqual(machine.snapshot.bytesDownloaded, 2)
        XCTAssertEqual(machine.snapshot.errorClass, .backpressure)
    }

    func testTotalOutboundOverflowFailsContributingStreamAndLeavesOthersActive() throws {
        var machine = connectedMachine()
        var opened: [(String, UInt64)] = []
        for index in 0 ..< 129 {
            let streamID = "stream-\(index)"
            let target = try openReadyTarget(&machine, streamID: streamID)
            opened.append((streamID, target.token))
        }

        for (streamID, token) in opened.prefix(128) {
            for value in 0 ..< 2 {
                XCTAssertTrue(machine.targetReceived(streamID: streamID, token: token, data: Data([UInt8(value)])).isEmpty)
            }
        }
        let last = try XCTUnwrap(opened.last)
        let overflow = machine.targetReceived(streamID: last.0, token: last.1, data: Data([0xFF]))

        XCTAssertEqual(overflow, [.cancelTarget(streamID: last.0, token: last.1)])
        try assertOutbound(&machine, type: .close, streamID: last.0, payload: Data("agent_unavailable".utf8))
        XCTAssertEqual(machine.snapshot.activeStreamCount, 128)
        XCTAssertEqual(machine.snapshot.bytesDownloaded, 256)
    }

    func testRequiredControlSaturationTerminatesWholeSession() throws {
        var machine = connectedMachine()

        for _ in 0 ..< 512 {
            XCTAssertTrue(machine.receiveRelay(try binary(type: .ping)).isEmpty)
        }
        let overflow = machine.receiveRelay(try binary(type: .ping))

        XCTAssertEqual(overflow, [.cancelRelay])
        XCTAssertEqual(machine.snapshot.connectionState, .stopping)
        XCTAssertEqual(machine.snapshot.errorClass, .backpressure)
        XCTAssertNil(machine.nextOutbound())
    }

    func testDuplicateAndTombstonedLateCloseAreIdempotentButLateDataIsProtocolFailure() throws {
        var machine = connectedMachine()
        let opened = try openTarget(&machine, streamID: "stream", ip: "8.8.8.8", port: 443)
        machine.targetWasCreated(streamID: "stream", token: opened.token)
        let close = try binary(type: .close, streamID: "stream", payload: Data("client_closed".utf8))

        XCTAssertEqual(machine.receiveRelay(close), [.cancelTarget(streamID: "stream", token: opened.token)])
        XCTAssertTrue(machine.receiveRelay(close).isEmpty)
        XCTAssertEqual(machine.snapshot.activeStreamCount, 0)

        let lateData = try binary(type: .data, streamID: "stream", payload: Data([0x01]))
        XCTAssertEqual(machine.receiveRelay(lateData), [.closeRelay(code: 1008, reason: "protocol_error")])
        XCTAssertEqual(machine.snapshot.errorClass, .protocol)
    }

    func testUnknownStreamCloseIsProtocolFailure() throws {
        var machine = connectedMachine()

        let effects = machine.receiveRelay(try binary(
            type: .close,
            streamID: "unknown",
            payload: Data("client_closed".utf8)
        ))

        XCTAssertEqual(effects, [.closeRelay(code: 1008, reason: "protocol_error")])
        XCTAssertEqual(machine.snapshot.errorClass, .protocol)
    }

    func testCloseWithUnknownErrorCodeIsProtocolFailure() throws {
        var machine = connectedMachine()
        _ = try openTarget(&machine, streamID: "stream", ip: "8.8.8.8", port: 443)

        let effects = machine.receiveRelay(try binary(
            type: .close,
            streamID: "stream",
            payload: Data("not_a_finite_code".utf8)
        ))

        XCTAssertEqual(effects, [.closeRelay(code: 1008, reason: "protocol_error")])
        XCTAssertEqual(machine.snapshot.errorClass, .protocol)
    }

    func testTargetCreationAndConnectFailuresUseFiniteTargetFailureFrames() throws {
        var creationMachine = connectedMachine()
        let creating = try openTarget(&creationMachine, streamID: "creating", ip: "8.8.8.8", port: 443)

        XCTAssertTrue(creationMachine.targetCreationFailed(streamID: "creating", token: creating.token).isEmpty)
        try assertOutbound(
            &creationMachine,
            type: .rejected,
            streamID: "creating",
            payload: Data("target_failure".utf8)
        )
        XCTAssertEqual(creationMachine.snapshot.activeStreamCount, 0)
        XCTAssertEqual(creationMachine.snapshot.errorClass, .targetConnect)

        var connectMachine = connectedMachine()
        let connecting = try openTarget(&connectMachine, streamID: "connecting", ip: "8.8.8.8", port: 443)
        connectMachine.targetWasCreated(streamID: "connecting", token: connecting.token)

        XCTAssertEqual(
            connectMachine.targetFailed(streamID: "connecting", token: connecting.token),
            [.cancelTarget(streamID: "connecting", token: connecting.token)]
        )
        try assertOutbound(
            &connectMachine,
            type: .close,
            streamID: "connecting",
            payload: Data("target_failure".utf8)
        )
        XCTAssertEqual(connectMachine.snapshot.activeStreamCount, 0)
        XCTAssertEqual(connectMachine.snapshot.errorClass, .targetConnect)
    }

    func testRelaySendFailureTerminatesAndCancelsEveryTarget() throws {
        var machine = connectedMachine()
        let opened = try openTarget(&machine, streamID: "stream", ip: "8.8.8.8", port: 443)
        machine.targetWasCreated(streamID: "stream", token: opened.token)
        XCTAssertTrue(machine.targetConnected(streamID: "stream", token: opened.token).isEmpty)
        let frame = try XCTUnwrap(machine.nextOutbound())

        let effects = machine.completeOutbound(frame, accepted: false)

        XCTAssertEqual(effects, [
            .cancelRelay,
            .cancelTarget(streamID: "stream", token: opened.token),
        ])
        XCTAssertEqual(machine.snapshot.connectionState, .stopping)
        XCTAssertEqual(machine.snapshot.activeStreamCount, 0)
        XCTAssertEqual(machine.snapshot.errorClass, .relayUnavailable)
    }

    func testFailedRelaySendTerminatesAfterItsStreamWasClosedWhileInFlight() throws {
        var machine = connectedMachine()
        let first = try openReadyTarget(&machine, streamID: "first")
        let second = try openReadyTarget(&machine, streamID: "second")
        XCTAssertTrue(machine.targetReceived(
            streamID: "first",
            token: first.token,
            data: Data([0x41])
        ).isEmpty)
        let inFlight = try popOutbound(
            &machine,
            type: .data,
            streamID: "first",
            payload: Data([0x41])
        )

        let closeEffects = machine.receiveRelay(try binary(
            type: .close,
            streamID: "first",
            payload: Data("client_closed".utf8)
        ))
        let failureEffects = machine.completeOutbound(inFlight, accepted: false)

        XCTAssertEqual(closeEffects, [.cancelTarget(streamID: "first", token: first.token)])
        XCTAssertEqual(failureEffects, [
            .cancelRelay,
            .cancelTarget(streamID: "second", token: second.token),
        ])
        XCTAssertEqual(machine.snapshot.connectionState, .stopping)
        XCTAssertEqual(machine.snapshot.activeStreamCount, 0)
        XCTAssertEqual(machine.snapshot.errorClass, .relayUnavailable)
    }

    func testSuccessfulRelaySendCompletionForCanceledFrameIsIgnored() throws {
        var machine = connectedMachine()
        let opened = try openReadyTarget(&machine, streamID: "stream")
        XCTAssertTrue(machine.targetReceived(
            streamID: "stream",
            token: opened.token,
            data: Data([0x42])
        ).isEmpty)
        let inFlight = try popOutbound(
            &machine,
            type: .data,
            streamID: "stream",
            payload: Data([0x42])
        )

        XCTAssertEqual(
            machine.receiveRelay(try binary(
                type: .close,
                streamID: "stream",
                payload: Data("client_closed".utf8)
            )),
            [.cancelTarget(streamID: "stream", token: opened.token)]
        )

        XCTAssertTrue(machine.completeOutbound(inFlight, accepted: true).isEmpty)
        XCTAssertEqual(machine.snapshot.connectionState, .connected)
        XCTAssertEqual(machine.snapshot.activeStreamCount, 0)
        XCTAssertEqual(machine.snapshot.errorClass, .none)
        XCTAssertNil(machine.nextOutbound())
    }

    func testCanceledInFlightFrameCannotAffectReusedStreamID() throws {
        var machine = connectedMachine()
        let original = try openReadyTarget(&machine, streamID: "stream")
        XCTAssertTrue(machine.targetReceived(
            streamID: "stream",
            token: original.token,
            data: Data([0x41])
        ).isEmpty)
        let oldFrame = try popOutbound(
            &machine,
            type: .data,
            streamID: "stream",
            payload: Data([0x41])
        )

        XCTAssertEqual(
            machine.receiveRelay(try binary(
                type: .close,
                streamID: "stream",
                payload: Data("client_closed".utf8)
            )),
            [.cancelTarget(streamID: "stream", token: original.token)]
        )
        let replacement = try openReadyTarget(&machine, streamID: "stream")
        XCTAssertNotEqual(replacement.token, original.token)
        XCTAssertTrue(machine.targetReceived(
            streamID: "stream",
            token: replacement.token,
            data: Data([0x42])
        ).isEmpty)

        XCTAssertTrue(machine.completeOutbound(oldFrame, accepted: true).isEmpty)
        XCTAssertEqual(machine.snapshot.activeStreamCount, 1)
        try assertOutbound(&machine, type: .data, streamID: "stream", payload: Data([0x42]))
    }

    func testStaleTargetWriteCompletionCannotAffectReusedStreamID() throws {
        var machine = connectedMachine()
        let original = try openReadyTarget(&machine, streamID: "stream")
        let oldWrite = try XCTUnwrap(machine.receiveRelay(try binary(
            type: .data,
            streamID: "stream",
            payload: Data([0x41])
        )).singleTargetWrite)

        XCTAssertEqual(
            machine.receiveRelay(try binary(
                type: .close,
                streamID: "stream",
                payload: Data("client_closed".utf8)
            )),
            [.cancelTarget(streamID: "stream", token: original.token)]
        )
        let replacement = try openReadyTarget(&machine, streamID: "stream")

        XCTAssertTrue(machine.targetWriteCompleted(
            streamID: "stream",
            token: original.token,
            writeID: oldWrite.writeID,
            succeeded: true
        ).isEmpty)
        XCTAssertEqual(machine.snapshot.activeStreamCount, 1)
        XCTAssertEqual(machine.snapshot.bytesUploaded, 0)

        let replacementWrite = try XCTUnwrap(machine.receiveRelay(try binary(
            type: .data,
            streamID: "stream",
            payload: Data([0x42])
        )).singleTargetWrite)
        XCTAssertTrue(machine.targetWriteCompleted(
            streamID: "stream",
            token: replacement.token,
            writeID: replacementWrite.writeID,
            succeeded: true
        ).isEmpty)
        XCTAssertEqual(machine.snapshot.bytesUploaded, 1)
    }

    func testInboundDataAcceptsThirtyTwoKiBOnceAndRejectsOneByteMore() throws {
        var machine = connectedMachine()
        let opened = try openReadyTarget(&machine, streamID: "stream")
        let acceptedPayload = Data(repeating: 0x41, count: 32 * 1_024)

        let accepted = try XCTUnwrap(machine.receiveRelay(try binary(
            type: .data,
            streamID: "stream",
            payload: acceptedPayload
        )).singleTargetWrite)
        XCTAssertEqual(accepted.data, acceptedPayload)
        XCTAssertEqual(machine.capacitySnapshot.targetOutstandingFrames["stream"], 1)

        let oversized = Data(repeating: 0x42, count: 32 * 1_024 + 1)
        XCTAssertEqual(
            machine.receiveRelay(try binary(type: .data, streamID: "stream", payload: oversized)),
            [
                .closeRelay(code: 1008, reason: "protocol_error"),
                .cancelTarget(streamID: "stream", token: opened.token),
            ]
        )
        XCTAssertEqual(machine.snapshot.errorClass, .protocol)
    }

    func testTargetReadsPreferSixteenKiBOutboundFrames() throws {
        var machine = connectedMachine()
        let opened = try openReadyTarget(&machine, streamID: "stream")
        let payload = Data(repeating: 0x5A, count: 16 * 1_024)

        XCTAssertTrue(machine.targetReceived(
            streamID: "stream",
            token: opened.token,
            data: payload
        ).isEmpty)
        try assertOutbound(&machine, type: .data, streamID: "stream", payload: payload)

        XCTAssertEqual(
            machine.targetReceived(
                streamID: "stream",
                token: opened.token,
                data: Data(repeating: 0x5B, count: 16 * 1_024 + 1)
            ),
            [.cancelTarget(streamID: "stream", token: opened.token)]
        )
        try assertOutbound(
            &machine,
            type: .close,
            streamID: "stream",
            payload: Data("target_failure".utf8)
        )
    }

    func testRejectTombstoneChurnRetainsOnlyNewestOneThousandTwentyFour() throws {
        var machine = connectedMachine()

        for index in 0 ... 1_024 {
            XCTAssertFalse(machine.receiveRelay(try openMessage(
                streamID: "reject-\(index)",
                ip: "10.0.0.1",
                port: 443
            )).containsTargetCreation)
            try assertOutbound(
                &machine,
                type: .rejected,
                streamID: "reject-\(index)",
                payload: Data("policy_denied".utf8)
            )
        }

        XCTAssertEqual(machine.capacitySnapshot.tombstones, 1_024)
        XCTAssertTrue(machine.receiveRelay(try binary(
            type: .close,
            streamID: "reject-1024",
            payload: Data("client_closed".utf8)
        )).isEmpty)
        XCTAssertEqual(
            machine.receiveRelay(try binary(
                type: .close,
                streamID: "reject-0",
                payload: Data("client_closed".utf8)
            )),
            [.closeRelay(code: 1008, reason: "protocol_error")]
        )
    }

    func testStopClearsQueuesAdmissionAndCancelsRelayAndTargetsExactlyOnce() throws {
        var machine = connectedMachine()
        let first = try openTarget(&machine, streamID: "first", ip: "8.8.8.8", port: 443)
        let second = try openTarget(&machine, streamID: "second", ip: "1.1.1.1", port: 443)
        machine.targetWasCreated(streamID: "first", token: first.token)
        machine.targetWasCreated(streamID: "second", token: second.token)
        XCTAssertTrue(machine.receiveRelay(try binary(type: .ping)).isEmpty)

        let effects = machine.stop()

        XCTAssertEqual(Set(effects), Set([
            .closeRelay(code: 1000, reason: "session_closed"),
            .cancelTarget(streamID: "first", token: first.token),
            .cancelTarget(streamID: "second", token: second.token),
        ]))
        XCTAssertEqual(machine.snapshot.connectionState, .stopping)
        XCTAssertEqual(machine.snapshot.activeStreamCount, 0)
        XCTAssertNil(machine.nextOutbound())
        XCTAssertTrue(machine.stop().isEmpty)
        XCTAssertTrue(machine.receiveRelay(try binary(type: .ping)).isEmpty)

        machine.finishStopping()
        XCTAssertEqual(machine.snapshot.connectionState, .stopped)
        XCTAssertTrue(machine.start().isEmpty)
    }

    private func connectedMachine(
        limits: AgentRuntimeLimits = .production
    ) -> AgentSessionStateMachine {
        var machine = AgentSessionStateMachine(limits: limits)
        XCTAssertEqual(machine.start(), [.startRelay])
        machine.relayConnected()
        XCTAssertEqual(machine.snapshot.connectionState, .connected)
        return machine
    }

    private func openReadyTarget(
        _ machine: inout AgentSessionStateMachine,
        streamID: String
    ) throws -> (token: UInt64, configuration: TargetConnectionConfiguration) {
        let opened = try openTarget(&machine, streamID: streamID, ip: "8.8.8.8", port: 443)
        machine.targetWasCreated(streamID: streamID, token: opened.token)
        XCTAssertTrue(machine.targetConnected(streamID: streamID, token: opened.token).isEmpty)
        try assertOutbound(&machine, type: .opened, streamID: streamID, payload: Data())
        return opened
    }

    private func openTarget(
        _ machine: inout AgentSessionStateMachine,
        streamID: String,
        ip: String,
        port: Int
    ) throws -> (token: UInt64, configuration: TargetConnectionConfiguration) {
        let effects = machine.receiveRelay(try openMessage(streamID: streamID, ip: ip, port: port))
        return try XCTUnwrap(effects.singleTargetCreation)
    }

    private func assertOutbound(
        _ machine: inout AgentSessionStateMachine,
        type: WireMessageType,
        streamID: String,
        payload: Data,
        file: StaticString = #filePath,
        line: UInt = #line
    ) throws {
        let frame = try popOutbound(&machine, type: type, streamID: streamID, payload: payload, file: file, line: line)
        XCTAssertTrue(machine.completeOutbound(frame, accepted: true).isEmpty, file: file, line: line)
    }

    private func popOutbound(
        _ machine: inout AgentSessionStateMachine,
        type: WireMessageType,
        streamID: String,
        payload: Data,
        file: StaticString = #filePath,
        line: UInt = #line
    ) throws -> OutboundFrame {
        let frame = try XCTUnwrap(machine.nextOutbound(), file: file, line: line)
        let envelope = try WireProtocol.parseAgentOutbound(frame.bytes)
        XCTAssertEqual(envelope.type, type, file: file, line: line)
        XCTAssertEqual(envelope.streamID, streamID, file: file, line: line)
        XCTAssertEqual(try envelope.decodedPayload(), payload, file: file, line: line)
        return frame
    }

    private func binary(
        type: WireMessageType,
        streamID: String = "",
        payload: Data = Data()
    ) throws -> RelayWebSocketMessage {
        .init(opcode: .binary, payload: try WireProtocol.encode(type: type, streamID: streamID, payload: payload), isComplete: true)
    }

    private func openMessage(streamID: String, ip: String, port: Int) throws -> RelayWebSocketMessage {
        let payload = try JSONSerialization.data(withJSONObject: ["ip": ip, "port": port], options: [.sortedKeys])
        return try binary(type: .open, streamID: streamID, payload: payload)
    }
}

private extension Array where Element == AgentRuntimeEffect {
    var singleTargetCreation: (token: UInt64, configuration: TargetConnectionConfiguration)? {
        guard count == 1,
              case let .createTarget(_, token, configuration) = self[0]
        else { return nil }
        return (token, configuration)
    }

    var singleTargetWrite: (writeID: UInt64, data: Data)? {
        guard count == 1,
              case let .writeTarget(_, _, writeID, data) = self[0]
        else { return nil }
        return (writeID, data)
    }

    var containsTargetCreation: Bool {
        contains { effect in
            if case .createTarget = effect { return true }
            return false
        }
    }
}
