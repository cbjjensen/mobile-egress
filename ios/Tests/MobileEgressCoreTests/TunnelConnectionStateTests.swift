import XCTest
@testable import MobileEgressCore

final class TunnelConnectionStateTests: XCTestCase {
    func testPendingStartWaitsForAndThenPreservesFiniteDisconnectFailure() {
        var state = TunnelConnectionStateReducer()

        XCTAssertEqual(
            state.startRequested(),
            TunnelConnectionPresentation(providerState: .starting, providerError: .none)
        )
        XCTAssertEqual(
            state.observe(.disconnected, disconnectError: nil),
            TunnelConnectionPresentation(providerState: .starting, providerError: .none)
        )
        XCTAssertEqual(
            state.observe(.disconnected, disconnectError: .identityUnavailable),
            TunnelConnectionPresentation(providerState: .failed, providerError: .identityUnavailable)
        )
        XCTAssertEqual(
            state.observe(.disconnected, disconnectError: nil),
            TunnelConnectionPresentation(providerState: .failed, providerError: .identityUnavailable)
        )
    }

    func testExplicitStopSuppressesLastDisconnectErrorAndReturnsToStopped() {
        var state = TunnelConnectionStateReducer()

        XCTAssertEqual(
            state.observe(.connected, disconnectError: nil),
            TunnelConnectionPresentation(providerState: .running, providerError: .none)
        )
        XCTAssertEqual(
            state.stopRequested(),
            TunnelConnectionPresentation(providerState: .stopping, providerError: .none)
        )
        XCTAssertEqual(
            state.observe(.disconnected, disconnectError: .runtimeUnavailable),
            TunnelConnectionPresentation(providerState: .stopped, providerError: .none)
        )
    }

    func testHistoricalErrorIsIgnoredButUnexpectedActiveDisconnectFailsFinite() {
        var state = TunnelConnectionStateReducer()

        XCTAssertEqual(
            state.observe(.disconnected, disconnectError: .invalidConfiguration),
            TunnelConnectionPresentation(providerState: .stopped, providerError: .none)
        )
        _ = state.observe(.connected, disconnectError: nil)
        XCTAssertEqual(
            state.observe(.disconnected, disconnectError: nil),
            TunnelConnectionPresentation(providerState: .failed, providerError: .runtimeUnavailable)
        )
    }

    func testPersistedOnDemandIntentSurfacesFailureAfterAppRelaunch() {
        var state = TunnelConnectionStateReducer()

        XCTAssertEqual(
            state.restorePersistentIntent(onDemandEnabled: true),
            TunnelConnectionPresentation(providerState: .starting, providerError: .none)
        )
        XCTAssertEqual(
            state.observe(.disconnected, disconnectError: .tunnelSettings),
            TunnelConnectionPresentation(providerState: .failed, providerError: .tunnelSettings)
        )
    }
}
