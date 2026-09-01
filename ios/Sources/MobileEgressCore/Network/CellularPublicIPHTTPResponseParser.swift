import Foundation

#if canImport(Darwin)
import Darwin
#elseif canImport(Glibc)
import Glibc
#endif

public enum CellularPublicIPHTTPResponseParser {
    public static let maximumHeaderBytes = 8 * 1024
    public static let maximumBodyBytes = 128

    public static func parse(
        _ response: Data,
        family: PublicIPFamily,
        maximumBodyBytes: Int = maximumBodyBytes
    ) throws -> String {
        let separator = Data([13, 10, 13, 10])
        let boundedHeaderEnd = response.index(
            response.startIndex,
            offsetBy: min(response.count, maximumHeaderBytes + separator.count)
        )
        guard maximumBodyBytes >= 0,
              let headerRange = response.range(
                  of: separator,
                  in: response.startIndex ..< boundedHeaderEnd
              )
        else {
            throw PublicIPProbeFailure(.malformedResponse)
        }
        let headerByteCount = response.distance(
            from: response.startIndex,
            to: headerRange.lowerBound
        )
        guard headerByteCount <= maximumHeaderBytes else {
            throw PublicIPProbeFailure(.malformedResponse)
        }

        let lines = splitHeaderLines(Array(response[..<headerRange.lowerBound]))
        guard let statusLine = lines.first,
              let status = parseStatusLine(statusLine)
        else {
            throw PublicIPProbeFailure(.malformedResponse)
        }

        var contentLengths: [Int] = []
        var hasTransferEncoding = false
        for line in lines.dropFirst() {
            guard !line.isEmpty,
                  line[0] != 32 && line[0] != 9,
                  let colon = line.firstIndex(of: 58)
            else {
                throw PublicIPProbeFailure(.malformedResponse, httpStatus: status)
            }
            let name = line[..<colon]
            let rawValue = line[line.index(after: colon)...]
            guard !name.isEmpty,
                  name.allSatisfy(isHTTPTokenByte),
                  rawValue.allSatisfy(isAllowedHeaderValueByte)
            else {
                throw PublicIPProbeFailure(.malformedResponse, httpStatus: status)
            }
            let lowercaseName = name.map(lowercaseASCII)
            let value = trimSpaces(rawValue)
            if lowercaseName.elementsEqual(Array("transfer-encoding".utf8)) {
                hasTransferEncoding = true
            }
            if lowercaseName.elementsEqual(Array("content-length".utf8)) {
                guard !value.isEmpty,
                      value.allSatisfy({ (48 ... 57).contains($0) }),
                      let length = Int(String(decoding: value, as: UTF8.self))
                else {
                    throw PublicIPProbeFailure(.malformedResponse, httpStatus: status)
                }
                contentLengths.append(length)
            }
        }

        if hasTransferEncoding {
            throw PublicIPProbeFailure(.unsupportedTransferEncoding, httpStatus: status)
        }
        guard contentLengths.count == 1 else {
            throw PublicIPProbeFailure(.malformedResponse, httpStatus: status)
        }
        let contentLength = contentLengths[0]
        guard contentLength <= maximumBodyBytes else {
            throw PublicIPProbeFailure(.responseTooLarge, httpStatus: status)
        }
        let body = response[headerRange.upperBound...]
        guard body.count == contentLength else {
            throw PublicIPProbeFailure(.malformedResponse, httpStatus: status)
        }
        guard (200 ... 299).contains(status) else {
            throw PublicIPProbeFailure(.httpStatus, httpStatus: status)
        }
        guard let rawValue = String(data: body, encoding: .utf8) else {
            throw PublicIPProbeFailure(.invalidAddress, httpStatus: status)
        }
        let value = rawValue.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !value.isEmpty else {
            throw PublicIPProbeFailure(.invalidAddress, httpStatus: status)
        }

        switch family {
        case .ipv4:
            if isStrictIPv4Literal(value) { return value }
            if isStrictIPv6Literal(value) {
                throw PublicIPProbeFailure(.wrongAddressFamily, httpStatus: status)
            }
        case .ipv6:
            if isStrictIPv6Literal(value) { return value.lowercased() }
            if isStrictIPv4Literal(value) {
                throw PublicIPProbeFailure(.wrongAddressFamily, httpStatus: status)
            }
        }
        throw PublicIPProbeFailure(.invalidAddress, httpStatus: status)
    }

    private static func splitHeaderLines(_ bytes: [UInt8]) -> [[UInt8]] {
        var lines: [[UInt8]] = []
        var lineStart = bytes.startIndex
        var index = bytes.startIndex
        while index + 1 < bytes.endIndex {
            if bytes[index] == 13, bytes[index + 1] == 10 {
                lines.append(Array(bytes[lineStart ..< index]))
                index += 2
                lineStart = index
            } else {
                index += 1
            }
        }
        lines.append(Array(bytes[lineStart ..< bytes.endIndex]))
        return lines
    }

    private static func parseStatusLine(_ line: [UInt8]) -> Int? {
        let prefix = Array("HTTP/1.1 ".utf8)
        guard line.count >= prefix.count + 5,
              line.prefix(prefix.count).elementsEqual(prefix),
              line[9 ..< 12].allSatisfy({ (48 ... 57).contains($0) }),
              line[12] == 32
        else { return nil }
        let reason = line[13...]
        guard let first = reason.first,
              let last = reason.last,
              first != 32,
              last != 32,
              reason.allSatisfy(isAllowedReasonPhraseByte),
              let status = Int(String(decoding: line[9 ..< 12], as: UTF8.self)),
              (100 ... 599).contains(status)
        else { return nil }
        return status
    }

    private static func isHTTPTokenByte(_ byte: UInt8) -> Bool {
        if (48 ... 57).contains(byte) ||
            (65 ... 90).contains(byte) ||
            (97 ... 122).contains(byte) {
            return true
        }
        return Array("!#$%&'*+-.^_`|~".utf8).contains(byte)
    }

    private static func isAllowedReasonPhraseByte(_ byte: UInt8) -> Bool {
        byte >= 32 && byte != 127
    }

    private static func isAllowedHeaderValueByte(_ byte: UInt8) -> Bool {
        byte >= 32 && byte != 127
    }

    private static func lowercaseASCII(_ byte: UInt8) -> UInt8 {
        (65 ... 90).contains(byte) ? byte + 32 : byte
    }

    private static func trimSpaces(_ bytes: ArraySlice<UInt8>) -> ArraySlice<UInt8> {
        guard let first = bytes.firstIndex(where: { $0 != 32 }),
              let last = bytes.lastIndex(where: { $0 != 32 })
        else { return bytes[bytes.endIndex ..< bytes.endIndex] }
        return bytes[first ... last]
    }

    private static func isStrictIPv4Literal(_ value: String) -> Bool {
        let parts = value.split(separator: ".", omittingEmptySubsequences: false)
        guard parts.count == 4 else { return false }
        return parts.allSatisfy { part in
            guard !part.isEmpty,
                  part.count <= 3,
                  part.allSatisfy(\.isASCIIWholeNumber),
                  part.count == 1 || part.first != "0",
                  let number = Int(part)
            else { return false }
            return (0 ... 255).contains(number)
        }
    }

    private static func isStrictIPv6Literal(_ value: String) -> Bool {
        guard value.contains(":"),
              value.allSatisfy({ $0.isHexDigit || $0 == ":" || $0 == "." })
        else { return false }
        var address = in6_addr()
        return value.withCString { pointer in
            inet_pton(AF_INET6, pointer, &address) == 1
        }
    }
}

private extension Character {
    var isASCIIWholeNumber: Bool {
        unicodeScalars.count == 1 && unicodeScalars.first.map { (48 ... 57).contains($0.value) } == true
    }
}
