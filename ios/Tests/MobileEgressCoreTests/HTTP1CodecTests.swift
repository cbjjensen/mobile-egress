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

    func testResponseRejectsChunkingAmbiguityTruncationAndTrailingBytes() throws {
        let invalidResponses = [
            Data("HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n0\r\n\r\n".utf8),
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

    func testResponseEnforcesHeaderAndBodyLimits() throws {
        let oversizedHeader = String(repeating: "a", count: HTTP1Limits.maximumHeaderBytes + 1)
        let headerResponse = Data("HTTP/1.1 200 OK\r\nX-Large: \(oversizedHeader)\r\nContent-Length: 0\r\n\r\n".utf8)
        let bodyResponse = Data("HTTP/1.1 200 OK\r\nContent-Length: \(HTTP1Limits.maximumBodyBytes + 1)\r\n\r\n".utf8)

        XCTAssertThrowsError(try HTTP1Codec.parseResponse(headerResponse))
        XCTAssertThrowsError(try HTTP1Codec.parseResponse(bodyResponse))
    }

    private func rawResponse(body: Data) -> Data {
        var data = Data("HTTP/1.1 201 Created\r\nContent-Type: application/json\r\nContent-Length: \(body.count)\r\nConnection: close\r\n\r\n".utf8)
        data.append(body)
        return data
    }
}
