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

    func pauseForRotation() async throws -> RotationReceipt
    func resumeAfterRotation(_ receipt: RotationReceipt?) async throws
}

@MainActor
public final class CellularIPRotationCoordinator<
    Tunnel: CellularIPRotationTunnelControlling
> {
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
    private var preserveFreshRecoveryPauseReceipt = false
    private var requiresTerminalTunnelResume = false
    private var timeoutDeadline: Date?
    private var pauseReceipt: Tunnel.RotationReceipt?
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
        guard holdSeconds == expectedHoldSeconds,
              availability.isEligible(for: state),
              let currentNetworkToken else { return }

        nextAttemptID = max(nextAttemptID, state.attemptID ?? 0)
        nextAttemptID &+= 1
        attemptGeneration &+= 1
        cancelOwnedTasks()
        pauseReceipt = nil
        recoveringAttemptID = nil
        preserveFreshRecoveryPauseReceipt = false
        requiresTerminalTunnelResume = false
        timeoutDeadline = nil
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
        guard state.isActive, let attemptID = state.attemptID else { return }
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
            preserveFreshRecoveryPauseReceipt = checkpoint.state.isPrePauseRecoveryState
            requiresTerminalTunnelResume = !preserveFreshRecoveryPauseReceipt
            timeoutDeadline = checkpoint.timeoutDeadline
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
        preserveFreshRecoveryPauseReceipt = false
        requiresTerminalTunnelResume = true
        timeoutDeadline = nil
        await apply(
            .recoveryFailed(attemptID: nextAttemptID),
            expectedGeneration: attemptGeneration
        )
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
        guard let attemptID = state.attemptID, state.isActive else { return }
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
        state = transition.state
        updateDeadline(
            for: transition.effects,
            state: transition.state,
            preservedDeadline: preservedDeadline
        )
        stateChangeHandler?(state)

        if state.isActive, !skipCheckpointSave {
            do {
                try checkpointStore.save(
                    CellularIPRotationCheckpoint(
                        state: state,
                        savedAt: clock.currentDate(),
                        timeoutDeadline: timeoutDeadline
                    )
                )
            } catch {
                try? checkpointStore.clear()
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
            try? checkpointStore.retire(attemptID: terminalAttemptID)
            await notificationCue.cancel(attemptID: terminalAttemptID)
        }

        let effectGeneration = attemptGeneration
        for effect in transition.effects {
            interpret(effect, generation: effectGeneration)
        }
    }

    private func updateDeadline(
        for effects: [CellularIPRotationEffect],
        state: CellularIPRotationState,
        preservedDeadline: Date?
    ) {
        switch state {
        case .awaitingAirplaneMode, .awaitingCellularReturn:
            break
        case .idle, .awaitingConfirmation, .preparing, .holding, .verifying, .completed, .failed:
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
            do {
                let receipt = try await self.tunnel.pauseForRotation()
                guard self.isCurrent(attemptID: attemptID, generation: generation) else { return }
                if self.recoveringAttemptID != attemptID
                    || self.preserveFreshRecoveryPauseReceipt {
                    self.pauseReceipt = receipt
                }
                self.requiresTerminalTunnelResume = true
            } catch {
                guard self.isCurrent(attemptID: attemptID, generation: generation) else { return }
                if self.recoveringAttemptID != attemptID
                    || self.preserveFreshRecoveryPauseReceipt {
                    self.requiresTerminalTunnelResume = false
                }
                if Task.isCancelled { return }
                await self.apply(
                    .cancelled(attemptID: attemptID),
                    expectedGeneration: generation
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
                self.pauseReceipt = nil
                self.recoveringAttemptID = nil
                self.preserveFreshRecoveryPauseReceipt = false
                return
            }
            do {
                try await self.tunnel.resumeAfterRotation(receipt)
                guard generation == self.attemptGeneration else { return }
                self.pauseReceipt = nil
                self.recoveringAttemptID = nil
                self.preserveFreshRecoveryPauseReceipt = false
                self.requiresTerminalTunnelResume = false
            } catch {
                guard generation == self.attemptGeneration else { return }
                self.pauseReceipt = nil
                self.recoveringAttemptID = nil
                self.preserveFreshRecoveryPauseReceipt = false
                self.requiresTerminalTunnelResume = false
                await self.apply(
                    .resumeFailed(attemptID: attemptID),
                    expectedGeneration: generation
                )
            }
        }
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
             .verifying, .completed, .failed:
            false
        }
    }
}
