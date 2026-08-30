import Foundation

public enum WireMessageType: String, Codable, CaseIterable, Sendable {
    case open, opened, rejected, data, close, ping, pong
}

public struct WireEnvelope: Equatable {
    public let version: Int
    public let type: WireMessageType
    public let streamID: String
    private let payload: String

    init(version: Int, type: WireMessageType, streamID: String, payload: String) {
        self.version = version
        self.type = type
        self.streamID = streamID
        self.payload = payload
    }

    public func decodedPayload() throws -> Data {
        try WireProtocol.decodePayload(payload)
    }
}

public enum WireProtocol {
    public static let maximumWebSocketMessageBytes = 2 * 1024 * 1024
    public static let maximumPayloadBytes = 1024 * 1024

    private static let agentInboundTypes: Set<WireMessageType> = [.open, .data, .close, .ping, .pong]
    private static let errorCodes: Set<String> = [
        "agent_stream_limit", "agent_unavailable", "client_closed", "client_stream_limit", "dns_failure",
        "idle_timeout", "invalid_target", "opening_timeout", "policy_denied", "protocol_error", "revoked",
        "session_closed", "stream_in_use", "stream_not_found", "target_closed", "target_failure",
    ]

    public static func encode(type: WireMessageType, streamID: String = "", payload: Data = Data()) throws -> Data {
        guard payload.count <= maximumPayloadBytes else { throw CoreValidationError.invalidJSON }
        let envelope = WireEnvelopeWire(
            version: 1,
            type: type,
            streamID: streamID,
            payload: payload.base64EncodedString()
                .replacingOccurrences(of: "+", with: "-")
                .replacingOccurrences(of: "/", with: "_")
                .replacingOccurrences(of: "=", with: "")
        )
        let encoded = try JSONEncoder().encode(envelope)
        try validate(version: envelope.version, type: envelope.type, streamID: envelope.streamID, payload: envelope.payload)
        guard encoded.count <= maximumWebSocketMessageBytes else { throw CoreValidationError.invalidJSON }
        return encoded
    }

    public static func parseAgentInbound(_ raw: Data) throws -> WireEnvelope {
        guard raw.count <= maximumWebSocketMessageBytes else { throw CoreValidationError.invalidJSON }
        try StrictJSONObject.exactKeys(in: raw, expected: ["version", "type", "streamId", "payload"])
        let wire = try JSONDecoder().decode(WireEnvelopeWire.self, from: raw)
        try validate(version: wire.version, type: wire.type, streamID: wire.streamID, payload: wire.payload)
        guard agentInboundTypes.contains(wire.type) else { throw CoreValidationError.invalidJSON }
        return WireEnvelope(version: wire.version, type: wire.type, streamID: wire.streamID, payload: wire.payload)
    }

    public static func finiteErrorCode(_ value: String) throws -> Data {
        guard errorCodes.contains(value) else { throw CoreValidationError.invalidJSON }
        return Data(value.utf8)
    }

    static func decodePayload(_ value: String) throws -> Data {
        guard value.allSatisfy({ $0.isASCII && ($0.isLetter || $0.isNumber || $0 == "-" || $0 == "_") }),
              value.utf8.count % 4 != 1,
              (value.utf8.count * 3) / 4 <= maximumPayloadBytes
        else {
            throw CoreValidationError.invalidBase64URL
        }
        let standard = value.replacingOccurrences(of: "-", with: "+").replacingOccurrences(of: "_", with: "/")
        let padding = String(repeating: "=", count: (4 - standard.utf8.count % 4) % 4)
        guard let payload = Data(base64Encoded: standard + padding), payload.count <= maximumPayloadBytes else {
            throw CoreValidationError.invalidBase64URL
        }
        return payload
    }

    private static func validate(version: Int, type: WireMessageType, streamID: String, payload: String) throws {
        guard version == 1 else { throw CoreValidationError.invalidJSON }
        let isKeepAlive = type == .ping || type == .pong
        guard (isKeepAlive && streamID.isEmpty) || (!isKeepAlive && isValidStreamID(streamID)) else {
            throw CoreValidationError.invalidJSON
        }
        _ = try decodePayload(payload)
    }

    private static func isValidStreamID(_ value: String) -> Bool {
        guard !value.isEmpty, value.utf8.count <= 128 else { return false }
        return value.allSatisfy { $0.isASCII && ($0.isLetter || $0.isNumber || $0 == "-" || $0 == "_") }
    }
}

private struct WireEnvelopeWire: Codable {
    let version: Int
    let type: WireMessageType
    let streamID: String
    let payload: String

    enum CodingKeys: String, CodingKey {
        case version, type, streamID = "streamId", payload
    }
}
