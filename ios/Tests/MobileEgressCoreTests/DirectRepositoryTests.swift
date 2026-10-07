#if canImport(Security)
import Foundation
import Security
import XCTest
@testable import MobileEgressCore

final class DirectRepositoryTests: XCTestCase {
    func testIndependentPhonesRetainDistinctPairingsForTheSameClient() async throws {
        let clientID = UUID().uuidString.lowercased()
        let firstStore = MemoryDirectVault(); let secondStore = MemoryDirectVault()
        let firstKeys = MemoryDirectKeys(); let secondKeys = MemoryDirectKeys()
        let first = try DirectClientRepository(store: firstStore, keys: firstKeys, control: RecoverableDirectControl(store: firstStore))
        let second = try DirectClientRepository(store: secondStore, keys: secondKeys, control: RecoverableDirectControl(store: secondStore))
        _ = try await first.add(invitation(clientID: clientID))
        _ = try await second.add(invitation(clientID: clientID))
        // Each independent phone persists its issuance before retrying the lost ACK.
        do { try await first.recover(clientID); XCTFail("first ACK should be lost") } catch { }
        do { try await second.recover(clientID); XCTFail("first ACK should be lost") } catch { }
        try await first.recover(clientID)
        try await second.recover(clientID)
        let firstBefore = await first.snapshot()
        let secondBefore = await second.snapshot()
        XCTAssertEqual(firstBefore.clients[0].clientID, secondBefore.clients[0].clientID)
        XCTAssertNotEqual(firstBefore.clients[0].pairingID, secondBefore.clients[0].pairingID)
        XCTAssertFalse(secondBefore.clients[0].needsAcknowledgement)
        try await first.remove(clientID)
        let removed = await first.snapshot()
        XCTAssertTrue(removed.clients.isEmpty)
        let reopened = try DirectClientRepository(store: secondStore, keys: secondKeys, control: RecoverableDirectControl(store: secondStore))
        let surviving = await reopened.snapshot()
        XCTAssertEqual(surviving, secondBefore)
        XCTAssertTrue(secondKeys.deleted.isEmpty)
    }

    func testRePairAfterReconstructionCannotInheritAnObsoleteRemovalMarker() async throws {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: directory) }
        let journal = FailingCleanupRemovalIntents(url: directory.appendingPathComponent("removal-intents.json"))
        let store = MemoryDirectVault(); let keys = MemoryDirectKeys()
        let control = RecoverableDirectControl(store: store)
        let first = try DirectClientRepository(store: store, keys: keys, control: control, removalIntents: journal)
        let id = try await first.add(invitation())
        journal.failNextCleanup()
        try await first.remove(id)
        XCTAssertEqual(try journal.load(), [id])
        let reopened = try DirectClientRepository(store: store, keys: keys, control: control, removalIntents: journal)
        _ = try await reopened.add(invitation(clientID: id))
        let nextLaunch = try DirectClientRepository(store: store, keys: keys, control: control, removalIntents: journal)
        let snapshot = await nextLaunch.snapshot()
        XCTAssertEqual(snapshot.clients.count, 1)
        XCTAssertTrue(snapshot.clients[0].enabled)
        XCTAssertFalse(snapshot.clients[0].removing, "a new explicit pairing cannot inherit a deleted pairing's marker")
    }
    func testCompletedRemovalPrunesObsoleteMarkersAndCannotExhaustJournalCapacity() async throws {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: directory) }
        let journal = FailingCleanupRemovalIntents(url: directory.appendingPathComponent("removal-intents.json"))
        let store = MemoryDirectVault(); let keys = MemoryDirectKeys()
        let control = RecoverableDirectControl(store: store)
        let repository = try DirectClientRepository(store: store, keys: keys, control: control, removalIntents: journal)
        for _ in 0..<12 {
            let id = try await repository.add(invitation())
            journal.failNextCleanup()
            do { try await repository.remove(id) }
            catch { XCTFail("completed credential deletion must not report Retry Remove for an absent row") }
            let snapshot = await repository.snapshot()
            XCTAssertTrue(snapshot.clients.isEmpty)
            XCTAssertLessThanOrEqual(try journal.load().count, 1, "markers for authoritatively absent Clients cannot consume later removal capacity")
        }
        XCTAssertEqual(keys.deleted.count, 12)
        // Reconstruct with the stale file and prove a later removal can persist.
        let reopened = try DirectClientRepository(store: store, keys: keys, control: control, removalIntents: journal)
        let id = try await reopened.add(invitation())
        store.failSave = true
        do { try await reopened.remove(id); XCTFail("expected Keychain failure") }
        catch { XCTAssertEqual(error as? DirectAgentError, .persistence) }
        let pending = await reopened.snapshot()
        XCTAssertTrue(pending.clients[0].removing)
        XCTAssertEqual(try journal.load(), [id])
    }
    #if canImport(Network)
    func testPeerRecoveryPausesOnlyAffectedPeerAndExplicitRetryPreservesOtherPeerAndKeys() async throws {
        for error in [DirectAgentError.authorizationDenied, .persistence] {
            let store = MemoryDirectVault(); let keys = MemoryDirectKeys()
            let control = PeerRecoveryControl(error: error)
            let repository = try DirectClientRepository(store: store, keys: keys, control: control)
            let affected = try await repository.add(invitation())
            let other = try await repository.add(invitation())
            await control.selectAffected(affected)
            let supervisor = DirectAgentSupervisor(repository: repository, identityResolver: UnavailableDirectIdentityResolver())
            await supervisor.reconcile(serve: true)
            for _ in 0..<100 {
                if await supervisor.statuses().first(where: { $0.clientID == affected })?.recovery != nil { break }
                try await Task.sleep(for: .milliseconds(5))
            }
            let initial = await supervisor.statuses()
            XCTAssertEqual(initial.first(where: { $0.clientID == affected })?.recovery, error == .persistence ? .unlockRequired : .pairingRequired)
            XCTAssertEqual(Set(initial.map(\.clientID)), [affected, other])
            let attempts = await control.attempts(affected)
            await supervisor.reconcile(serve: true)
            try await Task.sleep(for: .milliseconds(1100))
            let laterAttempts = await control.attempts(affected)
            let otherAttempts = await control.attempts(other)
            XCTAssertEqual(laterAttempts, attempts, "ordinary reconciliation must not reset required recovery")
            XCTAssertGreaterThanOrEqual(otherAttempts, 2, "the unaffected peer continues its retry policy")
            await supervisor.retryPeer(affected)
            await supervisor.reconcile(serve: true)
            for _ in 0..<100 {
                if await control.attempts(affected) > attempts { break }
                try await Task.sleep(for: .milliseconds(5))
            }
            let retried = await control.attempts(affected)
            XCTAssertGreaterThan(retried, attempts)
            let snapshot = await repository.snapshot()
            XCTAssertEqual(snapshot.clients.count, 2)
            XCTAssertTrue(keys.deleted.isEmpty)
            await supervisor.stop()
        }
    }
    func testSupervisorRemovalSuppressionSurvivesOrdinaryReconcileAndStartWithoutStoppingOtherPeers() async throws {
        let store = MemoryDirectVault(); let keys = MemoryDirectKeys()
        let repository = try DirectClientRepository(store: store, keys: keys, control: RecoverableDirectControl(store: store))
        let removed = try await repository.add(invitation())
        let other = try await repository.add(invitation())
        let supervisor = DirectAgentSupervisor(repository: repository, identityResolver: UnavailableDirectIdentityResolver())
        await supervisor.reconcile(serve: true)
        let before = await supervisor.statuses()
        XCTAssertEqual(Set(before.map(\.clientID)), [removed, other])
        // This deliberately precedes any repository mutation, covering the
        // stop-then-storage window and ordinary stale registry snapshots.
        await supervisor.suppressPeerForRemoval(removed)
        await supervisor.reconcile(serve: true)
        let after = await supervisor.statuses()
        XCTAssertEqual(after.map(\.clientID), [other])
        await supervisor.stop()
        await supervisor.reconcile(serve: true)
        let restarted = await supervisor.statuses()
        XCTAssertEqual(restarted.map(\.clientID), [other])
        await supervisor.stop()
    }
    #endif
    func testDelayedIssuedResponseCannotRestoreClientAfterFailedRemoval() async throws {
        let store = MemoryDirectVault(); let keys = MemoryDirectKeys()
        let entered = expectation(description: "enrollment awaiting server")
        let control = DeferredIssuingControl(entered: entered)
        let repository = try DirectClientRepository(store: store, keys: keys, control: control)
        let id = try await repository.add(invitation())
        let enrollment = Task { try await repository.recover(id) }
        await fulfillment(of: [entered], timeout: 2)
        store.failSave = true
        do { try await repository.remove(id); XCTFail("expected Keychain failure") } catch { }
        store.failSave = false
        await control.release()
        do { try await enrollment.value; XCTFail("removed peer must reject a late issued result") } catch { }
        let result = await repository.snapshot()
        XCTAssertTrue(result.clients[0].removing)
        XCTAssertFalse(result.clients[0].enabled)
        XCTAssertNil(result.clients[0].identity)
        XCTAssertTrue(keys.deleted.isEmpty)
    }
    func testFailedRemovalJournalStillSuppressesThisProcessAndCannotBeReenabled() async throws {
        let store = MemoryDirectVault(); let keys = MemoryDirectKeys()
        let journal = FailingRemovalIntents()
        let repository = try DirectClientRepository(store: store, keys: keys, control: RecoverableDirectControl(store: store), removalIntents: journal)
        let id = try await repository.add(invitation())
        journal.failFutureWrites()
        do { try await repository.remove(id); XCTFail("expected journal failure") }
        catch { XCTAssertEqual(error as? DirectAgentError, .removalIntentPersistence) }
        let snapshot = await repository.snapshot()
        XCTAssertFalse(snapshot.clients[0].enabled)
        XCTAssertTrue(snapshot.clients[0].removing)
        do { try await repository.setEnabled(id, enabled: true); XCTFail("removal must be retried before enabling") } catch { }
        do { try await repository.recover(id); XCTFail("removed peer must not authenticate") } catch { }
        XCTAssertTrue(keys.deleted.isEmpty)
    }
    func testFailedRemovalStaysDistrustedAcrossRefreshAndReconstructionUntilCleanupSucceeds() async throws {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: directory) }
        let url = directory.appendingPathComponent("removal-intents.json")
        let store = MemoryDirectVault(); let keys = MemoryDirectKeys()
        let control = RecoverableDirectControl(store: store)
        let repository = try DirectClientRepository(store: store, keys: keys, control: control, removalIntents: FileDirectRemovalIntentStore(url: url))
        let removedID = try await repository.add(invitation())
        let otherID = try await repository.add(invitation())
        do { try await repository.recover(removedID) } catch { }
        try await repository.recover(removedID)
        store.failSave = true
        do { try await repository.remove(removedID); XCTFail("expected unavailable Keychain") } catch { }
        let failed = await repository.snapshot()
        XCTAssertEqual(failed.clients.filter { $0.enabled && !$0.removing }.map(\.id), [otherID], "refresh must not make a removed Client eligible again")
        XCTAssertTrue(try XCTUnwrap(failed.clients.first { $0.id == removedID }).removing)
        XCTAssertTrue(keys.deleted.isEmpty, "failed tombstone must retain cleanup credentials")
        let reopened = try DirectClientRepository(store: store, keys: keys, control: control, removalIntents: FileDirectRemovalIntentStore(url: url))
        let restored = await reopened.snapshot()
        XCTAssertEqual(restored.clients.filter { $0.enabled && !$0.removing }.map(\.id), [otherID])
        do { try await reopened.recover(removedID); XCTFail("a removed Client must not authenticate") } catch { }
        store.failSave = false
        try await reopened.remove(removedID)
        let complete = await reopened.snapshot()
        XCTAssertEqual(complete.clients.map(\.id), [otherID])
        XCTAssertEqual(keys.deleted, ["test-key-1"])
        XCTAssertTrue(try FileDirectRemovalIntentStore(url: url).load().isEmpty)
        _ = try await reopened.add(invitation(clientID: removedID))
        let repaired = await reopened.snapshot()
        XCTAssertEqual(repaired.clients.first { $0.id == removedID }?.key?.keyTag, "test-key-3")
    }
    func testLateRejectionCleanupCannotDeleteIssuedIdentityAfterLostAck() async throws {
        let store = MemoryDirectVault(); let keys = MemoryDirectKeys()
        let repository = try DirectClientRepository(store: store, keys: keys, control: RecoverableDirectControl(store: store))
        let id = try await repository.add(invitation())
        let initial = await repository.snapshot()
        let attempted = try XCTUnwrap(initial.clients.first)
        do { try await repository.recover(id); XCTFail("first ACK is deliberately lost") } catch { }
        let issued = await repository.snapshot()
        XCTAssertNotNil(issued.clients[0].identity)
        try await repository.discardRejectedInvitation(attempted)
        let preserved = await repository.snapshot()
        XCTAssertEqual(preserved, issued)
        XCTAssertTrue(keys.deleted.isEmpty)
    }
    func testFailedRejectionRemovalCommitDoesNotDeletePendingKey() async throws {
        let store = MemoryDirectVault(); let keys = MemoryDirectKeys()
        let entered = expectation(description: "enrollment entered")
        let control = RejectingEnrollmentControl(error: .expiredInvitation, entered: entered)
        let repository = try DirectClientRepository(store: store, keys: keys, control: control)
        let id = try await repository.add(invitation())
        let attempt = Task { try await repository.recover(id) }
        await fulfillment(of: [entered], timeout: 2)
        store.failSave = true
        await control.release()
        do { try await attempt.value; XCTFail("expected persistence failure") } catch { XCTAssertEqual(error as? DirectAgentError, .persistence) }
        let snapshot = await repository.snapshot()
        XCTAssertEqual(snapshot.clients.count, 1)
        XCTAssertEqual(snapshot.clients[0].enrollmentAttempted, true)
        XCTAssertTrue(keys.deleted.isEmpty)
    }
    func testDefinitiveEnrollmentRejectionsReleaseAttemptedSlotBeforeDeletingKey() async throws {
        for error in [DirectAgentError.invitationInvalid, .expiredInvitation] {
            let store = MemoryDirectVault(); let keys = MemoryDirectKeys()
            keys.onDelete = { _ in XCTAssertTrue((try? store.load().clients.isEmpty) == true, "removal must be durable before key deletion") }
            let control = RejectingEnrollmentControl(error: error)
            let repository = try DirectClientRepository(store: store, keys: keys, control: control)
            let id = try await repository.add(invitation())
            do { try await repository.recover(id); XCTFail("expected rejection") } catch { }
            let snapshot = await repository.snapshot()
            XCTAssertTrue(snapshot.clients.isEmpty)
            XCTAssertEqual(keys.deleted.count, 1)
        }
    }
    func testUnknownEnrollmentRejectionsRetainAttemptAndSameKey() async throws {
        for error in [DirectAgentError.rejected, .unavailable, .trust] {
            let store = MemoryDirectVault(); let keys = MemoryDirectKeys()
            let repository = try DirectClientRepository(store: store, keys: keys, control: RejectingEnrollmentControl(error: error))
            let id = try await repository.add(invitation())
            do { try await repository.recover(id); XCTFail("expected rejection") } catch { }
            let snapshot = await repository.snapshot()
            XCTAssertEqual(snapshot.clients.count, 1)
            XCTAssertEqual(snapshot.clients[0].enrollmentAttempted, true)
            XCTAssertEqual(snapshot.clients[0].key?.keyTag, "test-key-1")
            XCTAssertTrue(keys.deleted.isEmpty)
        }
    }
    func testLateDefinitiveRejectionCannotRemoveReplacementInvitation() async throws {
        let store = MemoryDirectVault(); let keys = MemoryDirectKeys()
        let entered = expectation(description: "enrollment entered")
        let control = RejectingEnrollmentControl(error: .invitationInvalid, entered: entered)
        let repository = try DirectClientRepository(store: store, keys: keys, control: control)
        let id = try await repository.add(invitation())
        let attempt = Task { try await repository.recover(id) }
        await fulfillment(of: [entered], timeout: 2)
        try await repository.remove(id)
        _ = try await repository.add(invitation(clientID: id))
        await control.release()
        do { try await attempt.value; XCTFail("expected rejection") } catch { }
        let snapshot = await repository.snapshot()
        XCTAssertEqual(snapshot.clients.count, 1)
        XCTAssertEqual(snapshot.clients[0].key?.keyTag, "test-key-2")
        XCTAssertEqual(keys.deleted, ["test-key-1"])
    }
    func testExpiredUnsentInvitationReleasesSlotButUncertainIssuanceRetainsKey() async throws {
        let store = MemoryDirectVault(); let keys = MemoryDirectKeys()
        let invitation = try DirectInvitation.parse(invitation())
        var unused = DirectClientRecord(clientID: UUID().uuidString, displayName: "Unused", endpoint: invitation.endpoint)
        unused.invitation = invitation; unused.key = try keys.createKey()
        var uncertain = DirectClientRecord(clientID: UUID().uuidString, displayName: "Uncertain", endpoint: invitation.endpoint)
        uncertain.invitation = invitation; uncertain.key = try keys.createKey(); uncertain.enrollmentAttempted = true
        var document = DirectRegistryDocument(); try document.insert(unused); try document.insert(uncertain); try store.save(document)
        let repository = try DirectClientRepository(store: store, keys: keys, control: RecoverableDirectControl(store: store), now: { Date(timeIntervalSince1970: 1_893_456_000) })
        let snapshot = await repository.snapshot()
        XCTAssertEqual(snapshot.clients.map(\.id), [uncertain.id])
        XCTAssertEqual(keys.deleted, [try XCTUnwrap(unused.key?.keyTag)])
        XCTAssertEqual(snapshot.clients[0].key, uncertain.key)
    }
    func testRenewalAcknowledgementPreservesConcurrentDisable() async throws {
        let store = MemoryDirectVault()
        let keys = MemoryDirectKeys()
        let url = try XCTUnwrap(Bundle.module.url(forResource: "direct-v2-wire", withExtension: "json", subdirectory: "Fixtures"))
        struct Fixture: Decodable { let identity: DirectIssuedIdentity }
        let fixture = try JSONDecoder().decode(Fixture.self, from: Data(contentsOf: url))
        let issued = fixture.identity
        let expiry = try DirectCertificateExpiration.date(issued.certificatePem)
        let key = try keys.createKey()
        var record = DirectClientRecord(clientID: issued.clientId, displayName: "Workload", endpoint: "https://workload.example")
        record.key = key; record.csrPEM = "saved-csr"; record.pairingID = issued.pairingId; record.generation = issued.generation
        record.identity = AgentIdentity(relayOrigin: record.endpoint, role: "agent", serial: issued.serial, keyTag: key.keyTag, certificatePEM: issued.certificatePem, caCertificatePEM: issued.caCertificatePem, caCertificateDER: try PEMCertificateChain.parse(issued.caCertificatePem)[0])
        var document = DirectRegistryDocument(); try document.insert(record); try store.save(document)
        let entered = expectation(description: "renewal ACK entered")
        let control = DeferredRenewalControl(issued: issued, entered: entered)
        let repository = try DirectClientRepository(store: store, keys: keys, control: control, now: { expiry.addingTimeInterval(-3600) })
        let task = Task { try await repository.maintain(record.id) }
        await fulfillment(of: [entered], timeout: 2)
        try await repository.setEnabled(record.id, enabled: false)
        await control.completeAcknowledgement()
        try await task.value
        let final = await repository.snapshot()
        XCTAssertFalse(final.clients[0].enabled)
        XCTAssertFalse(final.clients[0].needsAcknowledgement)
    }
    func testLostAcknowledgementReopensSamePersistedIdentityWithoutIssuingAgain() async throws {
        let store = MemoryDirectVault()
        let keys = MemoryDirectKeys()
        let control = RecoverableDirectControl(store: store)
        let repository = try DirectClientRepository(store: store, keys: keys, control: control)
        let id = try await repository.add(invitation())
        do { try await repository.recover(id); XCTFail("first acknowledgement should fail") } catch {}
        let pending = await repository.snapshot()
        XCTAssertTrue(pending.clients[0].needsAcknowledgement)
        XCTAssertNotNil(pending.clients[0].identity)
        XCTAssertNotNil(pending.clients[0].invitation)
        let reopened = try DirectClientRepository(store: store, keys: keys, control: control)
        try await reopened.recover(id)
        let complete = await reopened.snapshot()
        XCTAssertEqual(complete.clients[0].key, pending.clients[0].key)
        XCTAssertEqual(complete.clients[0].pairingID, pending.clients[0].pairingID)
        XCTAssertFalse(complete.clients[0].needsAcknowledgement)
        XCTAssertNil(complete.clients[0].invitation)
        let issued = await control.issued
        XCTAssertEqual(issued, 1)
        XCTAssertEqual(keys.created, 1)
    }

    func testPersistenceFailureNeverPublishesSlotOrSendsCSR() async throws {
        let store = MemoryDirectVault(); store.failSave = true
        let keys = MemoryDirectKeys()
        let control = RecoverableDirectControl(store: store)
        let repository = try DirectClientRepository(store: store, keys: keys, control: control)
        do { _ = try await repository.add(invitation()); XCTFail("expected persistence failure") } catch {}
        let snapshot = await repository.snapshot()
        XCTAssertTrue(snapshot.clients.isEmpty)
        XCTAssertEqual(keys.created, 1)
        XCTAssertEqual(keys.deleted.count, 1)
        let issued = await control.issued
        XCTAssertEqual(issued, 0)
    }

    func testTenDistinctPairKeysAndRemovalDoesNotDeleteOtherPeerKeys() async throws {
        let store = MemoryDirectVault(); let keys = MemoryDirectKeys()
        let repository = try DirectClientRepository(store: store, keys: keys, control: RecoverableDirectControl(store: store))
        for _ in 0..<10 { _ = try await repository.add(invitation()) }
        let original = await repository.snapshot()
        XCTAssertEqual(Set(original.clients.compactMap { $0.key?.keyTag }).count, 10)
        do { _ = try await repository.add(invitation()); XCTFail("eleventh pairing should fail") } catch {}
        XCTAssertEqual(keys.created, 10)
        try await repository.remove(original.clients[3].id)
        let remaining = await repository.snapshot()
        XCTAssertEqual(remaining.clients.count, 9)
        XCTAssertEqual(keys.deleted, [try XCTUnwrap(original.clients[3].key?.keyTag)])
        XCTAssertEqual(remaining.clients.map(\.id), original.clients.filter { $0.id != original.clients[3].id }.map(\.id))
    }

    private func invitation(clientID: String = UUID().uuidString.lowercased()) throws -> String {
        DirectBundle.encode(try JSONSerialization.data(withJSONObject: ["version": 2, "type": "mobile-egress-direct-invitation", "clientId": clientID, "displayName": "Workload", "endpoint": "https://workload.example", "caCertificatePem": TestFixtures.validCAPEM, "invitationId": UUID().uuidString.lowercased(), "capability": DirectBundle.encode(Data(repeating: 7, count: 32)), "expiresAt": "2029-01-01T00:00:00Z", "role": "agent"]))
    }
}

private final class MemoryDirectVault: DirectIdentityVault, @unchecked Sendable {
    private let lock = NSLock()
    private var value = DirectRegistryDocument()
    var failSave = false
    func load() throws -> DirectRegistryDocument { lock.withLock { value } }
    func save(_ document: DirectRegistryDocument) throws { try lock.withLock { if failSave { throw DirectAgentError.persistence }; try document.validate(); value = document } }
    func stage(_ identity: AgentIdentity) throws {}
    func remove(_ identity: AgentIdentity) throws {}
}
private final class MemoryDirectKeys: DirectIdentityKeyManaging, @unchecked Sendable {
    var onDelete: (@Sendable (String) -> Void)?
    private let lock = NSLock()
    private var next = 0
    private var removed: [String] = []
    var created: Int { lock.withLock { next } }
    var deleted: [String] { lock.withLock { removed } }
    func createKey() throws -> IdentityKeyMaterial { lock.withLock { next += 1; return IdentityKeyMaterial(keyTag: "test-key-\(next)", publicKeyPEM: "public-\(next)", publicKeySPKIDER: Data([UInt8(next)])) } }
    func deleteKey(tag: String) throws { onDelete?(tag); lock.withLock { removed.append(tag) } }
    func createCSR(key: IdentityKeyMaterial, clientID: String) throws -> String { "csr-\(key.keyTag)-\(clientID)" }
}
private actor RecoverableDirectControl: DirectControlServing {
    let store: MemoryDirectVault
    var issued = 0
    var acknowledgements = 0
    init(store: MemoryDirectVault) { self.store = store }
    func issue(_ record: DirectClientRecord, renewal: Bool) async throws -> DirectIssuedIdentity {
        let saved = try store.load().clients.first { $0.id == record.id }
        guard saved?.key == record.key, saved?.csrPEM == record.csrPEM, saved?.identity == nil else { throw DirectAgentError.persistence }
        issued += 1
        return DirectIssuedIdentity(certificatePem: "test-leaf", caCertificatePem: TestFixtures.validCAPEM, serial: "A1", role: "agent", clientId: record.id, pairingId: UUID().uuidString.lowercased(), generation: 1)
    }
    func acknowledge(_ record: DirectClientRecord) async throws {
        guard try store.load().clients.first(where: { $0.id == record.id })?.identity == record.identity, record.identity != nil else { throw DirectAgentError.persistence }
        acknowledgements += 1
        if acknowledgements == 1 { throw DirectAgentError.unavailable }
    }
    func configuration(_ record: DirectClientRecord) async throws -> String { "" }
}
private actor DeferredRenewalControl: DirectControlServing {
    let issued: DirectIssuedIdentity
    let entered: XCTestExpectation
    var continuation: CheckedContinuation<Void, Never>?
    init(issued: DirectIssuedIdentity, entered: XCTestExpectation) { self.issued = issued; self.entered = entered }
    func issue(_ record: DirectClientRecord, renewal: Bool) async throws -> DirectIssuedIdentity { issued }
    func configuration(_ record: DirectClientRecord) async throws -> String { "" }
    func acknowledge(_ record: DirectClientRecord) async throws {
        await withCheckedContinuation { continuation in self.continuation = continuation; entered.fulfill() }
    }
    func completeAcknowledgement() { continuation?.resume(); continuation = nil }
}
private actor RejectingEnrollmentControl: DirectControlServing {
    let error: DirectAgentError
    let entered: XCTestExpectation?
    var continuation: CheckedContinuation<Void, Never>?
    init(error: DirectAgentError, entered: XCTestExpectation? = nil) { self.error = error; self.entered = entered }
    func issue(_ record: DirectClientRecord, renewal: Bool) async throws -> DirectIssuedIdentity {
        if let entered { await withCheckedContinuation { continuation in self.continuation = continuation; entered.fulfill() } }
        throw error
    }
    func acknowledge(_ record: DirectClientRecord) async throws { throw error }
    func configuration(_ record: DirectClientRecord) async throws -> String { "" }
    func release() { continuation?.resume(); continuation = nil }
}
private final class FailingRemovalIntents: DirectRemovalIntentPersisting, @unchecked Sendable {
    private let lock = NSLock()
    private var failWrites = false
    func failFutureWrites() { lock.withLock { failWrites = true } }
    func load() throws -> Set<String> { [] }
    func save(_ clientIDs: Set<String>) throws { if lock.withLock({ failWrites }) { throw DirectAgentError.persistence } }
}
private final class FailingCleanupRemovalIntents: DirectRemovalIntentPersisting, @unchecked Sendable {
    private let lock = NSLock()
    private var shouldFailCleanup = false
    private let backing: FileDirectRemovalIntentStore
    init(url: URL) { backing = FileDirectRemovalIntentStore(url: url) }
    func failNextCleanup() { lock.withLock { shouldFailCleanup = true } }
    func load() throws -> Set<String> { try backing.load() }
    func save(_ ids: Set<String>) throws {
        if try ids.isStrictSubset(of: backing.load()), lock.withLock({ let fail = shouldFailCleanup; shouldFailCleanup = false; return fail }) { throw DirectAgentError.persistence }
        try backing.save(ids)
    }
}
private struct UnavailableDirectIdentityResolver: SecurityIdentityResolving {
    func securityIdentity(forKeyTag keyTag: String) throws -> SecIdentity { throw IdentityError.identityUnavailable }
}
private actor PeerRecoveryControl: DirectControlServing {
    let error: DirectAgentError
    var affected = ""
    var counts: [String: Int] = [:]
    init(error: DirectAgentError) { self.error = error }
    func selectAffected(_ id: String) { affected = id }
    func attempts(_ id: String) -> Int { counts[id, default: 0] }
    func issue(_ record: DirectClientRecord, renewal: Bool) async throws -> DirectIssuedIdentity {
        counts[record.id, default: 0] += 1
        throw record.id == affected ? error : DirectAgentError.clientStorageUnavailable
    }
    func acknowledge(_ record: DirectClientRecord) async throws { }
    func configuration(_ record: DirectClientRecord) async throws -> String { "" }
}
private actor DeferredIssuingControl: DirectControlServing {
    let entered: XCTestExpectation
    var continuation: CheckedContinuation<Void, Never>?
    init(entered: XCTestExpectation) { self.entered = entered }
    func issue(_ record: DirectClientRecord, renewal: Bool) async throws -> DirectIssuedIdentity {
        await withCheckedContinuation { continuation in self.continuation = continuation; entered.fulfill() }
        return DirectIssuedIdentity(certificatePem: "test-leaf", caCertificatePem: TestFixtures.validCAPEM, serial: "A1", role: "agent", clientId: record.id, pairingId: UUID().uuidString, generation: 1)
    }
    func acknowledge(_ record: DirectClientRecord) async throws { }
    func configuration(_ record: DirectClientRecord) async throws -> String { "" }
    func release() { continuation?.resume(); continuation = nil }
}
#endif
