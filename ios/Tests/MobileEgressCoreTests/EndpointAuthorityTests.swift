import Foundation
import XCTest
@testable import MobileEgressCore

final class EndpointAuthorityTests: XCTestCase {
    private let authorities: [(input: String, origin: String, hostname: String, port: Int, hostHeader: String)] = [
        ("https://CLIENT.Example:08443", "https://client.example:8443", "client.example", 8443, "client.example:8443"),
        ("https://client.example:00443", "https://client.example", "client.example", 443, "client.example"),
        ("https://192.0.2.1", "https://192.0.2.1", "192.0.2.1", 443, "192.0.2.1"),
        ("https://192.0.2.1:08443", "https://192.0.2.1:8443", "192.0.2.1", 8443, "192.0.2.1:8443"),
        ("https://[2001:db8::1]", "https://[2001:db8::1]", "2001:db8::1", 443, "[2001:db8::1]"),
        ("https://[2001:0DB8:0:0:0:0:0:1]:00443", "https://[2001:db8::1]", "2001:db8::1", 443, "[2001:db8::1]"),
        ("https://[2001:0db8:0000:0000:0000:0000:0000:0001]:08443", "https://[2001:db8::1]:8443", "2001:db8::1", 8443, "[2001:db8::1]:8443"),
    ]

    func testCanonicalAuthoritiesReachHTTPHostAndPinnedSocketHostname() throws {
        let authority = try CertificateAuthorityValidator().validate(TestFixtures.validCAPEM, at: TestFixtures.now)
        for value in authorities {
            let configuration = try PinnedCellularTransportConfiguration(
                relayOrigin: value.input, pinnedCertificateAuthorityDER: authority.der, identity: nil
            )
            let request = HTTPRequest(relayOrigin: value.input, path: "/v2/direct/config", body: Data())
            let encoded = try XCTUnwrap(String(data: HTTP1Codec.encodeRequest(request), encoding: .utf8))
            XCTAssertEqual(configuration.relayOrigin, value.origin, value.input)
            XCTAssertEqual(configuration.hostname, value.hostname, value.input)
            XCTAssertEqual(configuration.port, value.port, value.input)
            XCTAssertTrue(configuration.validatesHostname)
            XCTAssertFalse(configuration.allowsSystemTrustFallback)
            XCTAssertEqual(encoded.components(separatedBy: "\r\n").filter { $0.hasPrefix("Host:") }, ["Host: \(value.hostHeader)"], value.input)
            #if canImport(Network) && canImport(Security)
            let policy = try ApplePinnedTransportParameterBuilder(identityResolver: nil, timeout: 30).makeTrustPolicy(configuration: configuration)
            XCTAssertEqual(policy.hostname, value.hostname, value.input)
            XCTAssertTrue(policy.validatesHostname)
            XCTAssertFalse(policy.allowsSystemTrustFallback)
            #endif
        }
    }

    func testSignedEquivalentAuthoritiesCanonicalizeOnlyAfterOriginalPayloadVerification() throws {
        for value in authorities {
            var client = record(endpoint: value.origin, generation: 3)
            let payload = try updatePayload(endpoint: value.input)
            let bundle = try updateBundle(payload)
            let verify: (Data, Data) -> Bool = { signed, signature in
                signed == Data("MobileEgress-Direct-Endpoint-v2\n".utf8) + payload && signature == Data([1])
            }
            let repeated = try DirectEndpointUpdate.parse(bundle, for: client, verify: verify)
            XCTAssertEqual(repeated.endpoint, value.origin, value.input)
            XCTAssertEqual(repeated.generation, 3)
            client.generation = 1
            XCTAssertEqual(try DirectEndpointUpdate.parse(bundle, for: client, verify: verify).endpoint, value.origin)
            client.generation = 3
            client.endpoint = value.input
            XCTAssertEqual(try DirectEndpointUpdate.parse(bundle, for: client, verify: verify).endpoint, value.origin)
            XCTAssertThrowsError(try DirectEndpointUpdate.parse(bundle, for: client) { _, _ in false }) {
                XCTAssertEqual($0 as? DirectAgentError, .trust)
            }
        }
    }

    func testEquivalentEndpointDoesNotBypassGenerationIdentityOrAuthorityBinding() throws {
        let payload = try updatePayload(endpoint: "https://CLIENT.Example:08443")
        let bundle = try updateBundle(payload)
        for endpoint in ["https://other.example:8443", "https://client.example:9443"] {
            XCTAssertThrowsError(try DirectEndpointUpdate.parse(bundle, for: record(endpoint: endpoint, generation: 3)) { _, _ in true }) {
                XCTAssertEqual($0 as? DirectAgentError, .staleUpdate)
            }
        }
        var client = record(endpoint: "https://client.example:8443", generation: 4)
        XCTAssertThrowsError(try DirectEndpointUpdate.parse(bundle, for: client) { _, _ in true }) {
            XCTAssertEqual($0 as? DirectAgentError, .staleUpdate)
        }
        client.generation = 2
        client.pairingID = UUID().uuidString
        XCTAssertThrowsError(try DirectEndpointUpdate.parse(bundle, for: client) { _, _ in true }) {
            XCTAssertEqual($0 as? DirectAgentError, .identityMismatch)
        }
    }

    func testMalformedOriginsCannotBecomeTrustedEndpointUpdatesOrTransportAuthorities() throws {
        let invalid = [
            "http://client.example", "https://user@client.example", "https://client.example/path",
            "https://client.example?query", "https://client.example#fragment", "https://client.example:0",
            "https://client.example:65536", "https://client.example:", "https://client.example:-1",
            " https://client.example", "https://client.example\n", "https://client.example%0d%0aInjected",
            "https://[2001:db8::1", "https://[[2001:db8::1]]", "https://[2001:db8::xyz]",
            "https://[example.com]", "https://[fe80::1%25en0]",
        ]
        for endpoint in invalid {
            XCTAssertThrowsError(try PinnedCellularTransportConfiguration(relayOrigin: endpoint, pinnedCertificateAuthorityDER: Task2Fixtures.caDER, identity: nil), endpoint)
            XCTAssertThrowsError(try HTTP1Codec.encodeRequest(HTTPRequest(relayOrigin: endpoint, path: "/v2/direct/config", body: Data())), endpoint)
            XCTAssertThrowsError(try DirectEndpointUpdate.parse(updateBundle(updatePayload(endpoint: endpoint)), for: record(endpoint: "https://client.example", generation: 1)) { _, _ in true }, endpoint)
        }
    }

    private func record(endpoint: String, generation: Int64) -> DirectClientRecord {
        var client = DirectClientRecord(clientID: "00c747f8-f6f1-4d52-9acf-3ccdc0a9bb42", displayName: "Workload", endpoint: endpoint)
        client.pairingID = "1b2b77c7-2e21-4cf3-9c23-42aab64bd69b"
        client.generation = generation
        return client
    }

    private func updatePayload(endpoint: String) throws -> Data {
        try JSONSerialization.data(withJSONObject: ["clientId": "00c747f8-f6f1-4d52-9acf-3ccdc0a9bb42", "pairingId": "1b2b77c7-2e21-4cf3-9c23-42aab64bd69b", "generation": 3, "endpoint": endpoint])
    }

    private func updateBundle(_ payload: Data) throws -> String {
        DirectBundle.encode(try JSONSerialization.data(withJSONObject: ["version": 2, "type": "mobile-egress-direct-endpoint-update", "payload": DirectBundle.encode(payload), "signature": DirectBundle.encode(Data([1]))]))
    }
}
