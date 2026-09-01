import Foundation
import XCTest
@testable import MobileEgressCore

final class RelayRuntimeConfigurationTests: XCTestCase {
    func testProductionAgentLimitsMatchSharedCapacityContract() {
        XCTAssertEqual(
            AgentRuntimeLimits.production,
            AgentRuntimeLimits(
                maximumStreams: 256,
                tombstones: 1_024,
                outboundControls: 512,
                outboundData: 256,
                outboundDataPerStream: 2,
                targetInbound: 2,
                targetReadChunkBytes: 16 * 1_024,
                maximumInboundDataBytes: 32 * 1_024
            )
        )
    }

    func testRelayConfigurationDerivesExactPinnedMTLSCellularSessionEndpoint() throws {
        let configuration = try RelayWebSocketConfiguration(identity: Task2Fixtures.identity())

        XCTAssertEqual(configuration.url.absoluteString, "wss://relay.example:8443/v1/session")
        XCTAssertEqual(configuration.hostname, "relay.example")
        XCTAssertEqual(configuration.port, 8443)
        XCTAssertEqual(configuration.pinnedCertificateAuthorityDER, Task2Fixtures.caDER)
        XCTAssertEqual(configuration.localIdentityKeyTag, "mobile-egress.agent.key.new")
        XCTAssertEqual(configuration.requiredInterfaceType, .cellular)
        XCTAssertEqual(configuration.prohibitedInterfaceTypes, [.wifi, .wiredEthernet])
        XCTAssertTrue(configuration.validatesHostname)
        XCTAssertTrue(configuration.requiresMutualTLS)
        XCTAssertTrue(configuration.requiresTLS13)
        XCTAssertTrue(configuration.automaticallyRepliesToWebSocketPings)
        XCTAssertFalse(configuration.allowsSystemTrustFallback)
        XCTAssertFalse(configuration.allowsProxyFallback)
        XCTAssertEqual(configuration.maximumMessageBytes, WireProtocol.maximumWebSocketMessageBytes)
    }

    func testRelayConfigurationRejectsNonAgentOrUnpinnedIdentity() {
        let valid = Task2Fixtures.identity()
        let client = AgentIdentity(
            relayOrigin: valid.relayOrigin,
            role: "client",
            serial: valid.serial,
            keyTag: valid.keyTag,
            certificatePEM: valid.certificatePEM,
            caCertificatePEM: valid.caCertificatePEM,
            caCertificateDER: valid.caCertificateDER
        )
        let unpinned = AgentIdentity(
            relayOrigin: valid.relayOrigin,
            role: valid.role,
            serial: valid.serial,
            keyTag: valid.keyTag,
            certificatePEM: valid.certificatePEM,
            caCertificatePEM: valid.caCertificatePEM,
            caCertificateDER: Data()
        )

        XCTAssertThrowsError(try RelayWebSocketConfiguration(identity: client))
        XCTAssertThrowsError(try RelayWebSocketConfiguration(identity: unpinned))
    }

    func testTargetConfigurationAcceptsOnlyPublicLiteralsAndCarriesBoundedCellularPolicy() throws {
        let configuration = try TargetConnectionConfiguration(ipLiteral: "8.8.8.8", port: 443)

        XCTAssertEqual(configuration.ipLiteral, "8.8.8.8")
        XCTAssertEqual(configuration.port, 443)
        XCTAssertEqual(configuration.requiredInterfaceType, .cellular)
        XCTAssertEqual(configuration.prohibitedInterfaceTypes, [.wifi, .wiredEthernet])
        XCTAssertFalse(configuration.allowsProxyFallback)
        XCTAssertEqual(configuration.readChunkBytes, 16 * 1024)
        XCTAssertEqual(configuration.inboundQueueCapacity, 2)
        XCTAssertEqual(configuration.connectTimeout, 30)

        XCTAssertThrowsError(try TargetConnectionConfiguration(ipLiteral: "example.com", port: 443))
        XCTAssertThrowsError(try TargetConnectionConfiguration(ipLiteral: "10.0.0.1", port: 443))
        XCTAssertThrowsError(try TargetConnectionConfiguration(ipLiteral: "8.8.8.8", port: 0))
    }
}
