import Foundation

public struct DirectEndpointUpdate: Decodable, Equatable, Sendable {
    public let clientId: String
    public let pairingId: String
    public let generation: Int64
    public let endpoint: String
    @ClientTransportValue public var transport: ClientTransport = .direct

    public static func parse(_ bundle: String, for client: DirectClientRecord, verify: (Data, Data) throws -> Bool) throws -> Self {
        struct Wrapper: Decodable { let version: Int; let type: String; let payload: String; let signature: String }
        let bytes = try DirectBundle.decode(bundle)
        try StrictJSONObject.exactKeys(in: bytes, expected: ["version", "type", "payload", "signature"])
        let wrapper = try JSONDecoder().decode(Wrapper.self, from: bytes)
        guard StrictJSONObject.hasIntegerLiteral(2, forKey: "version", in: bytes), wrapper.type == "mobile-egress-direct-endpoint-update" else { throw DirectAgentError.invalidBundle }
        let payload = try DirectBundle.decode(wrapper.payload)
        guard try verify(Data("MobileEgress-Direct-Endpoint-v2\n".utf8) + payload, DirectBundle.decode(wrapper.signature)) else { throw DirectAgentError.trust }
        try StrictJSONObject.exactKeys(in: payload, expected: ["clientId", "pairingId", "generation", "endpoint"], optional: ["transport"])
        let update = try JSONDecoder().decode(Self.self, from: payload)
        guard let generation = StrictJSONObject.integerLiteral(forKey: "generation", in: payload), generation > 0, Int64(generation) == update.generation else { throw DirectAgentError.invalidBundle }
        guard update.clientId == client.clientID, update.pairingId == client.pairingID else { throw DirectAgentError.identityMismatch }
        let endpoint = try RelayOrigin.parse(update.endpoint)
        guard update.generation >= client.generation else { throw DirectAgentError.staleUpdate }
        if update.generation == client.generation {
            guard endpoint == (try RelayOrigin.parse(client.endpoint)), update.transport == client.transport else { throw DirectAgentError.staleUpdate }
        }
        return Self(clientId: update.clientId, pairingId: update.pairingId, generation: update.generation, endpoint: endpoint, transport: update.transport)
    }
}
