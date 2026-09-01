import XCTest
@testable import MobileEgressCore

final class PublicAddressPolicyTests: XCTestCase {
    func testPublicAddressPolicyAcceptsPublicLiterals() throws {
        XCTAssertEqual(try PublicAddressPolicy.validate(ipLiteral: "1.1.1.1", port: 443), "1.1.1.1")
        XCTAssertEqual(try PublicAddressPolicy.validate(ipLiteral: "2606:4700:4700::1111", port: 443), "2606:4700:4700::1111")
    }

    func testPublicAddressPolicyRejectsPrivateReservedAndNonLiteralAddresses() throws {
        for address in ["10.0.0.1", "100.64.0.1", "192.168.1.1", "::1", "2001::1", "2001:2::1", "2001:db8::1", "3fff::1", "fc00::1", "example.com"] {
            XCTAssertThrowsError(try PublicAddressPolicy.validate(ipLiteral: address, port: 443))
        }
        XCTAssertThrowsError(try PublicAddressPolicy.validate(ipLiteral: "1.1.1.1", port: 0))
    }
}
