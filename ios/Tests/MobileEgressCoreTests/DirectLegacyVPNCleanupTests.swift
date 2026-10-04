import Foundation
import XCTest
@testable import MobileEgressCore

final class DirectLegacyVPNCleanupTests: XCTestCase {
    @MainActor func testCleanupRemovesEveryOwnedProfileAndLeavesOtherVPNsUntouched() async throws {
        let store = ProfileStore()
        store.profiles = [.init(id: "one", providerIdentifier: "owned"), .init(id: "other", providerIdentifier: "different"), .init(id: "two", providerIdentifier: "owned")]
        try await DirectLegacyVPNCleanup.run(providerIdentifier: "owned", store: store)
        XCTAssertEqual(store.profiles.map(\.id), ["other"])
        XCTAssertEqual(store.disabled, ["one", "two"])
        XCTAssertEqual(store.stopped, ["one", "two"])
    }
    @MainActor func testFailureOrUnconfirmedRemovalBlocksMigrationAndStillStopsOwnedSession() async throws {
        for failure in ["disable", "disconnect", "remove", "unconfirmed"] {
            let store = ProfileStore(); store.failure = failure
            store.profiles = [.init(id: "one", providerIdentifier: "owned")]
            do { try await DirectLegacyVPNCleanup.run(providerIdentifier: "owned", store: store); XCTFail("must block Start after \(failure)") } catch {}
            XCTAssertEqual(store.stopped, ["one"])
        }
    }
}
@MainActor private final class ProfileStore: DirectLegacyVPNProfileManaging {
    var profiles: [DirectLegacyVPNProfile] = []
    var disabled: [String] = []
    var stopped: [String] = []
    var failure = ""
    func listProfiles() async throws -> [DirectLegacyVPNProfile] { profiles }
    func disable(_ id: String) async throws { if failure == "disable" { throw DirectAgentError.persistence }; disabled.append(id) }
    func stop(_ id: String) { stopped.append(id) }
    func waitUntilStopped(_ id: String) async throws { if failure == "disconnect" { throw DirectAgentError.unavailable } }
    func remove(_ id: String) async throws {
        if failure == "remove" { throw DirectAgentError.persistence }
        if failure != "unconfirmed" { profiles.removeAll { $0.id == id } }
    }
}
