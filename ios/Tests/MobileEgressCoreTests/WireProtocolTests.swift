import Foundation
import XCTest
@testable import MobileEgressCore

final class WireProtocolTests: XCTestCase {
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
        XCTAssertThrowsError(try WireProtocol.encode(type: .data, streamID: "stream-1", payload: Data(repeating: 0, count: WireProtocol.maximumPayloadBytes + 1)))
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
}
