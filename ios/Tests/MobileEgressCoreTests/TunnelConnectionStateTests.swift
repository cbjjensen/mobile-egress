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

    func testSuspendedDisconnectLookupCannotCrossAcceptedConnectedNoOpObservation() async {
        let state = await MainActor.run { MainActorConnectionStateHarness() }
        let initialToken = await state.observationToken
        _ = await state.observe(.connected, disconnectError: nil, matching: initialToken)
        let lookup = SuspendedDisconnectLookup()
        let lookupToken = await state.observationToken
        let lookupTask = Task { await lookup.fetch() }

        await lookup.waitUntilSuspended()
        let repeatedConnected = await state.observe(
            .connected,
            disconnectError: nil,
            matching: lookupToken
        )
        XCTAssertEqual(
            repeatedConnected,
            TunnelConnectionPresentation(providerState: .running, providerError: .none)
        )
        let boundaryToken = await state.observationToken
        XCTAssertNotEqual(lookupToken, boundaryToken)

        await lookup.complete(with: .identityUnavailable)
        let staleError = await lookupTask.value
        let stalePresentation = await state.observe(
            .disconnected,
            disconnectError: staleError,
            matching: lookupToken
        )
        XCTAssertNil(stalePresentation)
        let currentToken = await state.observationToken
        let currentPresentation = await state.presentation
        XCTAssertEqual(currentToken, boundaryToken)
        XCTAssertEqual(
            currentPresentation,
            TunnelConnectionPresentation(providerState: .running, providerError: .none)
        )
    }

    func testSuspendedProviderStatusCannotCrossAcceptedConnectedNoOpObservation() async {
        let state = await MainActor.run { MainActorConnectionStateHarness() }
        let initialToken = await state.observationToken
        _ = await state.observe(.connected, disconnectError: nil, matching: initialToken)
        let providerRequest = SuspendedProviderStatusRequest()
        let providerToken = await state.observationToken
        let providerTask = Task { await providerRequest.fetch() }

        await providerRequest.waitUntilSuspended()
        let repeatedConnected = await state.observe(
            .connected,
            disconnectError: nil,
            matching: providerToken
        )
        XCTAssertEqual(
            repeatedConnected,
            TunnelConnectionPresentation(providerState: .running, providerError: .none)
        )

        await providerRequest.complete(with: staleProviderStatus)
        let staleStatus = await providerTask.value
        XCTAssertEqual(staleStatus.providerState, .failed)
        let providerTokenIsCurrent = await state.isCurrent(providerToken)
        let currentPresentation = await state.presentation
        XCTAssertFalse(providerTokenIsCurrent)
        XCTAssertEqual(
            currentPresentation,
            TunnelConnectionPresentation(providerState: .running, providerError: .none)
        )
    }

    func testEveryCurrentLifecycleObservationAdvancesTheTemporalBoundaryIncludingNoOps() {
        var idle = TunnelConnectionStateReducer()
        assertAcceptedObservation(
            .disconnected,
            disconnectError: nil,
            state: &idle,
            expected: TunnelConnectionPresentation(providerState: .stopped, providerError: .none)
        )

        var failed = TunnelConnectionStateReducer()
        _ = failed.startRequested()
        _ = failed.startTransactionFailed(.runtimeUnavailable)
        assertAcceptedObservation(
            .invalid,
            disconnectError: nil,
            state: &failed,
            expected: TunnelConnectionPresentation(
                providerState: .failed,
                providerError: .runtimeUnavailable
            )
        )

        var pendingStart = TunnelConnectionStateReducer()
        _ = pendingStart.startRequested()
        let pending = TunnelConnectionPresentation(providerState: .starting, providerError: .none)
        assertAcceptedObservation(
            .disconnected,
            disconnectError: .identityUnavailable,
            state: &pendingStart,
            expected: pending
        )
        assertAcceptedObservation(
            .disconnecting,
            disconnectError: nil,
            state: &pendingStart,
            expected: pending
        )
        assertAcceptedObservation(
            .connecting,
            disconnectError: nil,
            state: &pendingStart,
            expected: pending
        )
        assertAcceptedObservation(
            .connecting,
            disconnectError: nil,
            state: &pendingStart,
            expected: pending
        )
        assertAcceptedObservation(
            .reasserting,
            disconnectError: nil,
            state: &pendingStart,
            expected: pending
        )
        assertAcceptedObservation(
            .reasserting,
            disconnectError: nil,
            state: &pendingStart,
            expected: pending
        )
        let stopping = TunnelConnectionPresentation(providerState: .stopping, providerError: .none)
        assertAcceptedObservation(
            .disconnecting,
            disconnectError: nil,
            state: &pendingStart,
            expected: stopping
        )
        assertAcceptedObservation(
            .disconnecting,
            disconnectError: nil,
            state: &pendingStart,
            expected: stopping
        )

        var active = TunnelConnectionStateReducer()
        _ = active.observe(.connected, disconnectError: nil)
        assertAcceptedObservation(
            .connected,
            disconnectError: nil,
            state: &active,
            expected: TunnelConnectionPresentation(providerState: .running, providerError: .none)
        )

        var explicitStop = TunnelConnectionStateReducer()
        _ = explicitStop.observe(.connected, disconnectError: nil)
        _ = explicitStop.stopRequested()
        let explicitlyStopping = TunnelConnectionPresentation(
            providerState: .stopping,
            providerError: .none
        )
        for phase in [
            TunnelConnectionPhase.connecting,
            .connected,
            .reasserting,
        ] {
            assertAcceptedObservation(
                phase,
                disconnectError: nil,
                state: &explicitStop,
                expected: explicitlyStopping
            )
        }
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

    func testPersistedOnDemandIntentStaysCancelableAcrossDisconnectedPolling() {
        var state = TunnelConnectionStateReducer()

        XCTAssertEqual(
            state.restorePersistentIntent(onDemandEnabled: true),
            TunnelConnectionPresentation(providerState: .starting, providerError: .none)
        )
        XCTAssertEqual(
            state.observe(.disconnected, disconnectError: nil),
            TunnelConnectionPresentation(providerState: .starting, providerError: .none)
        )
        let command = TunnelCommandDecision.resolve(
            providerState: state.presentation.providerState,
            connectionPhase: .disconnected
        )
        XCTAssertEqual(command.command, .stop)
        XCTAssertTrue(command.isEnabled)
    }

    func testPersistedOnDemandIntentSurfacesFiniteFailureAfterAppRelaunch() {
        var state = TunnelConnectionStateReducer()

        _ = state.restorePersistentIntent(onDemandEnabled: true)
        XCTAssertEqual(
            state.observe(.disconnected, disconnectError: .tunnelSettings),
            TunnelConnectionPresentation(providerState: .failed, providerError: .tunnelSettings)
        )
    }

    func testPersistedOnDemandIntentUsesConnectionEvidenceForLaterNilDisconnect() {
        var state = TunnelConnectionStateReducer()

        _ = state.restorePersistentIntent(onDemandEnabled: true)
        XCTAssertEqual(
            state.observe(.connecting, disconnectError: nil),
            TunnelConnectionPresentation(providerState: .starting, providerError: .none)
        )
        XCTAssertEqual(
            state.observe(.disconnected, disconnectError: nil),
            TunnelConnectionPresentation(providerState: .failed, providerError: .runtimeUnavailable)
        )
    }

    func testPersistedOnDemandIntentCanRunReassertAndLaterSurfaceFiniteFailure() {
        var state = TunnelConnectionStateReducer()

        _ = state.restorePersistentIntent(onDemandEnabled: true)
        XCTAssertEqual(
            state.observe(.connected, disconnectError: nil),
            TunnelConnectionPresentation(providerState: .running, providerError: .none)
        )
        XCTAssertEqual(
            state.observe(.reasserting, disconnectError: nil),
            TunnelConnectionPresentation(providerState: .starting, providerError: .none)
        )
        XCTAssertEqual(
            state.observe(.disconnected, disconnectError: .identityUnavailable),
            TunnelConnectionPresentation(providerState: .failed, providerError: .identityUnavailable)
        )
    }

    func testExplicitStopFromPendingPersistedIntentSuppressesItsDisconnect() {
        var state = TunnelConnectionStateReducer()

        _ = state.restorePersistentIntent(onDemandEnabled: true)
        _ = state.stopRequested()
        _ = state.stopTransactionCompleted(persistenceSucceeded: true)
        XCTAssertEqual(
            state.observe(.disconnected, disconnectError: .runtimeUnavailable),
            TunnelConnectionPresentation(providerState: .stopped, providerError: .none)
        )
    }

    func testFailedStopPersistenceFromPendingPersistedIntentReleasesSuppression() {
        var state = TunnelConnectionStateReducer()

        _ = state.restorePersistentIntent(onDemandEnabled: true)
        _ = state.stopRequested()
        _ = state.stopTransactionCompleted(persistenceSucceeded: false)
        XCTAssertEqual(
            state.observe(.disconnected, disconnectError: .tunnelSettings),
            TunnelConnectionPresentation(providerState: .failed, providerError: .tunnelSettings)
        )
    }

    private func assertAcceptedObservation(
        _ phase: TunnelConnectionPhase,
        disconnectError: TunnelProviderErrorClass?,
        state: inout TunnelConnectionStateReducer,
        expected: TunnelConnectionPresentation,
        file: StaticString = #filePath,
        line: UInt = #line
    ) {
        let token = state.observationToken
        XCTAssertEqual(
            state.observe(phase, disconnectError: disconnectError, matching: token),
            expected,
            file: file,
            line: line
        )
        XCTAssertFalse(state.isCurrent(token), file: file, line: line)
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

    func isCurrent(_ token: TunnelConnectionObservationToken) -> Bool {
        state.isCurrent(token)
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

private actor SuspendedProviderStatusRequest {
    private var didSuspend = false
    private var resultContinuation: CheckedContinuation<TunnelProviderStatus, Never>?
    private var suspensionWaiters: [CheckedContinuation<Void, Never>] = []

    func fetch() async -> TunnelProviderStatus {
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

    func complete(with result: TunnelProviderStatus) {
        resultContinuation?.resume(returning: result)
        resultContinuation = nil
    }
}

private let staleProviderStatus = TunnelProviderStatus(
    providerState: .failed,
    runtimeSnapshot: AgentRuntimeSnapshot(
        connectionState: .stopped,
        activeStreamCount: 0,
        bytesUploaded: 0,
        bytesDownloaded: 0,
        errorClass: .none
    ),
    providerError: .runtimeUnavailable
)
