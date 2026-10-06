import Foundation
import XCTest
@testable import MobileEgressCore

final class HTTP1CodecTests: XCTestCase {
    func testRequestUsesOneBoundedHTTP11ExchangeAndConnectionClose() throws {
        let body = Data("{\"role\":\"agent\"}".utf8)
        let request = HTTPRequest(
            relayOrigin: "https://relay.example:8443",
            path: "/v1/enroll",
            body: body
        )

        let encoded = try HTTP1Codec.encodeRequest(request)
        let text = try XCTUnwrap(String(data: encoded, encoding: .utf8))

        XCTAssertTrue(text.hasPrefix("POST /v1/enroll HTTP/1.1\r\n"))
        XCTAssertTrue(text.contains("Host: relay.example:8443\r\n"))
        XCTAssertTrue(text.contains("Content-Type: application/json\r\n"))
        XCTAssertTrue(text.contains("Content-Length: \(body.count)\r\n"))
        XCTAssertTrue(text.contains("Connection: close\r\n"))
        XCTAssertTrue(text.hasSuffix("\r\n\r\n{\"role\":\"agent\"}"))
    }

    func testResponseRequiresOneStrictContentLengthAndExactBody() throws {
        let body = Data("{\"ok\":true}".utf8)
        let response = try HTTP1Codec.parseResponse(rawResponse(body: body))

        XCTAssertEqual(response.statusCode, 201)
        XCTAssertEqual(response.body, body)
        XCTAssertEqual(response.singleHeader(named: "content-type"), "application/json")
    }

    func testResponseRejectsAmbiguityTruncationAndTrailingBytes() throws {
        let invalidResponses = [
            Data("HTTP/1.1 200 OK\r\nContent-Length: 0\r\nContent-Length: 0\r\n\r\n".utf8),
            Data("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n{}".utf8),
            Data("HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\n{".utf8),
            Data("HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\n{}extra".utf8),
            Data("HTTP/1.1 200 OK\r\nContent-Length: 0\r\n folded:true\r\n\r\n".utf8),
            Data("HTTP/1.1 200 OK\r\nX-Injected: allowed\nInjected: true\r\nContent-Length: 0\r\n\r\n".utf8),
        ]

        for response in invalidResponses {
            XCTAssertThrowsError(try HTTP1Codec.parseResponse(response))
        }
    }

    func testChunkedResponseDecodesFragmentedBodyOnlyAtEOF() throws {
        let response = chunkedResponse("2\r\n{\"\r\n4\r\nok\":\r\n5\r\ntrue}\r\n0\r\n\r\n")
        var accumulator = HTTP1ResponseAccumulator()
        for byte in response {
            XCTAssertEqual(try accumulator.receive([byte], isComplete: false), .awaitingMoreData)
        }
        guard case let .complete(decoded) = try accumulator.receive(Data(), isComplete: true) else {
            return XCTFail("A complete chunked response must finish at EOF")
        }
        XCTAssertEqual(decoded.statusCode, 201)
        XCTAssertEqual(decoded.body, Data("{\"ok\":true}".utf8))
        XCTAssertEqual(decoded.singleHeader(named: "content-type"), "application/json")
    }

    func testChunkedResponseRejectsConflictingOrUnsupportedFraming() throws {
        for header in [
            "Transfer-Encoding: chunked\r\nContent-Length: 0",
            "Transfer-Encoding: chunked\r\nTransfer-Encoding: chunked",
            "Transfer-Encoding: gzip, chunked",
            "Transfer-Encoding: identity",
            "Transfer-Encoding: chunked, chunked",
        ] {
            let response = Data("HTTP/1.1 201 Created\r\n\(header)\r\n\r\n0\r\n\r\n".utf8)
            XCTAssertThrowsError(try HTTP1Codec.parseResponse(response), header)
        }
    }

    func testChunkedResponseRequiresExactTerminatorsAndNoTrailingBytes() throws {
        for body in ["", "1", "1\r", "1\r\n", "1\r\nx", "1\r\nx\r", "1\r\nx\n0\r\n\r\n",
                     "1\r\nx\r\n", "0\r\n", "0\r\n\r", "0\r\n\r\nextra", "-1\r\nx\r\n0\r\n\r\n",
                     "G\r\nx\r\n0\r\n\r\n", "FFFFFFFFFFFFFFFFFFFFFFFF\r\n", "1;ignored=true\r\nx\r\n0\r\n\r\n",
                     "0\r\nX-Trailer: value\r\n\r\n"] {
            XCTAssertThrowsError(try HTTP1Codec.parseResponse(chunkedResponse(body)), body)
        }
        var accumulator = HTTP1ResponseAccumulator()
        XCTAssertEqual(try accumulator.receive(chunkedResponse("0\r\n\r\n"), isComplete: false), .awaitingMoreData)
        XCTAssertThrowsError(try accumulator.receive(Data("x".utf8), isComplete: false))
    }

    func testChunkedResponseBoundsDecodedBodyAndFramingBeforeEOF() throws {
        var tooLarge = HTTP1ResponseAccumulator()
        XCTAssertThrowsError(try tooLarge.receive(chunkedResponse("40001\r\n"), isComplete: false)) { error in
            XCTAssertEqual(error as? HTTP1Error, .bodyTooLarge)
        }
        var metadata = HTTP1ResponseAccumulator()
        XCTAssertThrowsError(try metadata.receive(chunkedResponse(String(repeating: "0", count: 32 * 1024 + 1)), isComplete: false))
        var manyChunks = HTTP1ResponseAccumulator()
        XCTAssertThrowsError(try manyChunks.receive(chunkedResponse(String(repeating: "1\r\nx\r\n", count: 7_000)), isComplete: false))

        let maximumBody = String(repeating: "x", count: 256 * 1024)
        XCTAssertEqual(try HTTP1Codec.parseResponse(chunkedResponse("40000\r\n\(maximumBody)\r\n0\r\n\r\n")).body.count, 256 * 1024)
        let cumulative = chunkedResponse("40000\r\n\(maximumBody)\r\n1\r\nx\r\n0\r\n\r\n")
        XCTAssertThrowsError(try HTTP1Codec.parseResponse(cumulative)) { error in
            XCTAssertEqual(error as? HTTP1Error, .bodyTooLarge)
        }
    }

    private func chunkedResponse(_ body: String) -> Data {
        Data("HTTP/1.1 201 Created\r\nContent-Type: application/json\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n\(body)".utf8)
    }

    func testResponseEnforcesHeaderAndBodyLimits() throws {
        let oversizedHeader = String(repeating: "a", count: HTTP1Limits.maximumHeaderBytes + 1)
        let headerResponse = Data("HTTP/1.1 200 OK\r\nX-Large: \(oversizedHeader)\r\nContent-Length: 0\r\n\r\n".utf8)
        let bodyResponse = Data("HTTP/1.1 200 OK\r\nContent-Length: \(HTTP1Limits.maximumBodyBytes + 1)\r\n\r\n".utf8)

        XCTAssertThrowsError(try HTTP1Codec.parseResponse(headerResponse))
        XCTAssertThrowsError(try HTTP1Codec.parseResponse(bodyResponse))
    }

    func testResponseAccumulatorWaitsForEOFAndRejectsTrailingBytesInALaterCallback() throws {
        let body = Data("{\"ok\":true}".utf8)
        let response = rawResponse(body: body)
        let bodyEnd = response.index(response.endIndex, offsetBy: -1)
        var accumulator = HTTP1ResponseAccumulator()

        XCTAssertEqual(
            try accumulator.receive(response[..<bodyEnd], isComplete: false),
            .awaitingMoreData
        )
        XCTAssertEqual(
            try accumulator.receive(response[bodyEnd...], isComplete: false),
            .awaitingMoreData
        )
        XCTAssertThrowsError(try accumulator.receive(Data("x".utf8), isComplete: false)) { error in
            XCTAssertEqual(error as? HTTP1Error, .ambiguousResponse)
        }
    }

    func testResponseAccumulatorSucceedsOnlyAtEOFWithExactContentLength() throws {
        let body = Data("{\"ok\":true}".utf8)
        let response = rawResponse(body: body)
        var accumulator = HTTP1ResponseAccumulator()

        XCTAssertEqual(try accumulator.receive(response, isComplete: false), .awaitingMoreData)
        XCTAssertEqual(
            try accumulator.receive(Data(), isComplete: true),
            .complete(try HTTP1Codec.parseResponse(response))
        )
    }

    private func rawResponse(body: Data) -> Data {
        var data = Data("HTTP/1.1 201 Created\r\nContent-Type: application/json\r\nContent-Length: \(body.count)\r\nConnection: close\r\n\r\n".utf8)
        data.append(body)
        return data
    }
}
