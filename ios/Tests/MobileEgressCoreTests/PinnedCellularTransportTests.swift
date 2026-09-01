import XCTest
@testable import MobileEgressCore

final class PinnedCellularTransportTests: XCTestCase {
    func testEnrollmentTransportRequiresCellularAndPinnedHostnameTrustWithoutFallback() throws {
        let configuration = try PinnedCellularTransportConfiguration(
            relayOrigin: "https://relay.example:8443",
            pinnedCertificateAuthorityDER: Task2Fixtures.caDER,
            identity: nil
        )

        XCTAssertEqual(configuration.hostname, "relay.example")
        XCTAssertEqual(configuration.port, 8443)
        XCTAssertEqual(configuration.requiredInterfaceType, .cellular)
        XCTAssertEqual(configuration.prohibitedInterfaceTypes, [.wifi, .wiredEthernet])
        XCTAssertTrue(configuration.validatesHostname)
        XCTAssertFalse(configuration.allowsSystemTrustFallback)
        XCTAssertNil(configuration.localIdentityKeyTag)
    }

    func testMigrationTransportInjectsStoredIdentityWithoutChangingPinPolicy() throws {
        let identity = Task2Fixtures.identity()
        let configuration = try PinnedCellularTransportConfiguration(
            relayOrigin: "https://new-relay.example:9443",
            pinnedCertificateAuthorityDER: Task2Fixtures.caDER,
            identity: identity
        )

        XCTAssertEqual(configuration.requiredInterfaceType, .cellular)
        XCTAssertEqual(configuration.prohibitedInterfaceTypes, [.wifi, .wiredEthernet])
        XCTAssertEqual(configuration.pinnedCertificateAuthorityDER, Task2Fixtures.caDER)
        XCTAssertEqual(configuration.localIdentityKeyTag, identity.keyTag)
        XCTAssertFalse(configuration.allowsSystemTrustFallback)
    }

    func testTransportRejectsOutOfRangeRelayPorts() {
        XCTAssertThrowsError(try PinnedCellularTransportConfiguration(
            relayOrigin: "https://relay.example:70000",
            pinnedCertificateAuthorityDER: Task2Fixtures.caDER,
            identity: nil
        ))
    }
}
