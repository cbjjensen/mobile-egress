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
              Set(object.keys) == expected
        else {
            throw CoreValidationError.invalidJSON
        }
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
