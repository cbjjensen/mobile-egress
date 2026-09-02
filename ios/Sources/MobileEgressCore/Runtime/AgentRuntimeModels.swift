import Foundation

public enum AgentRuntimeConnectionState: String, Equatable, Sendable {
    case stopped
    case connecting
    case connected
    case stopping
}

public enum AgentRuntimeErrorClass: String, Equatable, Sendable {
    case none
    case relayUnavailable
    case relayAuth
    case relayTLS
    case `protocol`
    case targetPolicy
    case targetConnect
    case backpressure
    case `internal`
}

public struct AgentRuntimeSnapshot: Equatable, Sendable {
    public let connectionState: AgentRuntimeConnectionState
    public let activeStreamCount: Int
    public let bytesUploaded: UInt64
    public let bytesDownloaded: UInt64
    public let errorClass: AgentRuntimeErrorClass

    public init(
        connectionState: AgentRuntimeConnectionState,
        activeStreamCount: Int,
        bytesUploaded: UInt64,
        bytesDownloaded: UInt64,
        errorClass: AgentRuntimeErrorClass
    ) {
        self.connectionState = connectionState
        self.activeStreamCount = activeStreamCount
        self.bytesUploaded = bytesUploaded
        self.bytesDownloaded = bytesDownloaded
        self.errorClass = errorClass
    }
}

public enum RelayWebSocketOpcode: Equatable, Sendable {
    case binary
    case text
    case continuation
    case ping
    case pong
    case close
    case unknown
}

public struct RelayWebSocketMessage: Equatable, Sendable {
    public let opcode: RelayWebSocketOpcode
    public let payload: Data
    public let isComplete: Bool

    public init(opcode: RelayWebSocketOpcode, payload: Data, isComplete: Bool) {
        self.opcode = opcode
        self.payload = payload
        self.isComplete = isComplete
    }
}

public enum RelayConnectionFailure: Error, Equatable, Sendable {
    case unavailable
    case authentication
    case tls
}

public enum RelayWebSocketEvent: Equatable, Sendable {
    case connected
    case message(RelayWebSocketMessage)
    case closed
    case failed(RelayConnectionFailure)
}

public typealias RelayWebSocketEventHandler = @Sendable (RelayWebSocketEvent) async -> Void
public typealias RelayWebSocketSendCompletion = @Sendable (Result<Void, RelayConnectionFailure>) async -> Void

public protocol RelayWebSocketIO: Sendable {
    func start(eventHandler: @escaping RelayWebSocketEventHandler)
    func sendBinary(_ data: Data, completion: @escaping RelayWebSocketSendCompletion) -> Bool
    func close(code: UInt16, reason: String)
    func cancel()
}

public enum TargetConnectionFailure: Error, Equatable, Sendable {
    case failed
}

public enum TargetConnectionEvent: Equatable, Sendable {
    case ready
    case data(Data)
    case ended
    case failed
}

public typealias TargetConnectionEventHandler = @Sendable (TargetConnectionEvent) async -> Void
public typealias TargetConnectionSendCompletion = @Sendable (Result<Void, TargetConnectionFailure>) async -> Void

public protocol TargetConnectionIO: Sendable {
    func start(eventHandler: @escaping TargetConnectionEventHandler)
    func send(_ data: Data, completion: @escaping TargetConnectionSendCompletion) -> Bool
    func cancel()
}

public protocol TargetConnectionFactory: Sendable {
    func makeConnection(configuration: TargetConnectionConfiguration) throws -> any TargetConnectionIO
}

public struct TargetConnectionConfiguration: Equatable, Hashable, Sendable {
    public let ipLiteral: String
    public let port: Int
    public let requiredInterfaceType: TransportInterfaceType = .cellular
    public let prohibitedInterfaceTypes: Set<TransportInterfaceType> = [.wifi, .wiredEthernet]
    public let allowsProxyFallback = false
    public let readChunkBytes: Int
    public let inboundQueueCapacity: Int
    public let connectTimeout: TimeInterval

    public init(
        ipLiteral: String,
        port: Int,
        readChunkBytes: Int = 16 * 1_024,
        inboundQueueCapacity: Int = 2,
        connectTimeout: TimeInterval = 30
    ) throws {
        guard readChunkBytes > 0,
              inboundQueueCapacity > 0,
              connectTimeout > 0
        else { throw CoreValidationError.invalidPairing }
        self.ipLiteral = try PublicAddressPolicy.validate(ipLiteral: ipLiteral, port: port)
        self.port = port
        self.readChunkBytes = readChunkBytes
        self.inboundQueueCapacity = inboundQueueCapacity
        self.connectTimeout = connectTimeout
    }
}

public struct RelayWebSocketConfiguration: Equatable, Sendable {
    public let url: URL
    public let hostname: String
    public let port: Int
    public let pinnedCertificateAuthorityDER: Data
    public let localIdentityKeyTag: String
    public let requiredInterfaceType: TransportInterfaceType = .cellular
    public let prohibitedInterfaceTypes: Set<TransportInterfaceType> = [.wifi, .wiredEthernet]
    public let validatesHostname = true
    public let requiresMutualTLS = true
    public let requiresTLS13 = true
    public let automaticallyRepliesToWebSocketPings = true
    public let allowsSystemTrustFallback = false
    public let allowsProxyFallback = false
    public let maximumMessageBytes = WireProtocol.maximumWebSocketMessageBytes

    public init(identity: AgentIdentity) throws {
        guard identity.role == "agent",
              !identity.keyTag.isEmpty,
              !identity.caCertificateDER.isEmpty
        else {
            throw CoreValidationError.invalidPairing
        }
        let endpoint = try RelayEndpoint(origin: identity.relayOrigin)
        guard var components = URLComponents(string: endpoint.origin) else {
            throw CoreValidationError.invalidRelayOrigin
        }
        components.scheme = "wss"
        components.path = "/v1/session"
        guard let url = components.url else { throw CoreValidationError.invalidRelayOrigin }
        self.url = url
        hostname = endpoint.hostname
        port = endpoint.port
        pinnedCertificateAuthorityDER = identity.caCertificateDER
        localIdentityKeyTag = identity.keyTag
    }
}

struct AgentRuntimeLimits: Equatable, Sendable {
    let maximumStreams: Int
    let tombstones: Int
    let outboundControls: Int
    let outboundData: Int
    let outboundDataPerStream: Int
    let targetInbound: Int
    let targetInboundSessionFrames: Int
    let targetInboundSessionBytes: Int
    let targetReadChunkBytes: Int
    let maximumInboundDataBytes: Int

    static let production = AgentRuntimeLimits(
        maximumStreams: 256,
        tombstones: 1_024,
        outboundControls: 512,
        outboundData: 256,
        outboundDataPerStream: 2,
        targetInbound: 8,
        targetInboundSessionFrames: 512,
        targetInboundSessionBytes: 8 * 1_024 * 1_024,
        targetReadChunkBytes: 16 * 1_024,
        maximumInboundDataBytes: 32 * 1_024
    )
}

enum AgentRuntimeEffect: Equatable, Hashable, Sendable {
    case startRelay
    case createTarget(streamID: String, token: UInt64, configuration: TargetConnectionConfiguration)
    case writeTarget(streamID: String, token: UInt64, writeID: UInt64, data: Data)
    case cancelTarget(streamID: String, token: UInt64)
    case closeRelay(code: UInt16, reason: String)
    case cancelRelay
}
