import Foundation
import XCTest
@testable import MobileEgressCore

final class DirectAgentTests: XCTestCase {
    func testForegroundSharingKeepsAwakeDuringRetryAndStopsOnEveryExit() throws {
        var state = DirectSharingLifecycle()
        XCTAssertTrue(state.keepAwake)
        state.sceneActive = true
        XCTAssertThrowsError(try state.start())
        XCTAssertFalse(state.idleTimerDisabled)
        state.migrationReady = true
        try state.start()
        XCTAssertTrue(state.shouldServe)
        XCTAssertTrue(state.idleTimerDisabled)
        state.sceneActive = false
        XCTAssertFalse(state.shouldServe)
        XCTAssertFalse(state.idleTimerDisabled)
        XCTAssertTrue(state.startIntent)
        state.sceneActive = true
        XCTAssertTrue(state.shouldServe)
        state.keepAwake = false
        XCTAssertFalse(state.idleTimerDisabled)
        state.keepAwake = true
        state.stop()
        XCTAssertFalse(state.shouldServe)
        XCTAssertFalse(state.idleTimerDisabled)
        state.sceneActive = false
        state.sceneActive = true
        XCTAssertFalse(state.shouldServe)
    }

    func testTerminalFailureClearsIntentButRotationPreservesIt() throws {
        var state = DirectSharingLifecycle()
        state.sceneActive = true
        state.migrationReady = true
        try state.start()
        state.rotationPaused = true
        XCTAssertFalse(state.shouldServe)
        XCTAssertTrue(state.startIntent)
        state.rotationPaused = false
        XCTAssertTrue(state.shouldServe)
        state.terminalFailure()
        XCTAssertFalse(state.startIntent)
        XCTAssertFalse(state.idleTimerDisabled)
    }

    func testRegistryCountsPendingAndDisabledPeersAndRejectsEleventhWithoutMutation() throws {
        var registry = DirectRegistryDocument()
        for index in 0..<10 {
            try registry.insert(DirectClientRecord(clientID: UUID().uuidString.lowercased(), displayName: "Client \(index)", endpoint: "https://client.example", enabled: false))
        }
        let before = registry
        XCTAssertThrowsError(try registry.insert(DirectClientRecord(clientID: UUID().uuidString.lowercased(), displayName: "Overflow", endpoint: "https://client.example")))
        XCTAssertEqual(registry, before)
        XCTAssertThrowsError(try registry.insert(registry.clients[0]))
    }

    func testDirectSessionUsesNewPathAndMandatoryProtocolHeader() throws {
        let identity = AgentIdentity(relayOrigin: "https://workload.example:8443", role: "agent", serial: "A", keyTag: "key", certificatePEM: "leaf", caCertificatePEM: "ca", caCertificateDER: Data([1]))
        let configuration = try RelayWebSocketConfiguration(directIdentity: identity)
        XCTAssertEqual(configuration.url.absoluteString, "wss://workload.example:8443/v2/direct/session?transport=2")
        XCTAssertEqual(configuration.additionalHeaders["X-Mobile-Egress-Protocol"], "direct/1")
    }
}
