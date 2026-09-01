import XCTest
@testable import MobileEgressCore

final class EnrollmentReplacementTests: XCTestCase {
    func testConcurrentReplacementsCannotRestoreStaleMetadataOrLeakFailedCandidate() async throws {
        let old = Task2Fixtures.identity(keyTag: "mobile-egress.agent.key.old")
        let firstKey = Task2Fixtures.key(tag: "mobile-egress.agent.key.first")
        let secondKey = Task2Fixtures.key(tag: "mobile-egress.agent.key.second")
        let firstIdentity = Task2Fixtures.identity(keyTag: firstKey.keyTag, serial: "A1")
        let store = FakeAgentIdentityStore(current: old)
        store.saveFailures = [secondKey.keyTag]
        let keys = FakeIdentityKeyManager(generated: [firstKey, secondKey], existingTags: [old.keyTag])
        let performer = InterleavingEnrollmentPerformer()
        let coordinator = IdentityWorkflowCoordinator()
        let repository = EnrollmentRepository(
            keyManager: keys,
            identityStore: store,
            performer: performer,
            coordinator: coordinator
        )

        let first = Task { try await repository.replaceIdentity(using: Task2Fixtures.pairing()) }
        await performer.waitUntilCallCount(1)
        let second = Task { try await repository.replaceIdentity(using: Task2Fixtures.pairing()) }
        await coordinator.waitUntilQueuedOperationCount(1)
        await performer.releaseFirstCall()

        let firstResult = try await first.value
        XCTAssertEqual(firstResult, firstIdentity)
        do {
            _ = try await second.value
            XCTFail("Expected the second replacement to fail during persistence")
        } catch {}
        XCTAssertEqual(store.current, firstIdentity)
        XCTAssertEqual(store.stagedTags, [firstKey.keyTag])
        XCTAssertEqual(keys.availableTags, [firstKey.keyTag])
        XCTAssertEqual(keys.deleteAttempts, [old.keyTag, secondKey.keyTag])
    }

    func testDurableReplacementPrecedesOldCredentialCleanup() async throws {
        let old = Task2Fixtures.identity(keyTag: "mobile-egress.agent.key.old")
        let new = Task2Fixtures.identity()
        let store = FakeAgentIdentityStore(current: old)
        let keys = FakeIdentityKeyManager(
            generated: Task2Fixtures.key(),
            existingTags: [old.keyTag]
        )
        let repository = EnrollmentRepository(
            keyManager: keys,
            identityStore: store,
            performer: FakeEnrollmentPerformer(result: .success(new))
        )

        let result = try await repository.replaceIdentity(using: Task2Fixtures.pairing())

        XCTAssertEqual(result, new)
        XCTAssertEqual(store.current, new)
        XCTAssertEqual(store.events, [
            "load",
            "stage:\(new.keyTag)",
            "save:\(new.keyTag)",
            "remove:\(old.keyTag)",
        ])
        XCTAssertEqual(keys.deleteAttempts, [old.keyTag])
        XCTAssertTrue(keys.availableTags.contains(new.keyTag))
    }

    func testNetworkOrValidationFailureRetainsOldIdentityAndDeletesNewKey() async throws {
        let old = Task2Fixtures.identity(keyTag: "mobile-egress.agent.key.old")
        let store = FakeAgentIdentityStore(current: old)
        let keys = FakeIdentityKeyManager(generated: Task2Fixtures.key(), existingTags: [old.keyTag])
        let repository = EnrollmentRepository(
            keyManager: keys,
            identityStore: store,
            performer: FakeEnrollmentPerformer(result: .failure(TestTask2Error.injected))
        )

        do {
            _ = try await repository.replaceIdentity(using: Task2Fixtures.pairing())
            XCTFail("Expected enrollment failure")
        } catch {}

        XCTAssertEqual(store.current, old)
        XCTAssertEqual(store.events, ["load"])
        XCTAssertEqual(keys.deleteAttempts, [Task2Fixtures.key().keyTag])
        XCTAssertTrue(keys.availableTags.contains(old.keyTag))
        XCTAssertFalse(keys.availableTags.contains(Task2Fixtures.key().keyTag))
    }

    func testPersistenceFailureRetainsOldIdentityAndCleansNewCertificateAndKey() async throws {
        let old = Task2Fixtures.identity(keyTag: "mobile-egress.agent.key.old")
        let new = Task2Fixtures.identity()
        let store = FakeAgentIdentityStore(current: old)
        store.failSave = true
        let keys = FakeIdentityKeyManager(generated: Task2Fixtures.key(), existingTags: [old.keyTag])
        let repository = EnrollmentRepository(
            keyManager: keys,
            identityStore: store,
            performer: FakeEnrollmentPerformer(result: .success(new))
        )

        do {
            _ = try await repository.replaceIdentity(using: Task2Fixtures.pairing())
            XCTFail("Expected persistence failure")
        } catch {}

        XCTAssertEqual(store.current, old)
        XCTAssertEqual(store.events, [
            "load",
            "stage:\(new.keyTag)",
            "save:\(new.keyTag)",
            "remove:\(new.keyTag)",
        ])
        XCTAssertFalse(store.stagedTags.contains(new.keyTag))
        XCTAssertEqual(keys.deleteAttempts, [new.keyTag])
        XCTAssertTrue(keys.availableTags.contains(old.keyTag))
    }

    func testOldCleanupFailureDoesNotRollBackDurableNewIdentity() async throws {
        let old = Task2Fixtures.identity(keyTag: "mobile-egress.agent.key.old")
        let new = Task2Fixtures.identity()
        let store = FakeAgentIdentityStore(current: old)
        store.removeFailures = [old.keyTag]
        let keys = FakeIdentityKeyManager(generated: Task2Fixtures.key(), existingTags: [old.keyTag])
        keys.deleteFailures = [old.keyTag]
        let repository = EnrollmentRepository(
            keyManager: keys,
            identityStore: store,
            performer: FakeEnrollmentPerformer(result: .success(new))
        )

        let result = try await repository.replaceIdentity(using: Task2Fixtures.pairing())

        XCTAssertEqual(result, new)
        XCTAssertEqual(store.current, new)
        XCTAssertTrue(keys.availableTags.contains(new.keyTag))
        XCTAssertEqual(keys.deleteAttempts, [old.keyTag])
    }
}
