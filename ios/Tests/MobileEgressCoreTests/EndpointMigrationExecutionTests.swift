import Foundation
import XCTest
@testable import MobileEgressCore

final class EndpointMigrationExecutionTests: XCTestCase {
    func testConcurrentEnrollmentAndMigrationUseTheNewActiveIdentityWithoutStaleRestore() async throws {
        let old = Task2Fixtures.identity(
            relayOrigin: "https://old-relay.example:8443",
            keyTag: "mobile-egress.agent.key.old"
        )
        let newKey = Task2Fixtures.key(tag: "mobile-egress.agent.key.replacement")
        let enrolled = Task2Fixtures.identity(
            relayOrigin: "https://relay.example:8443",
            keyTag: newKey.keyTag
        )
        let migrated = enrolled.replacingRelayOrigin("https://new-relay.example:9443")
        let store = FakeAgentIdentityStore(current: old)
        let keys = FakeIdentityKeyManager(generated: newKey, existingTags: [old.keyTag])
        let enrollmentPerformer = InterleavingEnrollmentPerformer()
        let migrationPerformer = RecordingMigrationPerformer(result: .success(migrated.relayOrigin))
        let coordinator = IdentityWorkflowCoordinator()
        let enrollmentRepository = EnrollmentRepository(
            keyManager: keys,
            identityStore: store,
            performer: enrollmentPerformer,
            coordinator: coordinator
        )
        let migrationRepository = EndpointMigrationRepository(
            identityStore: store,
            performer: migrationPerformer,
            coordinator: coordinator
        )

        let enrollment = Task {
            try await enrollmentRepository.replaceIdentity(using: Task2Fixtures.pairing())
        }
        await enrollmentPerformer.waitUntilCallCount(1)
        let migration = Task { try await migrationRepository.consume(Task2Fixtures.migration()) }
        await coordinator.waitUntilQueuedOperationCount(1)
        await enrollmentPerformer.releaseFirstCall()

        let enrollmentResult = try await enrollment.value
        let migrationResult = try await migration.value
        XCTAssertEqual(enrollmentResult, enrolled)
        XCTAssertEqual(migrationResult, migrated)
        XCTAssertEqual(migrationPerformer.receivedIdentity, enrolled)
        XCTAssertEqual(store.current, migrated)
        XCTAssertEqual(keys.availableTags, [newKey.keyTag])
        XCTAssertEqual(store.stagedTags, [newKey.keyTag])
    }

    func testMigrationUsesStoredMTLSIdentityAndPersistsOnlyRelayOrigin() async throws {
        let original = Task2Fixtures.identity(relayOrigin: "https://old-relay.example:8443")
        let store = FakeAgentIdentityStore(current: original)
        let performer = RecordingMigrationPerformer(result: .success("https://new-relay.example:9443"))
        let repository = EndpointMigrationRepository(identityStore: store, performer: performer)

        let migrated = try await repository.consume(Task2Fixtures.migration())

        XCTAssertEqual(performer.receivedIdentity, original)
        XCTAssertEqual(migrated, original.replacingRelayOrigin("https://new-relay.example:9443"))
        XCTAssertEqual(store.current, migrated)
        XCTAssertEqual(store.events, ["load", "save:\(original.keyTag)"])
    }

    func testMigrationRejectsDifferentCAWithoutConsumingOrPersisting() async throws {
        let original = Task2Fixtures.identity(relayOrigin: "https://old-relay.example:8443")
        let store = FakeAgentIdentityStore(current: original)
        let performer = RecordingMigrationPerformer(result: .success("https://new-relay.example:9443"))
        let repository = EndpointMigrationRepository(identityStore: store, performer: performer)

        do {
            _ = try await repository.consume(Task2Fixtures.migration(caDER: Task2Fixtures.otherCaDER))
            XCTFail("Expected CA mismatch")
        } catch {}

        XCTAssertEqual(performer.callCount, 0)
        XCTAssertEqual(store.current, original)
        XCTAssertEqual(store.events, ["load"])
    }

    func testMigrationRequiresRelayToConfirmScannedOrigin() async throws {
        let original = Task2Fixtures.identity(relayOrigin: "https://old-relay.example:8443")
        let store = FakeAgentIdentityStore(current: original)
        let performer = RecordingMigrationPerformer(result: .success("https://different.example:9443"))
        let repository = EndpointMigrationRepository(identityStore: store, performer: performer)

        do {
            _ = try await repository.consume(Task2Fixtures.migration())
            XCTFail("Expected relay origin mismatch")
        } catch {}

        XCTAssertEqual(store.current, original)
        XCTAssertEqual(store.events, ["load"])
    }

    func testMigrationHTTPClientPostsCapabilityWithPinnedMTLSAndStrict200Response() async throws {
        let responseBody = try JSONSerialization.data(
            withJSONObject: ["relayUrl": "https://new-relay.example:9443"],
            options: [.sortedKeys]
        )
        let transport = RecordingHTTPTransport(response: HTTPResponse(
            statusCode: 200,
            headers: ["content-type": ["application/json"]],
            body: responseBody
        ))
        let client = EndpointMigrationHTTPClient(transport: transport)
        let identity = Task2Fixtures.identity(relayOrigin: "https://old-relay.example:8443")

        let relayOrigin = try await client.consume(migration: Task2Fixtures.migration(), identity: identity)

        let request = try XCTUnwrap(transport.request)
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: request.body) as? [String: String])
        XCTAssertEqual(object, ["capability": "one-use-migration-capability"])
        XCTAssertEqual(request.path, "/v1/endpoint-migrations/consume")
        XCTAssertEqual(transport.configuration?.localIdentityKeyTag, identity.keyTag)
        XCTAssertEqual(transport.configuration?.pinnedCertificateAuthorityDER, Task2Fixtures.caDER)
        XCTAssertEqual(relayOrigin, "https://new-relay.example:9443")
    }

    func testMigrationHTTPClientRejectsRedirectsAndUnexpectedResponseFields() async throws {
        let invalidResponses = [
            HTTPResponse(statusCode: 301, headers: ["content-type": ["application/json"]], body: Data()),
            HTTPResponse(
                statusCode: 200,
                headers: ["content-type": ["application/json"]],
                body: try JSONSerialization.data(withJSONObject: [
                    "relayUrl": "https://new-relay.example:9443",
                    "unexpected": true,
                ])
            ),
        ]

        for response in invalidResponses {
            let client = EndpointMigrationHTTPClient(transport: RecordingHTTPTransport(response: response))
            do {
                _ = try await client.consume(
                    migration: Task2Fixtures.migration(),
                    identity: Task2Fixtures.identity()
                )
                XCTFail("Expected strict migration response rejection")
            } catch {}
        }
    }
}
