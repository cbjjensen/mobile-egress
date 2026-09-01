import Foundation

public protocol CellularIPRotationClock: Sendable {
    func currentDate() -> Date
}

public struct SystemCellularIPRotationClock: CellularIPRotationClock {
    public init() {}

    public func currentDate() -> Date { Date() }
}

public protocol CellularIPRotationSleeping: Sendable {
    func sleep(seconds: Int) async throws
}

public struct ContinuousCellularIPRotationSleeper: CellularIPRotationSleeping {
    public init() {}

    public func sleep(seconds: Int) async throws {
        try await Task.sleep(for: .seconds(seconds))
    }
}

@MainActor
public protocol CellularIPRotationTunnelControlling: AnyObject, Sendable {
    associatedtype RotationReceipt: Sendable

    func captureRotationIntent() async throws -> RotationReceipt
    func pauseForRotation(using receipt: RotationReceipt) async throws
    func resumeAfterRotation(_ receipt: RotationReceipt?) async throws
}

@MainActor
public final class CellularIPRotationCoordinator<
    Tunnel: CellularIPRotationTunnelControlling
> where Tunnel.RotationReceipt == TunnelRotationReceipt {
    public typealias StateChangeHandler = @MainActor @Sendable (CellularIPRotationState) -> Void
    public typealias CellularChangeHandler = @MainActor @Sendable (Bool) -> Void

    public private(set) var state: CellularIPRotationState = .idle
    public private(set) var isCellularAvailable = false

    private let clock: any CellularIPRotationClock
    private let sleeper: any CellularIPRotationSleeping
    private let probe: any CellularPublicIPProbing
    private let pathObserver: any CellularPathObserving
    private let checkpointStore: any CellularIPRotationCheckpointStoring
    private let notificationCue: any CellularIPRotationNotificationCueing
    private let tunnel: Tunnel

    private var reducer = CellularIPRotationReducer()
    private var isEnrolled = false
    private var isAgentRunning = false
    private var activeStreamCount = 0
    private var pathObservationStarted = false
    private var lastCellularAvailability: Bool?
    private var recoveryAttempted = false
    private var nextAttemptID: UInt64 = 0
    private var cellularGeneration: UInt64 = 0
    private var currentNetworkToken: String?
    private var attemptGeneration: UInt64 = 0
    private var recoveringAttemptID: UInt64?
    private var recoveredPauseAlreadyCompleted = false
    private var requiresTerminalTunnelResume = false
    private var checkpointPauseDisposition: CellularIPRotationPauseDisposition = .legacyUnknown
    private var pendingRetirementFailureAttemptID: UInt64?
    private var timeoutDeadline: Date?
    private var pauseReceipt: TunnelRotationReceipt?
    private var pendingTerminalOutcome: CellularIPRotationTerminalOutcome?
    private var stateChangeHandler: StateChangeHandler?
    private var cellularChangeHandler: CellularChangeHandler?

    private var pauseTask: Task<Void, Never>?
    private var probeTask: Task<Void, Never>?
    private var lossTimeoutTask: Task<Void, Never>?
    private var returnTimeoutTask: Task<Void, Never>?
    private var holdTask: Task<Void, Never>?
    private var resumeTask: Task<Void, Never>?

    public init(
        clock: any CellularIPRotationClock = SystemCellularIPRotationClock(),
        sleeper: any CellularIPRotationSleeping = ContinuousCellularIPRotationSleeper(),
        probe: any CellularPublicIPProbing,
        pathObserver: any CellularPathObserving,
        checkpointStore: any CellularIPRotationCheckpointStoring,
        notificationCue: any CellularIPRotationNotificationCueing,
        tunnel: Tunnel
    ) {
        self.clock = clock
        self.sleeper = sleeper
        self.probe = probe
        self.pathObserver = pathObserver
        self.checkpointStore = checkpointStore
        self.notificationCue = notificationCue
        self.tunnel = tunnel
    }

    public func setStateChangeHandler(_ handler: StateChangeHandler?) {
        stateChangeHandler = handler
        handler?(state)
    }

    public func setCellularChangeHandler(_ handler: CellularChangeHandler?) {
        cellularChangeHandler = handler
        handler?(isCellularAvailable)
    }

    public func updateAgentAvailability(
        isEnrolled: Bool,
        isAgentRunning: Bool,
        activeStreamCount: Int
    ) {
        self.isEnrolled = isEnrolled
        self.isAgentRunning = isAgentRunning
        self.activeStreamCount = max(0, activeStreamCount)
    }

    public func start(holdSeconds: Int) async {
        let expectedHoldSeconds: Int
        if case .completed(_, _, _, .unchanged) = state {
            expectedHoldSeconds = 30
        } else {
            expectedHoldSeconds = 10
        }
        let availability = CellularIPRotationAvailability(
            isEnrolled: isEnrolled,
            isAgentRunning: isAgentRunning,
            isCellularAvailable: isCellularAvailable,
            activeStreamCount: activeStreamCount
        )
        guard pendingTerminalOutcome == nil,
              !state.requiresRecoveryReconstruction,
              holdSeconds == expectedHoldSeconds,
              availability.isEligible(for: state),
              let currentNetworkToken else { return }

        nextAttemptID = max(nextAttemptID, state.attemptID ?? 0)
        nextAttemptID &+= 1
        attemptGeneration &+= 1
        cancelOwnedTasks()
        pauseReceipt = nil
        recoveringAttemptID = nil
        recoveredPauseAlreadyCompleted = false
        requiresTerminalTunnelResume = false
        checkpointPauseDisposition = .pending
        pendingRetirementFailureAttemptID = nil
        timeoutDeadline = nil
        pendingTerminalOutcome = nil
        await apply(
            .requested(
                attemptID: nextAttemptID,
                networkToken: currentNetworkToken,
                availability: availability
            ),
            expectedGeneration: attemptGeneration
        )
    }

    public func confirm(proceed: Bool) async {
        guard let attemptID = state.attemptID else { return }
        await apply(
            .confirmationDecided(attemptID: attemptID, proceed: proceed),
            expectedGeneration: attemptGeneration
        )
    }

    public func cancel() async {
        guard pendingTerminalOutcome == nil,
              state.isCancellable,
              let attemptID = state.attemptID else { return }
        let generation = attemptGeneration
        let pendingPause = pauseTask
        pendingPause?.cancel()
        await pendingPause?.value
        guard isCurrent(attemptID: attemptID, generation: generation) else { return }
        pauseTask = nil
        await apply(
            .cancelled(attemptID: attemptID),
            expectedGeneration: generation
        )
    }

    public func resumeAfterActivation() async {
        startPathObservationIfNeeded()
        cellularChangeHandler?(isCellularAvailable)
        stateChangeHandler?(state)
        guard !state.isActive, !recoveryAttempted else { return }
        recoveryAttempted = true

        do {
            guard let checkpoint = try checkpointStore.load(at: clock.currentDate()) else { return }
            guard let attemptID = checkpoint.state.attemptID else {
                try? checkpointStore.clear()
                await failRecovery()
                return
            }
            nextAttemptID = max(nextAttemptID, attemptID)
            attemptGeneration &+= 1
            cancelOwnedTasks()
            pauseReceipt = nil
            recoveringAttemptID = attemptID
            checkpointPauseDisposition = checkpoint.pauseDisposition
            pendingRetirementFailureAttemptID = nil
            pendingTerminalOutcome = nil
            configureRecoveredPauseOwnership(from: checkpoint)
            timeoutDeadline = checkpoint.timeoutDeadline
            if case let .restoring(_, outcome) = checkpoint.state {
                guard checkpoint.pauseDisposition.hasRestorationReceipt else {
                    try? checkpointStore.clear()
                    await failRecovery()
                    return
                }
                pendingTerminalOutcome = outcome
                state = checkpoint.state
                reducer = CellularIPRotationReducer(
                    initialState: outcome.state(attemptID: attemptID)
                )
                timeoutDeadline = nil
                stateChangeHandler?(state)
                await notificationCue.cancel(attemptID: attemptID)
                scheduleResume(attemptID: attemptID, generation: attemptGeneration)
                return
            }
            await apply(
                .recover(checkpoint: checkpoint, at: clock.currentDate()),
                expectedGeneration: attemptGeneration,
                preservedDeadline: checkpoint.timeoutDeadline
            )
        } catch {
            try? checkpointStore.clear()
            await failRecovery()
        }
    }

    private func failRecovery() async {
        nextAttemptID &+= 1
        attemptGeneration &+= 1
        cancelOwnedTasks()
        pauseReceipt = nil
        recoveringAttemptID = nextAttemptID
        recoveredPauseAlreadyCompleted = false
        requiresTerminalTunnelResume = true
        checkpointPauseDisposition = .legacyUnknown
        pendingRetirementFailureAttemptID = nil
        timeoutDeadline = nil
        pendingTerminalOutcome = nil
        await apply(
            .recoveryFailed(attemptID: nextAttemptID),
            expectedGeneration: attemptGeneration
        )
    }

    private func configureRecoveredPauseOwnership(
        from checkpoint: CellularIPRotationCheckpoint
    ) {
        switch checkpoint.pauseDisposition {
        case let .pausing(receipt):
            pauseReceipt = receipt
            recoveredPauseAlreadyCompleted = false
            requiresTerminalTunnelResume = true
        case let .paused(receipt):
            pauseReceipt = receipt
            recoveredPauseAlreadyCompleted = true
            requiresTerminalTunnelResume = true
        case .pending:
            let isPrePause = checkpoint.state.isPrePauseRecoveryState
            recoveredPauseAlreadyCompleted = false
            requiresTerminalTunnelResume = !isPrePause
        case .legacyUnknown:
            recoveredPauseAlreadyCompleted = false
            requiresTerminalTunnelResume = true
        }
    }

    private func startPathObservationIfNeeded() {
        guard !pathObservationStarted else { return }
        pathObservationStarted = true
        pathObserver.start { [weak self] available in
            Task { @MainActor [weak self] in
                await self?.cellularPathChanged(available)
            }
        }
    }

    private func cellularPathChanged(_ available: Bool) async {
        guard lastCellularAvailability != available else {
            cellularChangeHandler?(available)
            return
        }
        lastCellularAvailability = available
        isCellularAvailable = available
        if available {
            cellularGeneration &+= 1
            currentNetworkToken = "cellular-\(cellularGeneration)"
        }
        cellularChangeHandler?(available)
        guard pendingTerminalOutcome == nil,
              let attemptID = state.attemptID,
              state.isActive else { return }
        let event: CellularIPRotationEvent
        if available, let currentNetworkToken {
            event = .cellularAvailable(
                attemptID: attemptID,
                networkToken: currentNetworkToken
            )
        } else {
            event = .cellularLost(attemptID: attemptID)
        }
        await apply(event, expectedGeneration: attemptGeneration)
    }

    private func apply(
        _ event: CellularIPRotationEvent,
        expectedGeneration: UInt64,
        preservedDeadline: Date? = nil,
        skipCheckpointSave: Bool = false
    ) async {
        guard expectedGeneration == attemptGeneration else { return }
        let priorAttemptID = state.attemptID
        let transition = reducer.reduce(event)
        if let attemptID = transition.state.attemptID,
           let outcome = transition.state.terminalOutcome,
           transition.effects.contains(where: { effect in
               if case let .resumeAgent(effectAttemptID) = effect {
                   return effectAttemptID == attemptID
               }
               return false
           }) {
            await beginTerminalRestoration(
                attemptID: attemptID,
                outcome: outcome
            )
            return
        }
        state = transition.state
        updateDeadline(
            for: transition.effects,
            state: transition.state,
            preservedDeadline: preservedDeadline
        )
        stateChangeHandler?(state)

        if state.isActive, !skipCheckpointSave {
            do {
                try saveActiveCheckpoint()
            } catch {
                if let attemptID = state.attemptID {
                    await apply(
                        .cancelled(attemptID: attemptID),
                        expectedGeneration: attemptGeneration,
                        skipCheckpointSave: true
                    )
                }
                return
            }
        }

        let terminalAttemptID = state.isActive ? nil : (state.attemptID ?? priorAttemptID)
        if let terminalAttemptID {
            cancelOwnedTasks()
            attemptGeneration &+= 1
            timeoutDeadline = nil
            pendingRetirementFailureAttemptID = nil
            do {
                try checkpointStore.retire(attemptID: terminalAttemptID)
            } catch {
                pendingRetirementFailureAttemptID = terminalAttemptID
            }
            await notificationCue.cancel(attemptID: terminalAttemptID)
        }

        let effectGeneration = attemptGeneration
        var schedulesResume = false
        for effect in transition.effects {
            if case .resumeAgent = effect {
                schedulesResume = true
            }
            interpret(effect, generation: effectGeneration)
        }
        if let terminalAttemptID, !schedulesResume {
            reportRetirementFailureIfNeeded(attemptID: terminalAttemptID)
        }
    }

    private func saveActiveCheckpoint() throws {
        try checkpointStore.save(
            CellularIPRotationCheckpoint(
                state: state,
                savedAt: clock.currentDate(),
                timeoutDeadline: timeoutDeadline,
                pauseDisposition: checkpointPauseDisposition
            )
        )
    }

    private func beginTerminalRestoration(
        attemptID: UInt64,
        outcome: CellularIPRotationTerminalOutcome
    ) async {
        cancelOwnedTasks()
        attemptGeneration &+= 1
        timeoutDeadline = nil
        pendingRetirementFailureAttemptID = nil
        pendingTerminalOutcome = outcome
        state = .restoring(attemptID: attemptID, outcome: outcome)
        try? saveActiveCheckpoint()
        stateChangeHandler?(state)
        await notificationCue.cancel(attemptID: attemptID)
        scheduleResume(attemptID: attemptID, generation: attemptGeneration)
    }

    private func updateDeadline(
        for effects: [CellularIPRotationEffect],
        state: CellularIPRotationState,
        preservedDeadline: Date?
    ) {
        switch state {
        case .awaitingAirplaneMode, .awaitingCellularReturn:
            break
        case .idle, .awaitingConfirmation, .preparing, .holding, .verifying, .restoring,
             .completed, .failed:
            timeoutDeadline = nil
        }
        for effect in effects {
            switch effect {
            case let .scheduleCellularLossTimeout(_, seconds),
                 let .scheduleCellularReturnTimeout(_, seconds):
                timeoutDeadline = preservedDeadline
                    ?? clock.currentDate().addingTimeInterval(TimeInterval(seconds))
            case .cancelCellularLossTimeout, .cancelCellularReturnTimeout:
                timeoutDeadline = nil
            case .pauseAgentAndStreams, .probeBefore, .presentAirplaneModeGuidance,
                 .startHoldCountdown, .probeAfter, .resumeAgent:
                break
            }
        }
    }

    private func interpret(_ effect: CellularIPRotationEffect, generation: UInt64) {
        switch effect {
        case let .pauseAgentAndStreams(attemptID):
            guard recoveringAttemptID != attemptID || !recoveredPauseAlreadyCompleted else {
                return
            }
            schedulePause(attemptID: attemptID, generation: generation)
        case let .probeBefore(attemptID, _):
            scheduleProbe(attemptID: attemptID, isBefore: true, generation: generation)
        case .presentAirplaneModeGuidance:
            stateChangeHandler?(state)
        case let .scheduleCellularLossTimeout(attemptID, seconds):
            scheduleTimeout(
                attemptID: attemptID,
                seconds: seconds,
                isLossTimeout: true,
                generation: generation
            )
        case .cancelCellularLossTimeout:
            lossTimeoutTask?.cancel()
            lossTimeoutTask = nil
        case let .startHoldCountdown(attemptID, seconds):
            scheduleHold(attemptID: attemptID, seconds: seconds, generation: generation)
        case let .scheduleCellularReturnTimeout(attemptID, seconds):
            scheduleTimeout(
                attemptID: attemptID,
                seconds: seconds,
                isLossTimeout: false,
                generation: generation
            )
        case .cancelCellularReturnTimeout:
            returnTimeoutTask?.cancel()
            returnTimeoutTask = nil
        case let .probeAfter(attemptID, _):
            scheduleProbe(attemptID: attemptID, isBefore: false, generation: generation)
        case let .resumeAgent(attemptID):
            scheduleResume(attemptID: attemptID, generation: generation)
        }
    }

    private func schedulePause(attemptID: UInt64, generation: UInt64) {
        pauseTask?.cancel()
        pauseTask = Task { @MainActor [weak self] in
            guard let self else { return }
            let receipt: TunnelRotationReceipt
            let usesRecoveredPausingReceipt: Bool
            do {
                if self.recoveringAttemptID == attemptID,
                   case let .pausing(persistedReceipt) = self.checkpointPauseDisposition {
                    receipt = persistedReceipt
                    usesRecoveredPausingReceipt = true
                } else {
                    receipt = try await self.tunnel.captureRotationIntent()
                    usesRecoveredPausingReceipt = false
                    guard self.isCurrent(attemptID: attemptID, generation: generation) else {
                        return
                    }
                    self.pauseReceipt = receipt
                    self.checkpointPauseDisposition = .pausing(receipt)
                    self.requiresTerminalTunnelResume = true
                    try self.saveActiveCheckpoint()
                }
            } catch {
                guard self.isCurrent(attemptID: attemptID, generation: generation) else { return }
                self.requiresTerminalTunnelResume = false
                if Task.isCancelled { return }
                await self.apply(
                    .cancelled(attemptID: attemptID),
                    expectedGeneration: generation,
                    skipCheckpointSave: true
                )
                return
            }

            do {
                try await self.tunnel.pauseForRotation(using: receipt)
            } catch {
                guard self.isCurrent(attemptID: attemptID, generation: generation) else { return }
                self.requiresTerminalTunnelResume = usesRecoveredPausingReceipt
                if Task.isCancelled { return }
                await self.apply(
                    .cancelled(attemptID: attemptID),
                    expectedGeneration: generation
                )
                return
            }

            guard self.isCurrent(attemptID: attemptID, generation: generation) else { return }
            self.pauseReceipt = receipt
            self.checkpointPauseDisposition = .paused(receipt)
            self.requiresTerminalTunnelResume = true
            do {
                try self.saveActiveCheckpoint()
            } catch {
                guard self.isCurrent(attemptID: attemptID, generation: generation) else { return }
                await self.apply(
                    .cancelled(attemptID: attemptID),
                    expectedGeneration: generation,
                    skipCheckpointSave: true
                )
            }
        }
    }

    private func scheduleProbe(
        attemptID: UInt64,
        isBefore: Bool,
        generation: UInt64
    ) {
        probeTask?.cancel()
        let pauseDependency = pauseTask
        probeTask = Task { @MainActor [weak self] in
            await pauseDependency?.value
            guard let self,
                  !Task.isCancelled,
                  self.isCurrent(attemptID: attemptID, generation: generation) else { return }
            let snapshot = await self.probe.probe()
            guard !Task.isCancelled,
                  self.isCurrent(attemptID: attemptID, generation: generation) else { return }
            let event: CellularIPRotationEvent = isBefore
                ? .beforeProbeCompleted(attemptID: attemptID, snapshot: snapshot)
                : .afterProbeCompleted(attemptID: attemptID, snapshot: snapshot)
            await self.apply(event, expectedGeneration: generation)
        }
    }

    private func scheduleTimeout(
        attemptID: UInt64,
        seconds: Int,
        isLossTimeout: Bool,
        generation: UInt64
    ) {
        let task = Task { @MainActor [weak self] in
            guard let self else { return }
            do {
                try await self.sleeper.sleep(seconds: seconds)
            } catch {
                return
            }
            guard !Task.isCancelled,
                  self.isCurrent(attemptID: attemptID, generation: generation) else { return }
            let event: CellularIPRotationEvent = isLossTimeout
                ? .lossTimedOut(attemptID: attemptID)
                : .returnTimedOut(attemptID: attemptID)
            await self.apply(event, expectedGeneration: generation)
        }
        if isLossTimeout {
            lossTimeoutTask?.cancel()
            lossTimeoutTask = task
        } else {
            returnTimeoutTask?.cancel()
            returnTimeoutTask = task
        }
    }

    private func scheduleHold(attemptID: UInt64, seconds: Int, generation: UInt64) {
        holdTask?.cancel()
        holdTask = Task { @MainActor [weak self] in
            guard let self else { return }
            let holdDeadline = self.clock.currentDate().addingTimeInterval(TimeInterval(seconds))
            _ = await self.notificationCue.schedule(
                attemptID: attemptID,
                holdDeadline: holdDeadline
            )
            guard !Task.isCancelled,
                  self.isCurrent(attemptID: attemptID, generation: generation) else { return }
            if seconds > 0 {
                for remainingSeconds in stride(from: seconds - 1, through: 0, by: -1) {
                    do {
                        try await self.sleeper.sleep(seconds: 1)
                    } catch {
                        return
                    }
                    guard !Task.isCancelled,
                          self.isCurrent(attemptID: attemptID, generation: generation) else { return }
                    await self.apply(
                        .holdCountdownTick(
                            attemptID: attemptID,
                            remainingSeconds: remainingSeconds
                        ),
                        expectedGeneration: generation
                    )
                }
            }
            guard self.isCurrent(attemptID: attemptID, generation: generation) else { return }
            await self.apply(
                .holdCountdownFinished(attemptID: attemptID),
                expectedGeneration: generation
            )
        }
    }

    private func scheduleResume(attemptID: UInt64, generation: UInt64) {
        resumeTask?.cancel()
        let receipt = pauseReceipt
        resumeTask = Task { @MainActor [weak self] in
            guard let self else { return }
            guard self.requiresTerminalTunnelResume else {
                self.completeTerminalRestoration(
                    attemptID: attemptID,
                    generation: generation
                )
                return
            }
            do {
                try await self.tunnel.resumeAfterRotation(receipt)
                guard generation == self.attemptGeneration else { return }
                self.completeTerminalRestoration(
                    attemptID: attemptID,
                    generation: generation
                )
            } catch {
                guard generation == self.attemptGeneration else { return }
                self.publishTerminalRestorationFailure(
                    attemptID: attemptID,
                    generation: generation
                )
            }
        }
    }

    private func completeTerminalRestoration(
        attemptID: UInt64,
        generation: UInt64
    ) {
        guard generation == attemptGeneration,
              state.attemptID == attemptID,
              let outcome = pendingTerminalOutcome else { return }
        guard case .restoring = state else { return }
        cancelOwnedTasks()
        attemptGeneration &+= 1
        timeoutDeadline = nil
        pendingRetirementFailureAttemptID = nil

        let finalState: CellularIPRotationState
        do {
            try checkpointStore.retire(attemptID: attemptID)
            finalState = outcome.state(attemptID: attemptID)
            pendingTerminalOutcome = nil
            clearTunnelRestorationOwnership()
        } catch {
            finalState = .failed(
                attemptID: attemptID,
                failure: .checkpointRetirementFailed
            )
        }
        state = finalState
        reducer = CellularIPRotationReducer(initialState: finalState)
        stateChangeHandler?(state)
    }

    private func publishTerminalRestorationFailure(
        attemptID: UInt64,
        generation: UInt64
    ) {
        guard generation == attemptGeneration,
              pendingTerminalOutcome != nil,
              state.attemptID == attemptID else { return }
        cancelOwnedTasks()
        attemptGeneration &+= 1
        timeoutDeadline = nil
        pendingRetirementFailureAttemptID = nil
        let transition = reducer.reduce(.resumeFailed(attemptID: attemptID))
        state = transition.state
        stateChangeHandler?(state)
    }

    private func clearTunnelRestorationOwnership() {
        pauseReceipt = nil
        recoveringAttemptID = nil
        recoveredPauseAlreadyCompleted = false
        requiresTerminalTunnelResume = false
        checkpointPauseDisposition = .legacyUnknown
    }

    private func reportRetirementFailureIfNeeded(attemptID: UInt64) {
        guard pendingRetirementFailureAttemptID == attemptID else { return }
        pendingRetirementFailureAttemptID = nil
        let transition = reducer.reduce(
            .checkpointRetirementFailed(attemptID: attemptID)
        )
        state = transition.state
        stateChangeHandler?(state)
    }

    private func isCurrent(attemptID: UInt64, generation: UInt64) -> Bool {
        generation == attemptGeneration && state.attemptID == attemptID
    }

    private func cancelOwnedTasks() {
        pauseTask?.cancel()
        probeTask?.cancel()
        lossTimeoutTask?.cancel()
        returnTimeoutTask?.cancel()
        holdTask?.cancel()
        resumeTask?.cancel()
        pauseTask = nil
        probeTask = nil
        lossTimeoutTask = nil
        returnTimeoutTask = nil
        holdTask = nil
        resumeTask = nil
    }
}

private extension CellularIPRotationState {
    var isPrePauseRecoveryState: Bool {
        switch self {
        case .awaitingConfirmation, .preparing:
            true
        case .idle, .awaitingAirplaneMode, .holding, .awaitingCellularReturn,
             .verifying, .restoring, .completed, .failed:
            false
        }
    }

    var terminalOutcome: CellularIPRotationTerminalOutcome? {
        switch self {
        case let .completed(_, before, after, result):
            .completed(before: before, after: after, result: result)
        case let .failed(_, failure):
            .failed(failure)
        case .idle, .awaitingConfirmation, .preparing, .awaitingAirplaneMode, .holding,
             .awaitingCellularReturn, .verifying, .restoring:
            nil
        }
    }
}

private extension CellularIPRotationPauseDisposition {
    var hasRestorationReceipt: Bool {
        switch self {
        case .pausing, .paused:
            true
        case .legacyUnknown, .pending:
            false
        }
    }
}
