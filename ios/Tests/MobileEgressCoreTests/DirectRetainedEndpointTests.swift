#if canImport(Security)
import Foundation
import XCTest
@testable import MobileEgressCore

final class DirectRetainedEndpointTests: XCTestCase, @unchecked Sendable {
    func testDetailsImportCannotUpdateAnotherSavedClient() async throws {
        let fixture = try fixture()
        let update = try XCTUnwrap(fixture.retainedUpdates.first)
        let original = try record(fixture: fixture, endpoint: update.endpoint, generation: 1, pending: false)
        let vault = RetainedEndpointVault(original)
        let repository = try DirectClientRepository(store: vault, keys: RetainedEndpointKeys(), control: RetainedEndpointControl(vault: vault))
        let route = try ClientCodeImportDestination.connectionUpdate(clientID: "another-client").route(isInvitation: false)
        guard case let .update(expectedID) = route else { return XCTFail("Expected an update") }
        do {
            try await repository.importUpdate(update.update, expectedClientID: expectedID)
            XCTFail("An update for a different Client must not succeed")
        } catch { XCTAssertEqual(error as? DirectAgentError, .staleUpdate) }
        XCTAssertEqual(try vault.load().clients, [original])
        // The same signed update is valid when imported from its own Client.
        try await repository.importUpdate(update.update, expectedClientID: original.id)
        XCTAssertEqual(try vault.load().clients[0].generation, update.generation)
    }
    func testRetainedIdentityReachesNativeKeyLookupAndStagingWithoutWeakeningIdentityValidation() throws {
        let fixture = try fixture()
        let store = try DirectKeychainStore(accessGroup: "test.mobile-egress.absent.\(UUID().uuidString)")
        for update in fixture.retainedUpdates {
            let keyTag = "test.mobile-egress.retained.\(UUID().uuidString)"
            let retained = try XCTUnwrap(record(fixture: fixture, endpoint: update.endpoint, generation: update.generation, pending: false, keyTag: keyTag).identity)
            defer { try? store.remove(retained) }
            XCTAssertThrowsError(try store.securityIdentity(for: retained)) { error in
                // This test deliberately has no entitled key. Reaching key lookup
                // proves that a valid retained origin passed the real native adapter.
                XCTAssertTrue(error as? IdentityError == .keyMissing || error as? IdentityError == .secureStorageUnavailable, "unexpected failure before native key lookup: \(error)")
            }
            XCTAssertThrowsError(try store.stage(retained)) { error in
                XCTAssertTrue(error as? IdentityError == .certificatePersistenceFailed || error as? IdentityError == .keyMissing || error as? IdentityError == .secureStorageUnavailable, "unexpected failure before native certificate staging: \(error)")
            }
            for invalid in [
                AgentIdentity(relayOrigin: retained.relayOrigin, role: "owner", serial: retained.serial, keyTag: retained.keyTag, certificatePEM: retained.certificatePEM, caCertificatePEM: retained.caCertificatePEM, caCertificateDER: retained.caCertificateDER),
                AgentIdentity(relayOrigin: retained.relayOrigin, role: retained.role, serial: "invalid-serial", keyTag: retained.keyTag, certificatePEM: retained.certificatePEM, caCertificatePEM: retained.caCertificatePEM, caCertificateDER: retained.caCertificateDER),
                AgentIdentity(relayOrigin: retained.relayOrigin, role: retained.role, serial: retained.serial, keyTag: retained.keyTag, certificatePEM: retained.certificatePEM, caCertificatePEM: retained.caCertificatePEM, caCertificateDER: Data([1])),
            ] {
                XCTAssertThrowsError(try store.securityIdentity(for: invalid)) {
                    XCTAssertEqual($0 as? IdentityError, .invalidIdentity)
                }
                XCTAssertThrowsError(try store.stage(invalid)) {
                    XCTAssertEqual($0 as? IdentityError, .invalidIdentity)
                }
            }
        }
    }

    func testGoSignedRetainedAuthoritiesPersistBeforeAcknowledgementWithoutReplacingPairing() async throws {
        let fixture = try fixture()
        for update in fixture.retainedUpdates {
            for (generation, pending) in [(Int64(1), false), (update.generation, false), (update.generation, true)] {
                let original = try record(fixture: fixture, endpoint: update.endpoint, generation: generation, pending: pending)
                let vault = RetainedEndpointVault(original)
                let control = RetainedEndpointControl(vault: vault)
                let repository = try DirectClientRepository(store: vault, keys: RetainedEndpointKeys(), control: control)

                try await repository.importUpdate(update.update)
                let saved = try XCTUnwrap(vault.load().clients.first)
                XCTAssertEqual(saved.endpoint, update.canonicalEndpoint)
                XCTAssertEqual(saved.identity, original.identity?.replacingRelayOrigin(update.canonicalEndpoint))
                XCTAssertEqual(saved.key, original.key)
                XCTAssertEqual(saved.pairingID, original.pairingID)
                XCTAssertEqual(saved.generation, update.generation)
                XCTAssertEqual(saved.needsAcknowledgement, pending || generation < update.generation)

                let reopened = try DirectClientRepository(store: vault, keys: RetainedEndpointKeys(), control: control)
                try await reopened.recover(saved.id)
                let acknowledgements = await control.acknowledgements
                XCTAssertEqual(acknowledgements.count, saved.needsAcknowledgement ? 1 : 0)
                if let acknowledged = acknowledgements.first {
                    XCTAssertEqual(acknowledged.endpoint, update.canonicalEndpoint)
                    XCTAssertEqual(acknowledged.identity, saved.identity)
                }
                XCTAssertFalse(try XCTUnwrap(vault.load().clients.first).needsAcknowledgement)
                try await reopened.importUpdate(update.update)
                XCTAssertFalse(try XCTUnwrap(vault.load().clients.first).needsAcknowledgement)
            }
        }
    }

    func testEqualGenerationNormalizationFailureLeavesOriginalRecordRecoverable() async throws {
        let fixture = try fixture()
        let update = try XCTUnwrap(fixture.retainedUpdates.first)
        let original = try record(fixture: fixture, endpoint: update.endpoint, generation: update.generation, pending: true)
        let vault = RetainedEndpointVault(original)
        let control = RetainedEndpointControl(vault: vault)
        let repository = try DirectClientRepository(store: vault, keys: RetainedEndpointKeys(), control: control)
        vault.failSave = true
        do {
            try await repository.importUpdate(update.update)
            XCTFail("normalization must persist before publishing the changed record")
        } catch { XCTAssertEqual(error as? DirectAgentError, .persistence) }
        let snapshot = await repository.snapshot()
        XCTAssertEqual(snapshot.clients, [original])
        XCTAssertEqual(try vault.load().clients, [original])
        let acknowledgements = await control.acknowledgements
        XCTAssertTrue(acknowledgements.isEmpty)
    }

    private struct Fixture: Decodable {
        let identity: DirectIssuedIdentity
        let retainedUpdates: [Update]
        struct Update: Decodable {
            let endpoint: String
            let canonicalEndpoint: String
            let generation: Int64
            let update: String
        }
    }

    private func fixture() throws -> Fixture {
        let url = try XCTUnwrap(Bundle.module.url(forResource: "direct-v2-wire", withExtension: "json", subdirectory: "Fixtures"))
        return try JSONDecoder().decode(Fixture.self, from: Data(contentsOf: url))
    }

    private func record(fixture: Fixture, endpoint: String, generation: Int64, pending: Bool, keyTag: String = "retained-pairing-key") throws -> DirectClientRecord {
        let issued = fixture.identity
        let key = IdentityKeyMaterial(keyTag: keyTag, publicKeyPEM: "unused", publicKeySPKIDER: Data([1]))
        var result = DirectClientRecord(clientID: issued.clientId, displayName: "Workload", endpoint: endpoint)
        result.key = key
        result.csrPEM = "retained-pairing-csr"
        result.pairingID = issued.pairingId
        result.generation = generation
        result.needsAcknowledgement = pending
        result.identity = AgentIdentity(relayOrigin: endpoint, role: "agent", serial: issued.serial, keyTag: key.keyTag, certificatePEM: issued.certificatePem, caCertificatePEM: issued.caCertificatePem, caCertificateDER: try PEMCertificateChain.parse(issued.caCertificatePem)[0])
        return result
    }
}

private final class RetainedEndpointVault: DirectIdentityVault, @unchecked Sendable {
    private let lock = NSLock()
    private var value: DirectRegistryDocument
    var failSave = false
    init(_ record: DirectClientRecord) { var document = DirectRegistryDocument(); document.clients = [record]; value = document }
    func load() throws -> DirectRegistryDocument { lock.withLock { value } }
    func save(_ document: DirectRegistryDocument) throws {
        try lock.withLock {
            if failSave { throw DirectAgentError.persistence }
            try document.validate()
            value = document
        }
    }
    func stage(_ identity: AgentIdentity) throws { throw DirectAgentError.unavailable }
    func remove(_ identity: AgentIdentity) throws { throw DirectAgentError.unavailable }
}

private struct RetainedEndpointKeys: DirectIdentityKeyManaging {
    func createKey() throws -> IdentityKeyMaterial { throw DirectAgentError.unavailable }
    func createCSR(key: IdentityKeyMaterial, clientID: String) throws -> String { throw DirectAgentError.unavailable }
    func deleteKey(tag: String) throws { throw DirectAgentError.unavailable }
}

private actor RetainedEndpointControl: DirectControlServing {
    let vault: RetainedEndpointVault
    private(set) var acknowledgements: [DirectClientRecord] = []
    init(vault: RetainedEndpointVault) { self.vault = vault }
    func issue(_ record: DirectClientRecord, renewal: Bool) async throws -> DirectIssuedIdentity { throw DirectAgentError.unavailable }
    func acknowledge(_ record: DirectClientRecord) async throws {
        XCTAssertEqual(try vault.load().clients.first, record, "the exact acknowledged identity must already be durable")
        acknowledgements.append(record)
    }
    func configuration(_ record: DirectClientRecord) async throws -> String { "" }
}
#endif
