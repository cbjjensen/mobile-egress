import Foundation

#if canImport(Darwin)
import Darwin
#elseif canImport(Glibc)
import Glibc
#endif

public enum CoreValidationError: Error, Equatable {
    case invalidBase64URL
    case invalidJSON
    case invalidPairing
    case invalidMigration
    case invalidRelayOrigin
    case expired
    case certificateAuthorityInvalid
    case certificateAuthorityMismatch
}

struct StrictQRCodeDecoder {
    static let maximumEncodedBytes = 512 * 1024

    static func decode(_ input: String) throws -> Data {
        let encoded = input.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !encoded.isEmpty,
              encoded.utf8.count <= maximumEncodedBytes,
              encoded.allSatisfy({ $0.isASCII && ($0.isLetter || $0.isNumber || $0 == "-" || $0 == "_") }),
              encoded.utf8.count % 4 != 1
        else {
            throw CoreValidationError.invalidBase64URL
        }

        let standard = encoded
            .replacingOccurrences(of: "-", with: "+")
            .replacingOccurrences(of: "_", with: "/")
        let padding = String(repeating: "=", count: (4 - standard.utf8.count % 4) % 4)
        guard let data = Data(base64Encoded: standard + padding) else {
            throw CoreValidationError.invalidBase64URL
        }
        return data
    }
}

struct StrictJSONObject {
    static func exactKeys(in data: Data, expected: Set<String>) throws {
        guard let object = try JSONSerialization.jsonObject(with: data) as? [String: Any],
              Set(object.keys) == expected,
              hasUniqueTopLevelKeys(in: data)
        else {
            throw CoreValidationError.invalidJSON
        }
    }

    static func hasUniqueTopLevelKeys(in data: Data) -> Bool {
        var lexer = JSONObjectLexer(bytes: Array(data))
        return lexer.hasUniqueTopLevelKeys()
    }

    static func hasIntegerLiteral(_ expected: Int, forKey key: String, in data: Data) -> Bool {
        var lexer = JSONObjectLexer(bytes: Array(data))
        return lexer.valueLiteral(forKey: Array(key.utf8)) == Array(String(expected).utf8)
    }

    static func integerLiteral(forKey key: String, in data: Data) -> Int? {
        var lexer = JSONObjectLexer(bytes: Array(data))
        guard let literal = lexer.valueLiteral(forKey: Array(key.utf8)), !literal.isEmpty else { return nil }
        var index = 0
        if literal[index] == 0x2D {
            index += 1
            guard index < literal.count else { return nil }
        }
        if literal[index] == 0x30 {
            guard index + 1 == literal.count else { return nil }
        } else {
            guard (0x31 ... 0x39).contains(literal[index]) else { return nil }
            index += 1
            while index < literal.count {
                guard (0x30 ... 0x39).contains(literal[index]) else { return nil }
                index += 1
            }
        }
        return Int(String(decoding: literal, as: UTF8.self))
    }
}

private struct JSONObjectLexer {
    private let bytes: [UInt8]
    private var index = 0

    init(bytes: [UInt8]) {
        self.bytes = bytes
    }

    mutating func valueLiteral(forKey key: [UInt8]) -> [UInt8]? {
        skipWhitespace()
        guard consume(0x7B) else { return nil }
        skipWhitespace()
        if consume(0x7D) { return nil }

        while true {
            guard let candidateKey = readString() else { return nil }
            skipWhitespace()
            guard consume(0x3A) else { return nil }
            skipWhitespace()
            let valueStart = index
            guard skipValue() else { return nil }
            if candidateKey == key {
                return Array(bytes[valueStart ..< index])
            }
            skipWhitespace()
            if consume(0x7D) { return nil }
            guard consume(0x2C) else { return nil }
            skipWhitespace()
        }
    }

    mutating func hasUniqueTopLevelKeys() -> Bool {
        skipWhitespace()
        guard consume(0x7B) else { return false }
        skipWhitespace()
        if consume(0x7D) {
            skipWhitespace()
            return index == bytes.count
        }

        var keys = Set<[UInt8]>()
        while true {
            guard let key = readString(), !key.contains(0x5C), keys.insert(key).inserted else { return false }
            skipWhitespace()
            guard consume(0x3A) else { return false }
            skipWhitespace()
            guard skipValue() else { return false }
            skipWhitespace()
            if consume(0x7D) {
                skipWhitespace()
                return index == bytes.count
            }
            guard consume(0x2C) else { return false }
            skipWhitespace()
        }
    }

    private mutating func skipValue() -> Bool {
        guard index < bytes.count else { return false }
        switch bytes[index] {
        case 0x22:
            return readString() != nil
        case 0x7B:
            return skipObject()
        case 0x5B:
            return skipArray()
        default:
            let start = index
            while index < bytes.count, !isWhitespace(bytes[index]), bytes[index] != 0x2C, bytes[index] != 0x5D, bytes[index] != 0x7D {
                index += 1
            }
            return index > start
        }
    }

    private mutating func skipObject() -> Bool {
        guard consume(0x7B) else { return false }
        skipWhitespace()
        if consume(0x7D) { return true }
        while true {
            guard readString() != nil else { return false }
            skipWhitespace()
            guard consume(0x3A) else { return false }
            skipWhitespace()
            guard skipValue() else { return false }
            skipWhitespace()
            if consume(0x7D) { return true }
            guard consume(0x2C) else { return false }
            skipWhitespace()
        }
    }

    private mutating func skipArray() -> Bool {
        guard consume(0x5B) else { return false }
        skipWhitespace()
        if consume(0x5D) { return true }
        while true {
            guard skipValue() else { return false }
            skipWhitespace()
            if consume(0x5D) { return true }
            guard consume(0x2C) else { return false }
            skipWhitespace()
        }
    }

    private mutating func readString() -> [UInt8]? {
        guard consume(0x22) else { return nil }
        let start = index
        var escaped = false
        while index < bytes.count {
            let byte = bytes[index]
            index += 1
            if escaped {
                if byte == 0x75 {
                    guard index + 4 <= bytes.count else { return nil }
                    index += 4
                }
                escaped = false
            } else if byte == 0x5C {
                escaped = true
            } else if byte == 0x22 {
                return Array(bytes[start ..< index - 1])
            }
        }
        return nil
    }

    private mutating func skipWhitespace() {
        while index < bytes.count, isWhitespace(bytes[index]) { index += 1 }
    }

    private mutating func consume(_ byte: UInt8) -> Bool {
        guard index < bytes.count, bytes[index] == byte else { return false }
        index += 1
        return true
    }

    private func isWhitespace(_ byte: UInt8) -> Bool {
        byte == 0x20 || byte == 0x09 || byte == 0x0A || byte == 0x0D
    }
}

enum RelayOrigin {
    static func parse(_ value: String) throws -> String {
        guard value.hasPrefix("https://"),
              value.utf8.allSatisfy({ (0x21 ... 0x7E).contains($0) && $0 != 0x25 && $0 != 0x5C }),
              let components = URLComponents(string: value),
              components.scheme == "https",
              let host = components.host,
              !host.isEmpty,
              components.user == nil,
              components.password == nil,
              components.query == nil,
              components.fragment == nil,
              components.path.isEmpty || components.path == "/"
        else {
            throw CoreValidationError.invalidRelayOrigin
        }
        // Check the original authority as well: Foundation accepts an empty port
        // and can otherwise repair malformed input while splitting URL components.
        let authority = value.dropFirst("https://".count).prefix { $0 != "/" }
        guard authority.hasPrefix(host) else { throw CoreValidationError.invalidRelayOrigin }
        let suffix = authority.dropFirst(host.count)
        let port: Int
        if suffix.isEmpty {
            port = 443
        } else {
            let digits = suffix.dropFirst()
            guard suffix.first == ":", !digits.isEmpty,
                  digits.utf8.allSatisfy({ (48 ... 57).contains($0) }),
                  let number = Int(digits), (1 ... 65_535).contains(number)
            else { throw CoreValidationError.invalidRelayOrigin }
            port = number
        }
        let hostname = try canonicalHostname(host)
        let headerHost = hostname.contains(":") ? "[\(hostname)]" : hostname
        return port == 443 ? "https://\(headerHost)" : "https://\(headerHost):\(port)"
    }

    private static func canonicalHostname(_ host: String) throws -> String {
        if host.first == "[", host.last == "]" {
            let literal = String(host.dropFirst().dropLast())
            var address = in6_addr()
            guard literal.withCString({ inet_pton(AF_INET6, $0, &address) }) == 1 else {
                throw CoreValidationError.invalidRelayOrigin
            }
            let bytes = withUnsafeBytes(of: address) { Array($0) }
            if bytes.prefix(10).allSatisfy({ $0 == 0 }), bytes[10] == 0xFF, bytes[11] == 0xFF {
                return bytes.suffix(4).map(String.init).joined(separator: ".")
            }
            var buffer = [CChar](repeating: 0, count: Int(INET6_ADDRSTRLEN))
            return try buffer.withUnsafeMutableBufferPointer { output in
                guard let text = inet_ntop(AF_INET6, &address, output.baseAddress, socklen_t(output.count)) else {
                    throw CoreValidationError.invalidRelayOrigin
                }
                return String(cString: text)
            }
        }
        guard host.utf8.count <= 253,
              host.utf8.allSatisfy({ (65 ... 90).contains($0) || (97 ... 122).contains($0) || (48 ... 57).contains($0) || $0 == 45 || $0 == 46 })
        else {
            throw CoreValidationError.invalidRelayOrigin
        }
        return host.lowercased()
    }
}

enum Expiry {
    static func parseFuture(_ value: String, now: Date) throws -> Date {
        for options in [
            ISO8601DateFormatter.Options.withInternetDateTime,
            [.withInternetDateTime, .withFractionalSeconds],
        ] {
            let formatter = ISO8601DateFormatter()
            formatter.formatOptions = options
            if let date = formatter.date(from: value) {
                guard date > now else { throw CoreValidationError.expired }
                return date
            }
        }
        throw CoreValidationError.invalidJSON
    }
}
