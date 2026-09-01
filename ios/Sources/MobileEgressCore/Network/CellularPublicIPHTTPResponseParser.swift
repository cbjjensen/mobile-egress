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
        guard let headerRange = response.range(of: separator) else {
            throw PublicIPProbeFailure(.malformedResponse)
        }
        guard headerRange.lowerBound <= maximumHeaderBytes,
              let header = String(
                data: response[..<headerRange.lowerBound],
                encoding: .utf8
              )
        else {
            throw PublicIPProbeFailure(.malformedResponse)
        }

        let lines = header.components(separatedBy: "\r\n")
        guard let statusLine = lines.first else {
            throw PublicIPProbeFailure(.malformedResponse)
        }
        let statusParts = statusLine.split(separator: " ", omittingEmptySubsequences: true)
        guard statusParts.count >= 2,
              statusParts[0] == "HTTP/1.1",
              statusParts[1].count == 3,
              statusParts[1].allSatisfy(\.isASCIIWholeNumber),
              let status = Int(statusParts[1]),
              (100 ... 599).contains(status)
        else {
            throw PublicIPProbeFailure(.malformedResponse)
        }

        var contentLengths: [Int] = []
        var hasTransferEncoding = false
        for line in lines.dropFirst() {
            guard !line.isEmpty,
                  line.first != " " && line.first != "\t",
                  let colon = line.firstIndex(of: ":")
            else {
                throw PublicIPProbeFailure(.malformedResponse)
            }
            let name = line[..<colon].lowercased()
            let value = line[line.index(after: colon)...]
                .trimmingCharacters(in: .whitespaces)
            guard !name.isEmpty, name.allSatisfy(\.isHTTPTokenCharacter) else {
                throw PublicIPProbeFailure(.malformedResponse)
            }
            if name == "transfer-encoding" {
                hasTransferEncoding = true
            }
            if name == "content-length" {
                guard !value.isEmpty,
                      value.allSatisfy(\.isASCIIWholeNumber),
                      let length = Int(value)
                else {
                    throw PublicIPProbeFailure(.malformedResponse)
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

    var isHTTPTokenCharacter: Bool {
        guard unicodeScalars.count == 1, let scalar = unicodeScalars.first else { return false }
        if (48 ... 57).contains(scalar.value) ||
            (65 ... 90).contains(scalar.value) ||
            (97 ... 122).contains(scalar.value) {
            return true
        }
        return "!#$%&'*+-.^_`|~".unicodeScalars.contains(scalar)
    }
}
