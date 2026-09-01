import Foundation

public enum TunnelProviderLifecycleState: String, Equatable, Sendable {
    case stopped
    case starting
    case running
    case stopping
    case failed
}

public enum TunnelProviderErrorClass: String, Equatable, Sendable {
    case none
    case identityUnavailable
    case invalidConfiguration
    case tunnelSettings
    case runtimeUnavailable
    case invalidMessage
    case relayUnavailable
    case relayAuth
    case relayTLS
    case `protocol`
    case targetPolicy
    case targetConnect
    case backpressure
    case `internal`

    public static let providerErrorDomain = "com.mobileegress.agent.tunnel"

    public var providerErrorCode: Int {
        switch self {
        case .none: 0
        case .identityUnavailable: 1
        case .invalidConfiguration: 2
        case .tunnelSettings: 3
        case .runtimeUnavailable: 4
        case .invalidMessage: 5
        case .relayUnavailable: 6
        case .relayAuth: 7
        case .relayTLS: 8
        case .protocol: 9
        case .targetPolicy: 10
        case .targetConnect: 11
        case .backpressure: 12
        case .internal: 13
        }
    }

    public init(runtimeFailure: AgentRuntimeErrorClass) {
        switch runtimeFailure {
        case .none: self = .none
        case .relayUnavailable: self = .relayUnavailable
        case .relayAuth: self = .relayAuth
        case .relayTLS: self = .relayTLS
        case .protocol: self = .protocol
        case .targetPolicy: self = .targetPolicy
        case .targetConnect: self = .targetConnect
        case .backpressure: self = .backpressure
        case .internal: self = .internal
        }
    }

    public var userMessage: String? {
        switch self {
        case .none: nil
        case .identityUnavailable: "Enrollment is required."
        case .invalidConfiguration: "Tunnel configuration is invalid."
        case .tunnelSettings: "iOS rejected the tunnel settings."
        case .runtimeUnavailable: "Agent runtime is unavailable."
        case .invalidMessage: "Tunnel status response was invalid."
        case .relayUnavailable: "Relay is unavailable."
        case .relayAuth: "Relay authentication failed."
        case .relayTLS: "Relay trust validation failed."
        case .protocol: "Relay protocol error."
        case .targetPolicy: "A target was blocked by policy."
        case .targetConnect: "A target connection failed."
        case .backpressure: "Agent capacity was exceeded."
        case .internal: "Agent runtime failed."
        }
    }

    public static func classifyDisconnectError(domain: String, code: Int) -> Self {
        guard domain == providerErrorDomain else { return .runtimeUnavailable }
        switch code {
        case 1: return .identityUnavailable
        case 2: return .invalidConfiguration
        case 3: return .tunnelSettings
        case 4: return .runtimeUnavailable
        case 5: return .invalidMessage
        case 6: return .relayUnavailable
        case 7: return .relayAuth
        case 8: return .relayTLS
        case 9: return .protocol
        case 10: return .targetPolicy
        case 11: return .targetConnect
        case 12: return .backpressure
        case 13: return .internal
        default: return .runtimeUnavailable
        }
    }
}

public struct TunnelProviderStatus: Equatable, Sendable {
    public let providerState: TunnelProviderLifecycleState
    public let runtimeSnapshot: AgentRuntimeSnapshot
    public let providerError: TunnelProviderErrorClass

    public init(
        providerState: TunnelProviderLifecycleState,
        runtimeSnapshot: AgentRuntimeSnapshot,
        providerError: TunnelProviderErrorClass
    ) {
        self.providerState = providerState
        self.runtimeSnapshot = runtimeSnapshot
        self.providerError = providerError
    }
}

public enum TunnelProviderMessageError: Error, Equatable, Sendable {
    case messageTooLarge
    case invalidMessage
}

public enum TunnelProviderMessageCodec {
    public static let maximumMessageBytes = 4 * 1024

    public static func statusRequest() throws -> Data {
        try boundedEncode(StatusRequestWire(version: 1, type: "status"))
    }

    public static func decodeStatusRequest(_ data: Data) throws {
        try requireBounded(data)
        do {
            try StrictJSONObject.exactKeys(in: data, expected: ["version", "type"])
            guard StrictJSONObject.hasIntegerLiteral(1, forKey: "version", in: data) else {
                throw TunnelProviderMessageError.invalidMessage
            }
            let wire = try JSONDecoder().decode(StatusRequestWire.self, from: data)
            guard wire.version == 1, wire.type == "status" else {
                throw TunnelProviderMessageError.invalidMessage
            }
        } catch let error as TunnelProviderMessageError {
            throw error
        } catch {
            throw TunnelProviderMessageError.invalidMessage
        }
    }

    public static func encodeStatus(_ status: TunnelProviderStatus) throws -> Data {
        let snapshot = status.runtimeSnapshot
        guard (0 ... AgentRuntimeLimits.production.maximumStreams).contains(snapshot.activeStreamCount) else {
            throw TunnelProviderMessageError.invalidMessage
        }
        return try boundedEncode(StatusResponseWire(
            version: 1,
            type: "status",
            providerState: status.providerState.rawValue,
            connectionState: snapshot.connectionState.rawValue,
            activeStreamCount: snapshot.activeStreamCount,
            bytesUploaded: snapshot.bytesUploaded,
            bytesDownloaded: snapshot.bytesDownloaded,
            providerError: status.providerError.rawValue,
            runtimeError: snapshot.errorClass.rawValue
        ))
    }

    public static func decodeStatus(_ data: Data) throws -> TunnelProviderStatus {
        try requireBounded(data)
        do {
            try StrictJSONObject.exactKeys(in: data, expected: [
                "activeStreamCount",
                "bytesDownloaded",
                "bytesUploaded",
                "connectionState",
                "providerError",
                "providerState",
                "runtimeError",
                "type",
                "version",
            ])
            guard StrictJSONObject.hasIntegerLiteral(1, forKey: "version", in: data) else {
                throw TunnelProviderMessageError.invalidMessage
            }
            let wire = try JSONDecoder().decode(StatusResponseWire.self, from: data)
            guard wire.version == 1,
                  wire.type == "status",
                  (0 ... AgentRuntimeLimits.production.maximumStreams).contains(wire.activeStreamCount),
                  let providerState = TunnelProviderLifecycleState(rawValue: wire.providerState),
                  let connectionState = AgentRuntimeConnectionState(rawValue: wire.connectionState),
                  let providerError = TunnelProviderErrorClass(rawValue: wire.providerError),
                  let runtimeError = AgentRuntimeErrorClass(rawValue: wire.runtimeError)
            else {
                throw TunnelProviderMessageError.invalidMessage
            }
            return TunnelProviderStatus(
                providerState: providerState,
                runtimeSnapshot: AgentRuntimeSnapshot(
                    connectionState: connectionState,
                    activeStreamCount: wire.activeStreamCount,
                    bytesUploaded: wire.bytesUploaded,
                    bytesDownloaded: wire.bytesDownloaded,
                    errorClass: runtimeError
                ),
                providerError: providerError
            )
        } catch let error as TunnelProviderMessageError {
            throw error
        } catch {
            throw TunnelProviderMessageError.invalidMessage
        }
    }

    private static func requireBounded(_ data: Data) throws {
        guard !data.isEmpty, data.count <= maximumMessageBytes else {
            throw TunnelProviderMessageError.messageTooLarge
        }
    }

    private static func boundedEncode<Value: Encodable>(_ value: Value) throws -> Data {
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.sortedKeys]
        let data = try encoder.encode(value)
        guard data.count <= maximumMessageBytes else {
            throw TunnelProviderMessageError.messageTooLarge
        }
        return data
    }
}

private struct StatusRequestWire: Codable {
    let version: Int
    let type: String
}

private struct StatusResponseWire: Codable {
    let version: Int
    let type: String
    let providerState: String
    let connectionState: String
    let activeStreamCount: Int
    let bytesUploaded: UInt64
    let bytesDownloaded: UInt64
    let providerError: String
    let runtimeError: String
}
