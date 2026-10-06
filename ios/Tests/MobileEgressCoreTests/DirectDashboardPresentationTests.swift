import XCTest
@testable import MobileEgressCore

final class DirectDashboardPresentationTests: XCTestCase {
    private func lifecycle(started: Bool = true) -> DirectSharingLifecycle {
        var value = DirectSharingLifecycle()
        value.sceneActive = true; value.migrationReady = true
        if started { try! value.start() }
        return value
    }
    private func client(_ id: String = "one", state: DirectDashboardConnection = .waiting) -> DirectDashboardClient {
        DirectDashboardClient(id: id, name: "My computer", transport: .hosted, connection: state)
    }
    private func present(_ clients: [DirectDashboardClient], started: Bool = true, cellular: DirectCellularAvailability = .available, busy: Bool = false) -> DirectDashboardPresentation {
        .init(clients: clients, lifecycle: lifecycle(started: started), cellular: cellular, busy: busy)
    }
    func testEmptyInstallationInvitesPairingAndCannotStart() {
        let view = present([], started: false)
        XCTAssertTrue(view.isEmpty); XCTAssertFalse(view.canToggleSharing)
        XCTAssertEqual(view.headline, "Pair your first computer")
    }
    func testStartingDoesNotClaimConnection() {
        let view = present([client()])
        XCTAssertEqual(view.state, .connecting); XCTAssertEqual(view.connectedCount, 0)
        XCTAssertEqual(view.clients[0].status, "Connecting")
    }
    func testAuthenticatedSessionAndAggregateActivity() {
        var first = client(state: .authenticated)
        first.streams = 3; first.uploaded = 1_024; first.downloaded = 9_000
        var second = client("two", state: .authenticated)
        second.streams = 2; second.uploaded = 2_048; second.downloaded = 7_000
        let view = present([first, second])
        XCTAssertEqual(view.state, .connected); XCTAssertEqual(view.connectedCount, 2)
        XCTAssertEqual(view.streams, 5); XCTAssertEqual(view.uploaded, 3_072); XCTAssertEqual(view.downloaded, 16_000)
    }
    func testStoppedAndInactiveNeverShowStaleConnectedSessions() {
        let connected = client(state: .authenticated)
        XCTAssertEqual(present([connected], started: false).state, .stopped)
        var paused = lifecycle(); paused.sceneActive = false
        let view = DirectDashboardPresentation(clients: [connected], lifecycle: paused, cellular: .available)
        XCTAssertEqual(view.state, .paused); XCTAssertEqual(view.connectedCount, 0)
        XCTAssertEqual(view.streams, 0); XCTAssertTrue(view.canToggleSharing)
    }
    func testMixedSuccessKeepsOtherClientFailureVisible() {
        let view = present([client(state: .authenticated), client("two", state: .recovering(.pairingRequired))])
        XCTAssertEqual(view.state, .partial); XCTAssertEqual(view.connectedCount, 1)
        XCTAssertNotNil(view.clients[1].guidance); XCTAssertEqual(view.clients[1].tone, .warning)
    }
    func testPendingDisabledAndRemovingHaveDistinctActions() {
        var pending = client(); pending.paired = false; pending.needsAcknowledgement = true
        var disabled = client("two", state: .authenticated); disabled.enabled = false
        var removing = client("three", state: .authenticated); removing.removing = true
        let view = present([pending, disabled, removing], started: false)
        XCTAssertEqual(view.clients.map(\.status), ["Finish pairing", "Disabled", "Removal pending"])
        XCTAssertTrue(view.clients[0].canRetry); XCTAssertFalse(view.clients[1].canRetry)
        XCTAssertFalse(view.clients[2].canRetry); XCTAssertFalse(view.clients[2].canEnable)
        XCTAssertNotNil(view.clients[2].guidance); XCTAssertEqual(view.connectedCount, 0)
    }
    func testCellularLossAndRotationSuppressStaleConnectedState() {
        let clients = [client(state: .authenticated)]
        let view = present(clients, cellular: .unavailable)
        XCTAssertEqual(view.state, .attention); XCTAssertEqual(view.connectedCount, 0)
        XCTAssertEqual(view.clients[0].status, "Waiting for cellular")
        var rotating = lifecycle(); rotating.rotationPaused = true
        let rotation = DirectDashboardPresentation(clients: clients, lifecycle: rotating, cellular: .available)
        XCTAssertEqual(rotation.state, .paused); XCTAssertEqual(rotation.connectedCount, 0)
    }
    func testStopRemainsAvailableWhenBusyAndStartDoesNot() {
        XCTAssertTrue(present([client()], busy: true).canToggleSharing)
        XCTAssertFalse(present([client()], started: false, busy: true).canToggleSharing)
        XCTAssertFalse(present([client()], busy: true).clients[0].canRetry)
    }
    func testRecoveryAndPendingPairingNeedAttentionWithoutFalseSuccess() {
        for recovery in [DirectPeerRecovery.retrying, .clientStorageUnavailable, .unlockRequired, .pairingRequired, .clockCheckRequired] {
            let view = present([client(state: .recovering(recovery))])
            XCTAssertEqual(view.state, .attention); XCTAssertEqual(view.connectedCount, 0)
            XCTAssertEqual(view.clients[0].guidance, recovery.guidance)
        }
        var pending = client(); pending.paired = false
        XCTAssertEqual(present([pending]).state, .attention)
    }
    func testActivityExcludesDisabledClientsAndSaturatesRatherThanOverflowing() {
        var first = client(state: .authenticated); first.uploaded = .max; first.streams = .max
        var second = client("two", state: .authenticated); second.uploaded = 1; second.streams = 1
        var disabled = client("three"); disabled.enabled = false; disabled.downloaded = 42
        let view = present([first, second, disabled])
        XCTAssertEqual(view.uploaded, .max); XCTAssertEqual(view.streams, .max); XCTAssertEqual(view.downloaded, 0)
    }
    func testImportCallbackDeliveredOncePerAttemptAndEmptyInputDoesNotConsumeIt() {
        var delivery = ClientCodeDelivery()
        var received: [String] = []
        for code in ["  ", "first", "duplicate"] { if let accepted = delivery.take(code) { received.append(accepted) } }
        XCTAssertEqual(received, ["first"])
        delivery = ClientCodeDelivery()
        XCTAssertEqual(delivery.take(" retry "), "retry")
    }
    func testClientActionErrorRemainsVisibleAfterStopping() {
        var failed = client(); failed.actionError = "Client preference could not be saved."
        let view = present([failed, client("two")], started: false)
        XCTAssertEqual(view.clients[0].guidance, failed.actionError)
        XCTAssertNil(view.clients[1].guidance)
    }
    func testUpdateSheetCannotRouteInvitationsAndKeepsSelectedIdentity() throws {
        let destination = ClientCodeImportDestination.connectionUpdate(clientID: "selected-client")
        XCTAssertThrowsError(try destination.route(isInvitation: true))
        XCTAssertEqual(try destination.route(isInvitation: false), .update(expectedClientID: "selected-client"))
        XCTAssertThrowsError(try ClientCodeImportDestination.connectionUpdate(clientID: nil).route(isInvitation: true))
    }
    func testGeneralAddSheetRetainsExistingInvitationAndUpdateSupport() throws {
        XCTAssertEqual(try ClientCodeImportDestination.addClient.route(isInvitation: true), .pair)
        XCTAssertEqual(try ClientCodeImportDestination.addClient.route(isInvitation: false), .update(expectedClientID: nil))
    }
}
