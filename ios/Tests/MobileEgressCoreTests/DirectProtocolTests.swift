import Foundation
import XCTest
@testable import MobileEgressCore

final class DirectProtocolTests: XCTestCase {
    func testHostedInvitationPreservesClientTrust() throws {
        var object: [String: Any] = ["version": 2, "type": "mobile-egress-direct-invitation", "clientId": UUID().uuidString, "displayName": "Workload", "endpoint": "https://workload.example", "caCertificatePem": TestFixtures.validCAPEM, "invitationId": UUID().uuidString, "capability": DirectBundle.encode(Data(repeating: 5, count: 32)), "expiresAt": "2029-01-01T00:00:00Z", "role": "agent", "transport": "hosted"]
        let encoded = DirectBundle.encode(try JSONSerialization.data(withJSONObject: object))
        let invitation = try DirectInvitation.parse(encoded, now: TestFixtures.now)
        XCTAssertEqual(invitation.caCertificatePem, TestFixtures.validCAPEM)
        XCTAssertEqual(invitation.transport, .hosted)
        for invalid: Any in [NSNull(), "HOSTED", "relay", 1] {
            object["transport"] = invalid
            XCTAssertThrowsError(try DirectInvitation.parse(DirectBundle.encode(try JSONSerialization.data(withJSONObject: object)), now: TestFixtures.now))
        }
        object.removeValue(forKey: "transport")
        XCTAssertEqual(try DirectInvitation.parse(DirectBundle.encode(try JSONSerialization.data(withJSONObject: object)), now: TestFixtures.now).transport, .direct)
    }
    func testTransportPersistenceDefaultsOldRecordsAndRejectsNull() throws {
        var client = DirectClientRecord(clientID: UUID().uuidString, displayName: "Workload", endpoint: "https://workload.example")
        client.transport = .hosted
        let data = try JSONEncoder().encode(client)
        XCTAssertEqual(try JSONDecoder().decode(DirectClientRecord.self, from: data), client)
        var old = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
        old.removeValue(forKey: "transport")
        XCTAssertEqual(try JSONDecoder().decode(DirectClientRecord.self, from: JSONSerialization.data(withJSONObject: old)).transport, .direct)
        old["transport"] = NSNull()
        XCTAssertThrowsError(try JSONDecoder().decode(DirectClientRecord.self, from: JSONSerialization.data(withJSONObject: old)))
    }
    func testSignedTransportSwitchRejectsSameGenerationConflictAndTampering() throws {
        var client = DirectClientRecord(clientID: UUID().uuidString, displayName: "Workload", endpoint: "https://workload.example")
        client.pairingID = UUID().uuidString; client.generation = 1
        var value: [String: Any] = ["clientId": client.clientID, "pairingId": client.pairingID!, "generation": 7, "endpoint": client.endpoint, "transport": "hosted"]
        func bundle(_ data: Data) throws -> String {
            DirectBundle.encode(try JSONSerialization.data(withJSONObject: ["version": 2, "type": "mobile-egress-direct-endpoint-update", "payload": DirectBundle.encode(data), "signature": DirectBundle.encode(Data([1]))]))
        }
        let payload = try JSONSerialization.data(withJSONObject: value)
        let encoded = try bundle(payload)
        let verify: (Data, Data) -> Bool = { signed, _ in signed == Data("MobileEgress-Direct-Endpoint-v2\n".utf8) + payload }
        XCTAssertEqual(try DirectEndpointUpdate.parse(encoded, for: client, verify: verify).transport, .hosted)
        client.generation = 7
        XCTAssertThrowsError(try DirectEndpointUpdate.parse(encoded, for: client, verify: verify))
        client.transport = .hosted
        XCTAssertNoThrow(try DirectEndpointUpdate.parse(encoded, for: client, verify: verify))
        value["transport"] = "direct"
        XCTAssertThrowsError(try DirectEndpointUpdate.parse(bundle(JSONSerialization.data(withJSONObject: value)), for: client, verify: verify))
    }
    func testNativeCompletionRefundsBeforeRetainedCallbackReturnsAndIsIdempotent() {
        let budget = DirectPhoneBudget(frameLimit: 1, byteLimit: 100)
        let completed = budget.acquire(.outbound, bytes: 100, streamKey: "peer/stream")
        XCTAssertNotNil(completed)
        completed?.complete()
        let next = budget.acquire(.outbound, bytes: 100, streamKey: "peer/stream")
        XCTAssertNotNil(next)
        completed?.complete()
        XCTAssertNil(budget.acquire(.outbound, bytes: 1))
        withExtendedLifetime((completed, next)) {}
    }
    func testInvitationRejectsLegacyDuplicateFieldsAndExpiredCapability() throws {
        var object: [String: Any] = ["version": 2, "type": "mobile-egress-direct-invitation", "clientId": UUID().uuidString, "displayName": "Workload", "endpoint": "https://workload.example", "caCertificatePem": TestFixtures.validCAPEM, "invitationId": UUID().uuidString, "capability": DirectBundle.encode(Data(repeating: 5, count: 32)), "expiresAt": "2029-01-01T00:00:00Z", "role": "agent"]
        let encoded = DirectBundle.encode(try JSONSerialization.data(withJSONObject: object))
        XCTAssertEqual(try DirectInvitation.parse(encoded, now: TestFixtures.now).displayName, "Workload")
        object["version"] = 1
        XCTAssertThrowsError(try DirectInvitation.parse(DirectBundle.encode(try JSONSerialization.data(withJSONObject: object)), now: TestFixtures.now))
        object["version"] = 2
        object["expiresAt"] = "2020-01-01T00:00:00Z"
        XCTAssertThrowsError(try DirectInvitation.parse(DirectBundle.encode(try JSONSerialization.data(withJSONObject: object)), now: TestFixtures.now))
        XCTAssertThrowsError(try DirectInvitation.parse(encoded + "="))
    }

    func testSignedUpdateBindsExactPayloadPeerAndMonotonicGeneration() throws {
        var client = DirectClientRecord(clientID: "00c747f8-f6f1-4d52-9acf-3ccdc0a9bb42", displayName: "Workload", endpoint: "https://old.example")
        client.pairingID = "1b2b77c7-2e21-4cf3-9c23-42aab64bd69b"
        client.generation = 1
        let payload = Data("{\"clientId\":\"00c747f8-f6f1-4d52-9acf-3ccdc0a9bb42\",\"pairingId\":\"1b2b77c7-2e21-4cf3-9c23-42aab64bd69b\",\"generation\":9,\"endpoint\":\"https://new.example\"}".utf8)
        let bundle = DirectBundle.encode(try JSONSerialization.data(withJSONObject: ["version": 2, "type": "mobile-egress-direct-endpoint-update", "payload": DirectBundle.encode(payload), "signature": DirectBundle.encode(Data([1]))]))
        let update = try DirectEndpointUpdate.parse(bundle, for: client) { signed, signature in
            signed == Data("MobileEgress-Direct-Endpoint-v2\n".utf8) + payload && signature == Data([1])
        }
        XCTAssertEqual(update.generation, 9)
        XCTAssertEqual(update.endpoint, "https://new.example")
        XCTAssertThrowsError(try DirectEndpointUpdate.parse(bundle, for: client) { _, _ in false })
        client.generation = 10
        XCTAssertThrowsError(try DirectEndpointUpdate.parse(bundle, for: client) { _, _ in true })
        client.generation = 9
        XCTAssertThrowsError(try DirectEndpointUpdate.parse(bundle, for: client) { _, _ in true })
        client.endpoint = "https://new.example"
        XCTAssertNoThrow(try DirectEndpointUpdate.parse(bundle, for: client) { _, _ in true })
        client.pairingID = UUID().uuidString
        XCTAssertThrowsError(try DirectEndpointUpdate.parse(bundle, for: client) { _, _ in true })
    }

    func testSharedBudgetBorrowsUnusedCapacityAndSeparatesDirections() throws {
        let budget = DirectPhoneBudget(frameLimit: 2, byteLimit: 100, controlLimit: 2)
        var first: DirectBudgetLease? = budget.acquire(.outbound, bytes: 60)
        XCTAssertNotNil(first)
        XCTAssertNil(budget.acquire(.outbound, bytes: 60))
        let second = budget.acquire(.outbound, bytes: 40)
        XCTAssertNotNil(second)
        XCTAssertNil(budget.acquire(.outbound, bytes: 1))
        first = nil
        XCTAssertNotNil(budget.acquire(.outbound, bytes: 60))
        XCTAssertNotNil(budget.acquire(.inbound, bytes: 100))
        withExtendedLifetime(second) {}
    }

    func testMailboxCloseCannotRefundFrameHeldByNativeSender() throws {
        let budget = DirectPhoneBudget(frameLimit: 1, byteLimit: 100, controlLimit: 2)
        let first = OutboundMailbox(controlCapacity: 2, dataCapacity: 2, perStreamDataCapacity: 2, dataByteCapacity: 100, sharedBudget: budget)
        let second = OutboundMailbox(controlCapacity: 2, dataCapacity: 2, perStreamDataCapacity: 2, dataByteCapacity: 100, sharedBudget: budget)
        XCTAssertTrue(first.offerData(Data(repeating: 1, count: 100), streamID: "same-id"))
        var nativeFrame = first.poll()
        XCTAssertNotNil(nativeFrame)
        first.close()
        XCTAssertFalse(second.offerData(Data([2]), streamID: "same-id"))
        nativeFrame = nil
        XCTAssertTrue(second.offerData(Data([2]), streamID: "same-id"))
        second.close()
    }
    func testCancelledNativeStreamDebtCannotBeBypassedByReusingItsID() {
        let budget = DirectPhoneBudget(frameLimit: 100, byteLimit: 4096)
        var held: [DirectBudgetLease] = []
        for _ in 0..<32 { if let lease = budget.acquire(.inbound, bytes: 1, streamKey: "peer/session/stream") { held.append(lease) } }
        XCTAssertEqual(held.count, 32)
        XCTAssertNil(budget.acquire(.inbound, bytes: 1, streamKey: "peer/session/stream"))
        XCTAssertNotNil(budget.acquire(.inbound, bytes: 1, streamKey: "other-peer/session/stream"))
        held.removeLast()
        XCTAssertNotNil(budget.acquire(.inbound, bytes: 1, streamKey: "peer/session/stream"))
        withExtendedLifetime(held) {}
    }
}
