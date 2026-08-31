import XCTest
@testable import MobileEgressCore

final class TunnelConnectionStateTests: XCTestCase {
    func testSuspendedHistoricalDisconnectLookupCannotCrossCurrentAttemptRevision() async {
        let state = await MainActor.run { MainActorConnectionStateHarness() }
        let lookup = SuspendedDisconnectLookup()
        let historicalToken = await state.observationToken
        let lookupTask = Task { await lookup.fetch() }

        await lookup.waitUntilSuspended()
        let startToken = await state.startRequested()
        XCTAssertNotEqual(historicalToken, startToken)
        let connectingPresentation = await state.observe(
            .connecting,
            disconnectError: nil,
            matching: startToken
        )
        XCTAssertEqual(
            connectingPresentation,
            TunnelConnectionPresentation(providerState: .starting, providerError: .none)
        )

        await lookup.complete(with: .identityUnavailable)
        let historicalError = await lookupTask.value
        let stalePresentation = await state.observe(
            .disconnected,
            disconnectError: historicalError,
            matching: historicalToken
        )
        XCTAssertNil(stalePresentation)
        let currentPresentation = await state.presentation
        XCTAssertEqual(
            currentPresentation,
            TunnelConnectionPresentation(providerState: .starting, providerError: .none)
        )
    }

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

@MainActor
private final class MainActorConnectionStateHarness {
    private var state = TunnelConnectionStateReducer()

    var observationToken: TunnelConnectionObservationToken {
        state.observationToken
    }

    var presentation: TunnelConnectionPresentation {
        state.presentation
    }

    func startRequested() -> TunnelConnectionObservationToken {
        _ = state.startRequested()
        return state.observationToken
    }

    func observe(
        _ phase: TunnelConnectionPhase,
        disconnectError: TunnelProviderErrorClass?,
        matching token: TunnelConnectionObservationToken
    ) -> TunnelConnectionPresentation? {
        state.observe(phase, disconnectError: disconnectError, matching: token)
    }
}

private actor SuspendedDisconnectLookup {
    private var didSuspend = false
    private var resultContinuation: CheckedContinuation<TunnelProviderErrorClass?, Never>?
    private var suspensionWaiters: [CheckedContinuation<Void, Never>] = []

    func fetch() async -> TunnelProviderErrorClass? {
        didSuspend = true
        let waiters = suspensionWaiters
        suspensionWaiters.removeAll()
        for waiter in waiters {
            waiter.resume()
        }
        return await withCheckedContinuation { continuation in
            resultContinuation = continuation
        }
    }

    func waitUntilSuspended() async {
        guard !didSuspend else { return }
        await withCheckedContinuation { continuation in
            suspensionWaiters.append(continuation)
        }
    }

    func complete(with result: TunnelProviderErrorClass?) {
        resultContinuation?.resume(returning: result)
        resultContinuation = nil
    }
}
