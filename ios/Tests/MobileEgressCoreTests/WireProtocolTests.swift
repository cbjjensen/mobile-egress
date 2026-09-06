import Foundation
import XCTest
@testable import MobileEgressCore

final class WireProtocolTests: XCTestCase {
    func testNegotiatedBinaryLiteralAndBounds() throws {
        let literal = Data([2, 4, 0, 3, 65, 95, 45, 0, 255])
        XCTAssertEqual(try WireProtocol.encode(type: .data, streamID: "A_-", payload: Data([0, 255]), transportV2: true), literal)
        let parsed = try WireProtocol.parseAgentInbound(literal, transportV2: true)
        XCTAssertEqual(parsed.streamID, "A_-")
        XCTAssertEqual(try parsed.decodedPayload(), Data([0, 255]))
        XCTAssertThrowsError(try WireProtocol.parseAgentInbound(literal))
        let max = Data([2, 4, 0, 128]) + Data(repeating: 65, count: 128) + Data(repeating: 255, count: 32768)
        XCTAssertEqual(try WireProtocol.parseAgentInbound(max, transportV2: true).decodedPayload().count, 32768)
        XCTAssertEqual(try WireProtocol.parseAgentInbound(Data([2, 4, 0, 1, 115]), transportV2: true).decodedPayload(), Data())
        for malformed in [Data([2]), Data([2, 4, 0]), Data([2, 3, 0, 1, 115]), Data([2, 4, 0, 0]), Data([2, 4, 0, 2, 115]), Data([2, 4, 0, 1, 255]), Data([2, 4, 0, 1, 32]), Data([2, 4, 0, 129]) + Data(repeating: 65, count: 129), max + Data([1])] {
            XCTAssertThrowsError(try WireProtocol.parseAgentInbound(malformed, transportV2: true))
        }
        XCTAssertThrowsError(try WireProtocol.encode(type: .data, streamID: "s", payload: Data(repeating: 0, count: 32769), transportV2: true))
        let pong = try WireProtocol.encode(type: .pong, transportV2: true)
        XCTAssertEqual(try WireProtocol.parseAgentOutbound(pong).version, 1)
    }

    func testBinaryEnvelopeRoundTripsOpaqueStreamData() throws {
        let encoded = try WireProtocol.encode(type: .data, streamID: "opaque_stream-1", payload: Data([0, 1, 2, 255]))

        let envelope = try WireProtocol.parseAgentInbound(encoded)

        XCTAssertEqual(envelope.type, .data)
        XCTAssertEqual(envelope.streamID, "opaque_stream-1")
        XCTAssertEqual(try envelope.decodedPayload(), Data([0, 1, 2, 255]))
    }

    func testWireProtocolRejectsRoleIncompatibleTypesAndOversizedPayloads() throws {
        let opened = try WireProtocol.encode(type: .opened, streamID: "stream-1")
        XCTAssertThrowsError(try WireProtocol.parseAgentInbound(opened))
        XCTAssertThrowsError(try WireProtocol.encode(type: .open, streamID: "stream-1", payload: Data(repeating: 0, count: WireProtocol.maximumPayloadBytes + 1)))
    }

    func testDataEncodingAcceptsExactlyThirtyTwoKiBAndRejectsOneByteMore() throws {
        let acceptedPayload = Data(repeating: 0x41, count: 32 * 1_024)
        let encoded = try WireProtocol.encode(type: .data, streamID: "stream-1", payload: acceptedPayload)

        XCTAssertEqual(
            try WireProtocol.parseAgentInbound(encoded).decodedPayload(),
            acceptedPayload
        )
        XCTAssertThrowsError(try WireProtocol.encode(
            type: .data,
            streamID: "stream-1",
            payload: Data(repeating: 0x42, count: 32 * 1_024 + 1)
        )) { error in
            XCTAssertEqual(error as? CoreValidationError, .invalidJSON)
        }
    }

    func testDataParsingAcceptsExactlyThirtyTwoKiBAndRejectsOneByteMore() throws {
        let acceptedPayload = Data(repeating: 0x41, count: 32 * 1_024)
        let accepted = try WireProtocol.parseAgentInbound(rawEnvelope(
            type: .data,
            streamID: "stream-1",
            payload: acceptedPayload
        ))

        XCTAssertEqual(try accepted.decodedPayload(), acceptedPayload)
        XCTAssertThrowsError(try WireProtocol.parseAgentInbound(rawEnvelope(
            type: .data,
            streamID: "stream-1",
            payload: Data(repeating: 0x42, count: 32 * 1_024 + 1)
        ))) { error in
            XCTAssertEqual(error as? CoreValidationError, .invalidBase64URL)
        }
    }

    func testNonDataFramesRetainGenericOneMiBPayloadLimit() throws {
        let acceptedPayload = Data(repeating: 0x41, count: 1_024 * 1_024)
        let encoded = try WireProtocol.encode(
            type: .open,
            streamID: "stream-1",
            payload: acceptedPayload
        )

        XCTAssertEqual(try WireProtocol.parseAgentInbound(encoded).decodedPayload(), acceptedPayload)
        let oversizedPayload = Data(repeating: 0x42, count: 1_024 * 1_024 + 1)
        XCTAssertThrowsError(try WireProtocol.encode(
            type: .open,
            streamID: "stream-1",
            payload: oversizedPayload
        ))
        XCTAssertThrowsError(try WireProtocol.parseAgentInbound(rawEnvelope(
            type: .open,
            streamID: "stream-1",
            payload: oversizedPayload
        )))
    }

    func testWireProtocolAcceptsOnlyFiniteErrorCodes() throws {
        XCTAssertEqual(try WireProtocol.finiteErrorCode("agent_stream_limit"), Data("agent_stream_limit".utf8))
        XCTAssertThrowsError(try WireProtocol.finiteErrorCode("arbitrary_error"))
    }

    func testWireProtocolRejectsUnexpectedEnvelopeFields() throws {
        let raw = Data("{\"version\":1,\"type\":\"data\",\"streamId\":\"stream-1\",\"payload\":\"\",\"extra\":true}".utf8)

        XCTAssertThrowsError(try WireProtocol.parseAgentInbound(raw))
    }

    func testWireProtocolRejectsDecimalAndExponentVersionLiterals() throws {
        for version in ["1.0", "1e0"] {
            let raw = Data("{\"version\":\(version),\"type\":\"data\",\"streamId\":\"stream-1\",\"payload\":\"\"}".utf8)

            XCTAssertThrowsError(try WireProtocol.parseAgentInbound(raw))
        }
    }

    func testWireProtocolRejectsDuplicateVersionKeysInEitherOrder() throws {
        for versions in [("1", "1.0"), ("1.0", "1")] {
            let raw = Data("{\"version\":\(versions.0),\"version\":\(versions.1),\"type\":\"data\",\"streamId\":\"stream-1\",\"payload\":\"\"}".utf8)

            XCTAssertThrowsError(try WireProtocol.parseAgentInbound(raw))
        }
    }

    func testWireProtocolRejectsDuplicateNonVersionKeys() throws {
        let raw = Data("{\"version\":1,\"type\":\"data\",\"type\":\"data\",\"streamId\":\"stream-1\",\"payload\":\"\"}".utf8)

        XCTAssertThrowsError(try WireProtocol.parseAgentInbound(raw))
    }

    private func rawEnvelope(type: WireMessageType, streamID: String, payload: Data) -> Data {
        let encodedPayload = payload.base64EncodedString()
            .replacingOccurrences(of: "+", with: "-")
            .replacingOccurrences(of: "/", with: "_")
            .replacingOccurrences(of: "=", with: "")
        return Data(
            "{\"version\":1,\"type\":\"\(type.rawValue)\",\"streamId\":\"\(streamID)\",\"payload\":\"\(encodedPayload)\"}".utf8
        )
    }
}
