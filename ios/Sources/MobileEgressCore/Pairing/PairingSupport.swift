import Foundation

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
        let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let components = URLComponents(string: trimmed),
              components.scheme == "https",
              let host = components.host,
              !host.isEmpty,
              components.user == nil,
              components.password == nil,
              components.query == nil,
              components.fragment == nil,
              components.path.isEmpty || components.path == "/",
              components.port != 0
        else {
            throw CoreValidationError.invalidRelayOrigin
        }

        var origin = URLComponents()
        origin.scheme = "https"
        origin.host = host
        origin.port = components.port
        guard let normalized = origin.string else {
            throw CoreValidationError.invalidRelayOrigin
        }
        return normalized
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
