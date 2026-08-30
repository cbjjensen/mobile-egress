#if canImport(Network) && canImport(Security)
import Foundation
import Network
import Security
import XCTest
@testable import MobileEgressCore

final class AppleProductionAdaptersTests: XCTestCase {
    func testSecurityCertificateValidatorRejectsMissingProductionChain() {
        let validator = SecurityEnrollmentCertificateValidator()

        XCTAssertThrowsError(try validator.validate(
            certificateChainDER: [],
            pinnedAuthorityDER: Data([0x01]),
            expectedPublicKeySPKIDER: Data([0x02]),
            at: Date()
        ))
    }

    func testProductionParameterBuilderRequiresCellularAndProhibitsWifiAndWired() throws {
        let authority = try CertificateAuthorityValidator().validate(TestFixtures.validCAPEM, at: TestFixtures.now)
        let configuration = try PinnedCellularTransportConfiguration(
            relayOrigin: "https://relay.example:8443",
            pinnedCertificateAuthorityDER: authority.der,
            identity: nil
        )
        let builder = ApplePinnedTransportParameterBuilder(identityResolver: nil, timeout: 30)

        let trustPolicy = try builder.makeTrustPolicy(configuration: configuration)
        let parameters = try builder.makeParameters(configuration: configuration)

        XCTAssertEqual(trustPolicy.hostname, "relay.example")
        XCTAssertEqual(trustPolicy.authorityDER, authority.der)
        XCTAssertTrue(trustPolicy.validatesHostname)
        XCTAssertFalse(trustPolicy.allowsSystemTrustFallback)
        XCTAssertEqual(parameters.requiredInterfaceType, .cellular)
        XCTAssertEqual(parameters.prohibitedInterfaceTypes, [.wifi, .wiredEthernet])
        XCTAssertFalse(parameters.includePeerToPeer)
        XCTAssertFalse(parameters.allowLocalEndpointReuse)
        _ = CellularPinnedHTTPTransport(timeout: 30)
    }

    func testProductionParameterBuilderRequestsStoredIdentityForMTLS() throws {
        let authority = try CertificateAuthorityValidator().validate(TestFixtures.validCAPEM, at: TestFixtures.now)
        let identity = Task2Fixtures.identity(keyTag: "mobile-egress.agent.key.acceptance")
        let configuration = try PinnedCellularTransportConfiguration(
            relayOrigin: "https://relay.example:8443",
            pinnedCertificateAuthorityDER: authority.der,
            identity: identity
        )
        let builder = ApplePinnedTransportParameterBuilder(
            identityResolver: ThrowingIdentityResolver(),
            timeout: 30
        )

        XCTAssertThrowsError(try builder.makeParameters(configuration: configuration)) { error in
            XCTAssertEqual(error as? ProductionAdapterTestError, .identityRequested(identity.keyTag))
        }
    }

    func testSharedKeychainStoreCanBeProbedWhenAcceptanceIsEnabled() throws {
        let environment = ProcessInfo.processInfo.environment
        guard environment["MOBILE_EGRESS_RUN_KEYCHAIN_ACCEPTANCE"] == "1",
              let accessGroup = environment["MOBILE_EGRESS_KEYCHAIN_ACCESS_GROUP"],
              !accessGroup.isEmpty
        else {
            throw XCTSkip("Set the Keychain acceptance flag and an entitled access group to run this test")
        }

        let store = try SharedKeychainIdentityStore(accessGroup: accessGroup)
        _ = try store.load()
    }

    func testSecureEnclaveManagerCreatesAndSignsOnAnEnabledPhysicalDevice() throws {
        #if os(iOS)
        #if targetEnvironment(simulator)
        throw XCTSkip("Secure Enclave key generation requires a supported physical device")
        #else
        let environment = ProcessInfo.processInfo.environment
        guard environment["MOBILE_EGRESS_RUN_SECURE_ENCLAVE_ACCEPTANCE"] == "1",
              let accessGroup = environment["MOBILE_EGRESS_KEYCHAIN_ACCESS_GROUP"],
              !accessGroup.isEmpty
        else {
            throw XCTSkip("Set the Secure Enclave acceptance flag and an entitled access group on a physical device")
        }
        let manager = try SecureEnclaveIdentityKeyManager(accessGroup: accessGroup)
        let material = try manager.createKey()
        defer { try? manager.deleteKey(tag: material.keyTag) }
        let privateKey = try manager.privateKey(forTag: material.keyTag)
        let message = Data("mobile-egress-secure-enclave-acceptance".utf8)
        var exportError: Unmanaged<CFError>?
        var signingError: Unmanaged<CFError>?
        let signature = try XCTUnwrap(SecKeyCreateSignature(
            privateKey,
            .ecdsaSignatureMessageX962SHA256,
            message as CFData,
            &signingError
        ) as Data?)
        let publicKey = try XCTUnwrap(SecKeyCopyPublicKey(privateKey))
        var verificationError: Unmanaged<CFError>?

        XCTAssertNil(SecKeyCopyExternalRepresentation(privateKey, &exportError))
        XCTAssertTrue(SecKeyVerifySignature(
            publicKey,
            .ecdsaSignatureMessageX962SHA256,
            message as CFData,
            signature as CFData,
            &verificationError
        ))
        #endif
        #else
        throw XCTSkip("Secure Enclave acceptance runs only on a supported physical iOS device")
        #endif
    }
}

private enum ProductionAdapterTestError: Error, Equatable {
    case identityRequested(String)
}

private struct ThrowingIdentityResolver: SecurityIdentityResolving {
    func securityIdentity(forKeyTag keyTag: String) throws -> SecIdentity {
        throw ProductionAdapterTestError.identityRequested(keyTag)
    }
}
#endif
