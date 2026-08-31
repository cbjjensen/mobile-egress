import Foundation
import XCTest
@testable import MobileEgressCore

final class ProviderMessagingTests: XCTestCase {
    func testConfigurationAcceptsExpandedSharedIdentifiers() throws {
        let configuration = try MobileEgressSystemConfiguration(
            providerBundleIdentifier: "com.mobileegress.agent.tunnel",
            appGroupIdentifier: "group.com.mobileegress.agent",
            keychainAccessGroup: "generated-prefix.com.mobileegress.agent.shared"
        )

        XCTAssertEqual(configuration.providerBundleIdentifier, "com.mobileegress.agent.tunnel")
        XCTAssertEqual(configuration.appGroupIdentifier, "group.com.mobileegress.agent")
        XCTAssertEqual(configuration.keychainAccessGroup, "generated-prefix.com.mobileegress.agent.shared")
    }

    func testConfigurationRejectsMissingUnexpandedAndMalformedIdentifiers() {
        assertConfigurationError(.missingProviderBundleIdentifier) {
            try MobileEgressSystemConfiguration(
                providerBundleIdentifier: " ",
                appGroupIdentifier: "group.com.mobileegress.agent",
                keychainAccessGroup: "generated-prefix.com.mobileegress.agent.shared"
            )
        }
        assertConfigurationError(.unresolvedBuildSetting) {
            try MobileEgressSystemConfiguration(
                providerBundleIdentifier: "$(MOBILE_EGRESS_PROVIDER_BUNDLE_IDENTIFIER)",
                appGroupIdentifier: "group.com.mobileegress.agent",
                keychainAccessGroup: "$(AppIdentifierPrefix)com.mobileegress.agent.shared"
            )
        }
        assertConfigurationError(.invalidAppGroupIdentifier) {
            try MobileEgressSystemConfiguration(
                providerBundleIdentifier: "com.mobileegress.agent.tunnel",
                appGroupIdentifier: "com.mobileegress.agent",
                keychainAccessGroup: "generated-prefix.com.mobileegress.agent.shared"
            )
        }
        assertConfigurationError(.invalidKeychainAccessGroup) {
            try MobileEgressSystemConfiguration(
                providerBundleIdentifier: "com.mobileegress.agent.tunnel",
                appGroupIdentifier: "group.com.mobileegress.agent",
                keychainAccessGroup: "shared secret"
            )
        }
    }

    func testStatusRequestUsesAnExactBoundedFiniteMessage() throws {
        let request = try TunnelProviderMessageCodec.statusRequest()
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: request) as? [String: Any])

        XCTAssertEqual(Set(object.keys), ["type", "version"])
        XCTAssertEqual(object["type"] as? String, "status")
        XCTAssertEqual(object["version"] as? Int, 1)
        XCTAssertNoThrow(try TunnelProviderMessageCodec.decodeStatusRequest(request))

        for malformed in [
            #"{"version":2,"type":"status"}"#,
            #"{"version":1,"type":"start"}"#,
            #"{"version":1,"type":"status","secret":"do-not-accept"}"#,
            #"{"version":1,"version":1,"type":"status"}"#,
        ] {
            XCTAssertThrowsError(try TunnelProviderMessageCodec.decodeStatusRequest(Data(malformed.utf8))) {
                XCTAssertEqual($0 as? TunnelProviderMessageError, .invalidMessage)
            }
        }

        let oversized = Data(repeating: 0x41, count: TunnelProviderMessageCodec.maximumMessageBytes + 1)
        XCTAssertThrowsError(try TunnelProviderMessageCodec.decodeStatusRequest(oversized)) {
            XCTAssertEqual($0 as? TunnelProviderMessageError, .messageTooLarge)
        }
    }

    func testStatusRoundTripExposesOnlyFiniteSnapshotFields() throws {
        let status = TunnelProviderStatus(
            providerState: .running,
            runtimeSnapshot: AgentRuntimeSnapshot(
                connectionState: .connected,
                activeStreamCount: 7,
                bytesUploaded: 4_294_967_296,
                bytesDownloaded: 9_876_543_210,
                errorClass: .targetPolicy
            ),
            providerError: .none
        )

        let encoded = try TunnelProviderMessageCodec.encodeStatus(status)
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: encoded) as? [String: Any])

        XCTAssertEqual(Set(object.keys), [
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
        XCTAssertEqual(try TunnelProviderMessageCodec.decodeStatus(encoded), status)
        XCTAssertLessThanOrEqual(encoded.count, TunnelProviderMessageCodec.maximumMessageBytes)
    }

    func testStatusDecoderRejectsUnknownExtraDuplicateAndInvalidMetricValues() {
        let valid = #"{"version":1,"type":"status","providerState":"running","connectionState":"connected","activeStreamCount":0,"bytesUploaded":0,"bytesDownloaded":0,"providerError":"none","runtimeError":"none"}"#
        let withExtraField = String(valid.dropLast()) + #", "relayUrl":"https://secret.example"}"#
        let malformed = [
            valid.replacingOccurrences(of: #""providerState":"running""#, with: #""providerState":"unknown""#),
            valid.replacingOccurrences(of: #""runtimeError":"none""#, with: #""runtimeError":"certificatePem""#),
            valid.replacingOccurrences(of: #""activeStreamCount":0"#, with: #""activeStreamCount":-1"#),
            withExtraField,
            #"{"version":1,"version":1,"type":"status","providerState":"running","connectionState":"connected","activeStreamCount":0,"bytesUploaded":0,"bytesDownloaded":0,"providerError":"none","runtimeError":"none"}"#,
        ]

        for payload in malformed {
            XCTAssertThrowsError(try TunnelProviderMessageCodec.decodeStatus(Data(payload.utf8))) {
                XCTAssertEqual($0 as? TunnelProviderMessageError, .invalidMessage)
            }
        }
    }

    func testProviderDisconnectErrorsMapOnlyFiniteDomainAndCodes() {
        let expected: [(TunnelProviderErrorClass, Int)] = [
            (.identityUnavailable, 1),
            (.invalidConfiguration, 2),
            (.tunnelSettings, 3),
            (.runtimeUnavailable, 4),
            (.invalidMessage, 5),
        ]

        XCTAssertEqual(
            TunnelProviderErrorClass.providerErrorDomain,
            "com.mobileegress.agent.tunnel"
        )
        for (error, code) in expected {
            XCTAssertEqual(error.providerErrorCode, code)
            XCTAssertEqual(
                TunnelProviderErrorClass.classifyDisconnectError(
                    domain: TunnelProviderErrorClass.providerErrorDomain,
                    code: code
                ),
                error
            )
        }
        XCTAssertEqual(
            TunnelProviderErrorClass.classifyDisconnectError(
                domain: "NEVPNConnectionErrorDomain",
                code: 17
            ),
            .runtimeUnavailable
        )
        XCTAssertEqual(
            TunnelProviderErrorClass.classifyDisconnectError(
                domain: TunnelProviderErrorClass.providerErrorDomain,
                code: 999
            ),
            .runtimeUnavailable
        )
    }

    private func assertConfigurationError(
        _ expected: MobileEgressConfigurationError,
        operation: () throws -> MobileEgressSystemConfiguration,
        file: StaticString = #filePath,
        line: UInt = #line
    ) {
        XCTAssertThrowsError(try operation(), file: file, line: line) {
            XCTAssertEqual($0 as? MobileEgressConfigurationError, expected, file: file, line: line)
        }
    }
}
