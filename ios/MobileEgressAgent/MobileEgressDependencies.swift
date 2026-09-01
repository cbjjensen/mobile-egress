import Foundation
import MobileEgressCore

enum ScanWorkflowResult: Sendable {
    case enrolled
    case migrated
}

enum ScanWorkflowError: Error, Sendable {
    case enrollmentRejected
    case migrationRejected
}

struct MobileEgressDependencies: Sendable {
    let configuration: MobileEgressSystemConfiguration
    private let identityStore: SharedKeychainIdentityStore
    private let enrollmentRepository: EnrollmentRepository
    private let migrationRepository: EndpointMigrationRepository

    static func live(bundle: Bundle = .main) throws -> MobileEgressDependencies {
        let configuration = try MobileEgressSystemConfiguration(
            providerBundleIdentifier: bundle.object(
                forInfoDictionaryKey: "MobileEgressProviderBundleIdentifier"
            ) as? String ?? "",
            appGroupIdentifier: bundle.object(
                forInfoDictionaryKey: "MobileEgressAppGroupIdentifier"
            ) as? String ?? "",
            keychainAccessGroup: bundle.object(
                forInfoDictionaryKey: "MobileEgressKeychainAccessGroup"
            ) as? String ?? ""
        )
        let identityStore = try SharedKeychainIdentityStore(
            accessGroup: configuration.keychainAccessGroup
        )
        let keyManager = try SecureEnclaveIdentityKeyManager(
            accessGroup: configuration.keychainAccessGroup
        )
        let transport = CellularPinnedHTTPTransport(identityResolver: identityStore)
        let enrollmentClient = EnrollmentHTTPClient(
            transport: transport,
            certificateAuthorityValidator: CertificateAuthorityValidator(),
            certificateValidator: SecurityEnrollmentCertificateValidator()
        )
        let migrationClient = EndpointMigrationHTTPClient(transport: transport)

        return MobileEgressDependencies(
            configuration: configuration,
            identityStore: identityStore,
            enrollmentRepository: EnrollmentRepository(
                keyManager: keyManager,
                identityStore: identityStore,
                performer: enrollmentClient
            ),
            migrationRepository: EndpointMigrationRepository(
                identityStore: identityStore,
                performer: migrationClient
            )
        )
    }

    func hasIdentity() throws -> Bool {
        try identityStore.load() != nil
    }

    func processScannedPayload(_ payload: String) async throws -> ScanWorkflowResult {
        let migrationParser = EndpointMigrationParser()
        if migrationParser.recognizes(payload) {
            do {
                let migration = try migrationParser.parse(payload)
                _ = try await migrationRepository.consume(migration)
                return .migrated
            } catch {
                throw ScanWorkflowError.migrationRejected
            }
        }

        do {
            let pairing = try PairingBundleParser().parse(payload)
            _ = try await enrollmentRepository.replaceIdentity(using: pairing)
            return .enrolled
        } catch {
            throw ScanWorkflowError.enrollmentRejected
        }
    }
}
