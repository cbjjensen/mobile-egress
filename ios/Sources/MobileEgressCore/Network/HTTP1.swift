import Foundation

public enum HTTP1Limits {
    public static let maximumHeaderBytes = 32 * 1024
    public static let maximumBodyBytes = 256 * 1024
    public static let maximumChunkMetadataBytes = 32 * 1024
    static let maximumResponseBytes = maximumHeaderBytes + 4 + maximumBodyBytes + maximumChunkMetadataBytes
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
    public let method: String
    public let relayOrigin: String
    public let path: String
    public let body: Data

    public init(relayOrigin: String, path: String, body: Data, method: String = "POST") {
        self.method = method
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
    private var head: HTTP1Codec.ParsedHead?
    private var chunks: HTTP1ChunkedBody?

    public init() {}

    public mutating func receive(
        _ content: some DataProtocol,
        isComplete: Bool
    ) throws -> HTTP1ResponseAccumulatorOutcome {
        guard content.count <= HTTP1Limits.maximumResponseBytes - data.count else { throw HTTP1Error.bodyTooLarge }
        data.append(contentsOf: content)
        if head == nil { head = try HTTP1Codec.parseHead(in: data) }
        guard let head else {
            if data.count > HTTP1Limits.maximumHeaderBytes { throw HTTP1Error.headerTooLarge }
            if isComplete { throw HTTP1Error.truncatedResponse }
            return .awaitingMoreData
        }
        let expected: Int?
        switch head.framing {
        case let .contentLength(length): expected = head.bodyOffset + length
        case .chunked:
            if chunks == nil { chunks = HTTP1ChunkedBody(cursor: head.bodyOffset) }
            expected = try chunks?.advance(in: data)
        }
        if let expected, data.count > expected { throw HTTP1Error.ambiguousResponse }
        guard isComplete else { return .awaitingMoreData }
        guard let expected, data.count == expected else { throw HTTP1Error.truncatedResponse }
        let body: Data
        if let chunks {
            body = chunks.bodyRanges.reduce(into: Data()) { $0.append(data[$1]) }
        } else {
            body = data.subdata(in: head.bodyOffset..<expected)
        }
        return .complete(HTTPResponse(statusCode: head.statusCode, headers: head.headers, body: body))
    }
}

/// Advances once over each chunk. Keep wire bytes bounded, and materialize the
/// decoded body only after exact framing and clean EOF have both been observed.
private struct HTTP1ChunkedBody: Sendable {
    var cursor: Int
    private var chunkSize: Int?
    private var decodedBytes = 0
    private var metadataBytes = 0
    private var end: Int?
    private(set) var bodyRanges: [Range<Int>] = []

    init(cursor: Int) { self.cursor = cursor }

    mutating func advance(in data: Data) throws -> Int? {
        if let end { return end }
        while true {
            if chunkSize == nil {
                let searchEnd = min(data.count, cursor + 18)
                guard let separator = data.range(of: Data([13, 10]), in: cursor..<searchEnd) else {
                    if data.count - cursor > 17 { throw HTTP1Error.invalidResponse }
                    return nil
                }
                let digits = data[cursor..<separator.lowerBound]
                guard !digits.isEmpty, digits.count <= 16,
                      digits.allSatisfy({ (48...57).contains($0) || (65...70).contains($0) || (97...102).contains($0) }),
                      let literal = String(data: digits, encoding: .ascii), let size = Int(literal, radix: 16)
                else { throw HTTP1Error.invalidResponse }
                guard size <= HTTP1Limits.maximumBodyBytes - decodedBytes else { throw HTTP1Error.bodyTooLarge }
                try chargeMetadata(digits.count + 2)
                decodedBytes += size
                cursor = separator.upperBound
                chunkSize = size
            }
            guard let size = chunkSize else { throw HTTP1Error.invalidResponse }
            let chunkEnd = cursor + size
            guard data.count >= chunkEnd + 2 else { return nil }
            guard data[chunkEnd] == 13, data[chunkEnd + 1] == 10 else { throw HTTP1Error.invalidResponse }
            try chargeMetadata(2)
            if size > 0 { bodyRanges.append(cursor..<chunkEnd) }
            cursor = chunkEnd + 2
            chunkSize = nil
            if size == 0 { end = cursor; return cursor }
        }
    }

    private mutating func chargeMetadata(_ count: Int) throws {
        guard count <= HTTP1Limits.maximumChunkMetadataBytes - metadataBytes else { throw HTTP1Error.headerTooLarge }
        metadataBytes += count
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
        guard ["GET", "POST"].contains(request.method), request.body.count <= HTTP1Limits.maximumBodyBytes,
              request.path.hasPrefix("/"),
              request.path.utf8.allSatisfy({ $0 >= 0x21 && $0 <= 0x7E && $0 != 0x20 })
        else {
            throw request.body.count > HTTP1Limits.maximumBodyBytes ? HTTP1Error.requestTooLarge : HTTP1Error.invalidRequest
        }
        let endpoint = try RelayEndpoint(origin: request.relayOrigin)
        let head = """
        \(request.method) \(request.path) HTTP/1.1\r
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
        var accumulator = HTTP1ResponseAccumulator()
        guard case let .complete(response) = try accumulator.receive(data, isComplete: true) else {
            throw HTTP1Error.truncatedResponse
        }
        return response
    }

    fileprivate static func parseHead(in data: Data) throws -> ParsedHead? {
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
        if let transferEncodings = headers["transfer-encoding"] {
            guard headers["content-length"] == nil, transferEncodings.count == 1,
                  transferEncodings[0].lowercased() == "chunked" else { throw HTTP1Error.ambiguousResponse }
            return ParsedHead(statusCode: statusCode, headers: headers, bodyOffset: bodyOffset, framing: .chunked)
        }
        guard let contentLengths = headers["content-length"],
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
            framing: .contentLength(contentLength)
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

    fileprivate enum BodyFraming: Sendable { case contentLength(Int), chunked }

    fileprivate struct ParsedHead: Sendable {
        let statusCode: Int
        let headers: [String: [String]]
        let bodyOffset: Int
        let framing: BodyFraming
    }
}
