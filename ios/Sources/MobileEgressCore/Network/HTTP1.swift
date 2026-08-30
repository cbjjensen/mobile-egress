import Foundation

public enum HTTP1Limits {
    public static let maximumHeaderBytes = 32 * 1024
    public static let maximumBodyBytes = 256 * 1024
}

public enum HTTP1Error: Error, Equatable {
    case invalidRequest
    case requestTooLarge
    case invalidResponse
    case headerTooLarge
    case bodyTooLarge
    case truncatedResponse
    case ambiguousResponse
}

public struct HTTPRequest: Equatable, Sendable {
    public let relayOrigin: String
    public let path: String
    public let body: Data

    public init(relayOrigin: String, path: String, body: Data) {
        self.relayOrigin = relayOrigin
        self.path = path
        self.body = body
    }
}

public struct HTTPResponse: Equatable, Sendable {
    public let statusCode: Int
    public let headers: [String: [String]]
    public let body: Data

    public init(statusCode: Int, headers: [String: [String]], body: Data) {
        self.statusCode = statusCode
        self.headers = headers.reduce(into: [:]) { result, item in
            result[item.key.lowercased()] = item.value
        }
        self.body = body
    }

    public func singleHeader(named name: String) -> String? {
        guard let values = headers[name.lowercased()], values.count == 1 else { return nil }
        return values[0]
    }
}

public enum HTTP1ResponseAccumulatorOutcome: Equatable, Sendable {
    case awaitingMoreData
    case complete(HTTPResponse)
}

public struct HTTP1ResponseAccumulator: Sendable {
    private var data = Data()

    public init() {}

    public mutating func receive(
        _ content: some DataProtocol,
        isComplete: Bool
    ) throws -> HTTP1ResponseAccumulatorOutcome {
        data.append(contentsOf: content)
        if let expected = try HTTP1Codec.expectedResponseBytes(in: data), data.count > expected {
            throw HTTP1Error.ambiguousResponse
        }
        guard isComplete else { return .awaitingMoreData }
        return .complete(try HTTP1Codec.parseResponse(data))
    }
}

public protocol HTTPTransporting: Sendable {
    func execute(
        _ request: HTTPRequest,
        configuration: PinnedCellularTransportConfiguration
    ) async throws -> HTTPResponse
}

public enum HTTP1Codec {
    public static func encodeRequest(_ request: HTTPRequest) throws -> Data {
        guard request.body.count <= HTTP1Limits.maximumBodyBytes,
              request.path.hasPrefix("/"),
              request.path.utf8.allSatisfy({ $0 >= 0x21 && $0 <= 0x7E && $0 != 0x20 })
        else {
            throw request.body.count > HTTP1Limits.maximumBodyBytes ? HTTP1Error.requestTooLarge : HTTP1Error.invalidRequest
        }
        let endpoint = try RelayEndpoint(origin: request.relayOrigin)
        let head = """
        POST \(request.path) HTTP/1.1\r
        Host: \(endpoint.hostHeader)\r
        Content-Type: application/json\r
        Content-Length: \(request.body.count)\r
        Connection: close\r
        \r

        """
        guard let headData = head.data(using: .utf8), headData.count <= HTTP1Limits.maximumHeaderBytes else {
            throw HTTP1Error.invalidRequest
        }
        var encoded = headData
        encoded.append(request.body)
        return encoded
    }

    public static func parseResponse(_ data: Data) throws -> HTTPResponse {
        guard let parsedHead = try parseHead(in: data) else {
            if data.count > HTTP1Limits.maximumHeaderBytes { throw HTTP1Error.headerTooLarge }
            throw HTTP1Error.truncatedResponse
        }
        let expectedCount = parsedHead.bodyOffset + parsedHead.contentLength
        guard data.count == expectedCount else {
            throw data.count < expectedCount ? HTTP1Error.truncatedResponse : HTTP1Error.ambiguousResponse
        }
        return HTTPResponse(
            statusCode: parsedHead.statusCode,
            headers: parsedHead.headers,
            body: data.subdata(in: parsedHead.bodyOffset ..< expectedCount)
        )
    }

    static func expectedResponseBytes(in data: Data) throws -> Int? {
        guard let parsedHead = try parseHead(in: data) else {
            if data.count > HTTP1Limits.maximumHeaderBytes { throw HTTP1Error.headerTooLarge }
            return nil
        }
        return parsedHead.bodyOffset + parsedHead.contentLength
    }

    private static func parseHead(in data: Data) throws -> ParsedHead? {
        let separator = Data([0x0D, 0x0A, 0x0D, 0x0A])
        guard let separatorRange = data.range(of: separator) else { return nil }
        guard separatorRange.lowerBound <= HTTP1Limits.maximumHeaderBytes else {
            throw HTTP1Error.headerTooLarge
        }
        let bodyOffset = separatorRange.upperBound
        let headData = data.subdata(in: data.startIndex ..< separatorRange.lowerBound)
        guard headData.allSatisfy({ $0 == 0x09 || ($0 >= 0x20 && $0 <= 0x7E) || $0 == 0x0D || $0 == 0x0A }),
              let head = String(data: headData, encoding: .ascii)
        else {
            throw HTTP1Error.invalidResponse
        }
        let lines = head.components(separatedBy: "\r\n")
        guard let statusLine = lines.first else { throw HTTP1Error.invalidResponse }
        let statusPieces = statusLine.split(separator: " ", maxSplits: 2, omittingEmptySubsequences: false)
        guard statusPieces.count == 3,
              statusPieces[0] == "HTTP/1.1",
              statusPieces[1].count == 3,
              statusPieces[1].allSatisfy(\.isNumber),
              let statusCode = Int(statusPieces[1]),
              (100 ... 599).contains(statusCode)
        else {
            throw HTTP1Error.invalidResponse
        }

        var headers: [String: [String]] = [:]
        for line in lines.dropFirst() {
            guard !line.isEmpty,
                  line.first != " ", line.first != "\t",
                  let colon = line.firstIndex(of: ":")
            else {
                throw HTTP1Error.invalidResponse
            }
            let name = String(line[..<colon])
            guard !name.isEmpty, name.utf8.allSatisfy(isHeaderNameByte) else {
                throw HTTP1Error.invalidResponse
            }
            let valueStart = line.index(after: colon)
            let value = line[valueStart...].trimmingCharacters(in: CharacterSet(charactersIn: " \t"))
            guard value.utf8.allSatisfy({ $0 == 0x09 || ($0 >= 0x20 && $0 <= 0x7E) }) else {
                throw HTTP1Error.invalidResponse
            }
            let normalizedName = name.lowercased()
            headers[normalizedName, default: []].append(value)
        }
        guard headers["transfer-encoding"] == nil,
              let contentLengths = headers["content-length"],
              contentLengths.count == 1,
              let literal = contentLengths.first,
              !literal.isEmpty,
              literal.utf8.allSatisfy({ $0 >= 0x30 && $0 <= 0x39 }),
              let contentLength = Int(literal)
        else {
            throw HTTP1Error.ambiguousResponse
        }
        guard contentLength <= HTTP1Limits.maximumBodyBytes else { throw HTTP1Error.bodyTooLarge }
        return ParsedHead(
            statusCode: statusCode,
            headers: headers,
            bodyOffset: bodyOffset,
            contentLength: contentLength
        )
    }

    private static func isHeaderNameByte(_ byte: UInt8) -> Bool {
        switch byte {
        case 0x21, 0x23 ... 0x27, 0x2A, 0x2B, 0x2D, 0x2E, 0x30 ... 0x39,
             0x41 ... 0x5A, 0x5E ... 0x60, 0x61 ... 0x7A, 0x7C, 0x7E:
            true
        default:
            false
        }
    }

    private struct ParsedHead {
        let statusCode: Int
        let headers: [String: [String]]
        let bodyOffset: Int
        let contentLength: Int
    }
}
