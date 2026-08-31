import XCTest
@testable import MobileEgressCore

final class TunnelConnectionStateTests: XCTestCase {
    func testNewStartIgnoresTheSameHistoricalDisconnectErrorUntilCurrentAttemptEvidence() {
        var state = TunnelConnectionStateReducer()

        XCTAssertEqual(
            state.observe(.disconnected, disconnectError: .identityUnavailable),
            TunnelConnectionPresentation(providerState: .stopped, providerError: .none)
        )
        XCTAssertEqual(
            state.startRequested(),
            TunnelConnectionPresentation(providerState: .starting, providerError: .none)
        )
        XCTAssertEqual(
            state.observe(.disconnected, disconnectError: .identityUnavailable),
            TunnelConnectionPresentation(providerState: .starting, providerError: .none)
        )
    }

    func testCurrentAttemptLifecycleEvidenceAllowsItsFiniteDisconnectFailure() {
        var state = TunnelConnectionStateReducer()

        _ = state.startRequested()
        XCTAssertEqual(
            state.observe(.connecting, disconnectError: nil),
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

    func testStartTransactionFailureDoesNotWaitForLifecycleEvidence() {
        var state = TunnelConnectionStateReducer()

        _ = state.startRequested()
        XCTAssertEqual(
            state.startTransactionFailed(.runtimeUnavailable),
            TunnelConnectionPresentation(providerState: .failed, providerError: .runtimeUnavailable)
        )
    }

    func testCurrentAttemptDisconnectWithoutProviderErrorFailsFinite() {
        var state = TunnelConnectionStateReducer()

        _ = state.startRequested()
        _ = state.observe(.connecting, disconnectError: nil)
        _ = state.observe(.disconnecting, disconnectError: nil)
        XCTAssertEqual(
            state.observe(.disconnected, disconnectError: nil),
            TunnelConnectionPresentation(providerState: .failed, providerError: .runtimeUnavailable)
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
            state.stopTransactionCompleted(persistenceSucceeded: true),
            TunnelConnectionPresentation(providerState: .stopping, providerError: .none)
        )
        XCTAssertEqual(
            state.observe(.disconnected, disconnectError: .runtimeUnavailable),
            TunnelConnectionPresentation(providerState: .stopped, providerError: .none)
        )
    }

    func testFailedStopPersistenceReleasesSuppressionBeforeOnDemandReconnect() {
        var state = TunnelConnectionStateReducer()

        _ = state.observe(.connected, disconnectError: nil)
        _ = state.stopRequested()
        XCTAssertEqual(
            state.stopTransactionCompleted(persistenceSucceeded: false),
            TunnelConnectionPresentation(providerState: .stopping, providerError: .none)
        )
        XCTAssertEqual(
            state.observe(.connected, disconnectError: nil),
            TunnelConnectionPresentation(providerState: .running, providerError: .none)
        )
        XCTAssertEqual(
            state.observe(.reasserting, disconnectError: nil),
            TunnelConnectionPresentation(providerState: .starting, providerError: .none)
        )
        XCTAssertEqual(
            state.observe(.disconnected, disconnectError: .tunnelSettings),
            TunnelConnectionPresentation(providerState: .failed, providerError: .tunnelSettings)
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
