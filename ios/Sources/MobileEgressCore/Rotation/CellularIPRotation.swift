import Foundation

public struct PublicIPSnapshot: Codable, Equatable, Sendable {
    public let ipv4: String?
    public let ipv6: String?

    public init(ipv4: String? = nil, ipv6: String? = nil) {
        self.ipv4 = ipv4
        self.ipv6 = ipv6
    }
}

public enum CellularIPRotationResult: String, Codable, Equatable, Sendable {
    case changed
    case unchanged
    case unverified

    public static func compare(before: PublicIPSnapshot, after: PublicIPSnapshot) -> Self {
        var comparable: [(String, String)] = []
        if let beforeIPv4 = before.ipv4, let afterIPv4 = after.ipv4 {
            comparable.append((beforeIPv4, afterIPv4))
        }
        if let beforeIPv6 = before.ipv6, let afterIPv6 = after.ipv6 {
            comparable.append((beforeIPv6, afterIPv6))
        }
        guard !comparable.isEmpty else { return .unverified }
        return comparable.contains(where: { pair in pair.0 != pair.1 }) ? .changed : .unchanged
    }
}

public enum CellularIPRotationFailure: String, Codable, Equatable, Sendable {
    case cellularDidNotDisconnect
    case cellularDidNotReturn
    case cancelled
    case recoveryExpired
    case tunnelResumeFailed
}

public enum CellularIPRotationState: Codable, Equatable, Sendable {
    case idle
    case awaitingConfirmation(
        attemptID: UInt64,
        originalNetworkToken: String,
        holdSeconds: Int,
        activeStreamCount: Int
    )
    case preparing(
        attemptID: UInt64,
        originalNetworkToken: String,
        holdSeconds: Int,
        cellularLost: Bool,
        returnedNetworkToken: String?
    )
    case awaitingAirplaneMode(
        attemptID: UInt64,
        originalNetworkToken: String,
        holdSeconds: Int,
        before: PublicIPSnapshot
    )
    case holding(
        attemptID: UInt64,
        remainingSeconds: Int,
        before: PublicIPSnapshot,
        returnedNetworkToken: String?
    )
    case awaitingCellularReturn(attemptID: UInt64, before: PublicIPSnapshot)
    case verifying(
        attemptID: UInt64,
        before: PublicIPSnapshot,
        returnedNetworkToken: String
    )
    case completed(
        attemptID: UInt64,
        before: PublicIPSnapshot,
        after: PublicIPSnapshot,
        result: CellularIPRotationResult
    )
    case failed(attemptID: UInt64, failure: CellularIPRotationFailure)

    public var attemptID: UInt64? {
        switch self {
        case .idle:
            nil
        case let .awaitingConfirmation(attemptID, _, _, _),
             let .preparing(attemptID, _, _, _, _),
             let .awaitingAirplaneMode(attemptID, _, _, _),
             let .holding(attemptID, _, _, _),
             let .awaitingCellularReturn(attemptID, _),
             let .verifying(attemptID, _, _),
             let .completed(attemptID, _, _, _),
             let .failed(attemptID, _):
            attemptID
        }
    }

    public var isActive: Bool {
        switch self {
        case .awaitingConfirmation, .preparing, .awaitingAirplaneMode, .holding,
             .awaitingCellularReturn, .verifying:
            true
        case .idle, .completed, .failed:
            false
        }
    }

    fileprivate var nextHoldSeconds: Int {
        if case .completed(_, _, _, .unchanged) = self {
            return CellularIPRotationPolicy.retryHoldSeconds
        }
        return CellularIPRotationPolicy.normalHoldSeconds
    }
}

public struct CellularIPRotationAvailability: Codable, Equatable, Sendable {
    public let isEnrolled: Bool
    public let isAgentRunning: Bool
    public let isCellularAvailable: Bool
    public let activeStreamCount: Int

    public init(
        isEnrolled: Bool,
        isAgentRunning: Bool,
        isCellularAvailable: Bool,
        activeStreamCount: Int
    ) {
        self.isEnrolled = isEnrolled
        self.isAgentRunning = isAgentRunning
        self.isCellularAvailable = isCellularAvailable
        self.activeStreamCount = max(0, activeStreamCount)
    }

    public func isEligible(for state: CellularIPRotationState) -> Bool {
        isEnrolled && isAgentRunning && isCellularAvailable && !state.isActive
    }

    public func requiresConfirmation(for state: CellularIPRotationState) -> Bool {
        isEligible(for: state) && activeStreamCount > 0
    }
}

public enum CellularIPRotationEvent: Codable, Equatable, Sendable {
    case requested(
        attemptID: UInt64,
        networkToken: String,
        availability: CellularIPRotationAvailability
    )
    case confirmationDecided(attemptID: UInt64, proceed: Bool)
    case beforeProbeCompleted(attemptID: UInt64, snapshot: PublicIPSnapshot)
    case cellularLost(attemptID: UInt64)
    case cellularAvailable(attemptID: UInt64, networkToken: String)
    case holdCountdownTick(attemptID: UInt64, remainingSeconds: Int)
    case holdCountdownFinished(attemptID: UInt64)
    case afterProbeCompleted(attemptID: UInt64, snapshot: PublicIPSnapshot)
    case lossTimedOut(attemptID: UInt64)
    case returnTimedOut(attemptID: UInt64)
    case cancelled(attemptID: UInt64)
    case recover(checkpoint: CellularIPRotationCheckpoint, at: Date)
    case resumeFailed(attemptID: UInt64)
    case reset
}

public enum CellularIPRotationEffect: Codable, Equatable, Sendable {
    case pauseAgentAndStreams(attemptID: UInt64)
    case probeBefore(attemptID: UInt64, networkToken: String)
    case presentAirplaneModeGuidance(attemptID: UInt64)
    case scheduleCellularLossTimeout(attemptID: UInt64, seconds: Int)
    case cancelCellularLossTimeout(attemptID: UInt64)
    case startHoldCountdown(attemptID: UInt64, seconds: Int)
    case scheduleCellularReturnTimeout(attemptID: UInt64, seconds: Int)
    case cancelCellularReturnTimeout(attemptID: UInt64)
    case probeAfter(attemptID: UInt64, networkToken: String)
    case resumeAgent(attemptID: UInt64)
}

public struct CellularIPRotationTransition: Codable, Equatable, Sendable {
    public let state: CellularIPRotationState
    public let effects: [CellularIPRotationEffect]

    public init(
        state: CellularIPRotationState,
        effects: [CellularIPRotationEffect] = []
    ) {
        self.state = state
        self.effects = effects
    }
}

public struct CellularIPRotationCheckpoint: Codable, Equatable, Sendable {
    public static let validityDuration: TimeInterval = 5 * 60

    public let state: CellularIPRotationState
    public let savedAt: Date

    public var expiresAt: Date {
        savedAt.addingTimeInterval(Self.validityDuration)
    }

    public init(state: CellularIPRotationState, savedAt: Date) {
        self.state = state
        self.savedAt = savedAt
    }

    public func isExpired(at date: Date) -> Bool {
        date >= expiresAt
    }
}

public struct CellularIPRotationReducer: Sendable {
    public private(set) var state: CellularIPRotationState
    private var highestAttemptID: UInt64?

    public init(initialState: CellularIPRotationState = .idle) {
        state = initialState
        highestAttemptID = initialState.attemptID
    }

    @discardableResult
    public mutating func reduce(_ event: CellularIPRotationEvent) -> CellularIPRotationTransition {
        let transition: CellularIPRotationTransition
        switch event {
        case let .requested(attemptID, networkToken, availability):
            transition = request(
                attemptID: attemptID,
                networkToken: networkToken,
                availability: availability
            )
        case let .confirmationDecided(attemptID, proceed):
            transition = confirm(attemptID: attemptID, proceed: proceed)
        case let .beforeProbeCompleted(attemptID, snapshot):
            transition = completeBeforeProbe(attemptID: attemptID, snapshot: snapshot)
        case let .cellularLost(attemptID):
            transition = cellularLost(attemptID: attemptID)
        case let .cellularAvailable(attemptID, networkToken):
            transition = cellularAvailable(attemptID: attemptID, networkToken: networkToken)
        case let .holdCountdownTick(attemptID, remainingSeconds):
            transition = holdCountdownTick(attemptID: attemptID, remainingSeconds: remainingSeconds)
        case let .holdCountdownFinished(attemptID):
            transition = holdCountdownFinished(attemptID: attemptID)
        case let .afterProbeCompleted(attemptID, snapshot):
            transition = completeAfterProbe(attemptID: attemptID, snapshot: snapshot)
        case let .lossTimedOut(attemptID):
            transition = lossTimedOut(attemptID: attemptID)
        case let .returnTimedOut(attemptID):
            transition = returnTimedOut(attemptID: attemptID)
        case let .cancelled(attemptID):
            transition = cancel(attemptID: attemptID)
        case let .recover(checkpoint, date):
            transition = recover(checkpoint: checkpoint, at: date)
        case let .resumeFailed(attemptID):
            transition = resumeFailed(attemptID: attemptID)
        case .reset:
            transition = reset()
        }
        state = transition.state
        recordAttemptID(transition.state.attemptID)
        return transition
    }

    private func request(
        attemptID: UInt64,
        networkToken: String,
        availability: CellularIPRotationAvailability
    ) -> CellularIPRotationTransition {
        guard availability.isEligible(for: state), isNewAttemptID(attemptID) else {
            return unchanged()
        }
        let holdSeconds = state.nextHoldSeconds
        if availability.activeStreamCount > 0 {
            return CellularIPRotationTransition(
                state: .awaitingConfirmation(
                    attemptID: attemptID,
                    originalNetworkToken: networkToken,
                    holdSeconds: holdSeconds,
                    activeStreamCount: availability.activeStreamCount
                )
            )
        }
        return start(attemptID: attemptID, networkToken: networkToken, holdSeconds: holdSeconds)
    }

    private func confirm(attemptID: UInt64, proceed: Bool) -> CellularIPRotationTransition {
        guard case let .awaitingConfirmation(
            currentAttemptID,
            networkToken,
            holdSeconds,
            _
        ) = state, currentAttemptID == attemptID else {
            return unchanged()
        }
        guard proceed else { return CellularIPRotationTransition(state: .idle) }
        return start(attemptID: attemptID, networkToken: networkToken, holdSeconds: holdSeconds)
    }

    private func start(
        attemptID: UInt64,
        networkToken: String,
        holdSeconds: Int
    ) -> CellularIPRotationTransition {
        CellularIPRotationTransition(
            state: .preparing(
                attemptID: attemptID,
                originalNetworkToken: networkToken,
                holdSeconds: holdSeconds,
                cellularLost: false,
                returnedNetworkToken: nil
            ),
            effects: [
                .pauseAgentAndStreams(attemptID: attemptID),
                .probeBefore(attemptID: attemptID, networkToken: networkToken),
            ]
        )
    }

    private func completeBeforeProbe(
        attemptID: UInt64,
        snapshot: PublicIPSnapshot
    ) -> CellularIPRotationTransition {
        guard case let .preparing(
            currentAttemptID,
            originalNetworkToken,
            holdSeconds,
            cellularLost,
            returnedNetworkToken
        ) = state, currentAttemptID == attemptID else {
            return unchanged()
        }
        if cellularLost {
            return CellularIPRotationTransition(
                state: .holding(
                    attemptID: attemptID,
                    remainingSeconds: holdSeconds,
                    before: snapshot,
                    returnedNetworkToken: returnedNetworkToken
                ),
                effects: [.startHoldCountdown(attemptID: attemptID, seconds: holdSeconds)]
            )
        }
        return CellularIPRotationTransition(
            state: .awaitingAirplaneMode(
                attemptID: attemptID,
                originalNetworkToken: originalNetworkToken,
                holdSeconds: holdSeconds,
                before: snapshot
            ),
            effects: [
                .presentAirplaneModeGuidance(attemptID: attemptID),
                .scheduleCellularLossTimeout(
                    attemptID: attemptID,
                    seconds: CellularIPRotationPolicy.cellularLossTimeoutSeconds
                ),
            ]
        )
    }

    private func cellularLost(attemptID: UInt64) -> CellularIPRotationTransition {
        switch state {
        case let .preparing(
            currentAttemptID,
            originalNetworkToken,
            holdSeconds,
            cellularLost,
            returnedNetworkToken
        ) where currentAttemptID == attemptID:
            guard !cellularLost else { return unchanged() }
            return CellularIPRotationTransition(
                state: .preparing(
                    attemptID: attemptID,
                    originalNetworkToken: originalNetworkToken,
                    holdSeconds: holdSeconds,
                    cellularLost: true,
                    returnedNetworkToken: returnedNetworkToken
                )
            )
        case let .awaitingAirplaneMode(currentAttemptID, _, holdSeconds, before)
            where currentAttemptID == attemptID:
            return CellularIPRotationTransition(
                state: .holding(
                    attemptID: attemptID,
                    remainingSeconds: holdSeconds,
                    before: before,
                    returnedNetworkToken: nil
                ),
                effects: [
                    .cancelCellularLossTimeout(attemptID: attemptID),
                    .startHoldCountdown(attemptID: attemptID, seconds: holdSeconds),
                ]
            )
        default:
            return unchanged()
        }
    }

    private func cellularAvailable(
        attemptID: UInt64,
        networkToken: String
    ) -> CellularIPRotationTransition {
        switch state {
        case let .preparing(
            currentAttemptID,
            originalNetworkToken,
            holdSeconds,
            true,
            returnedNetworkToken
        ) where currentAttemptID == attemptID:
            guard returnedNetworkToken != networkToken else { return unchanged() }
            return CellularIPRotationTransition(
                state: .preparing(
                    attemptID: attemptID,
                    originalNetworkToken: originalNetworkToken,
                    holdSeconds: holdSeconds,
                    cellularLost: true,
                    returnedNetworkToken: networkToken
                )
            )
        case let .holding(currentAttemptID, remainingSeconds, before, returnedNetworkToken)
            where currentAttemptID == attemptID:
            guard returnedNetworkToken != networkToken else { return unchanged() }
            return CellularIPRotationTransition(
                state: .holding(
                    attemptID: attemptID,
                    remainingSeconds: remainingSeconds,
                    before: before,
                    returnedNetworkToken: networkToken
                )
            )
        case let .awaitingCellularReturn(currentAttemptID, before)
            where currentAttemptID == attemptID:
            return CellularIPRotationTransition(
                state: .verifying(
                    attemptID: attemptID,
                    before: before,
                    returnedNetworkToken: networkToken
                ),
                effects: [
                    .cancelCellularReturnTimeout(attemptID: attemptID),
                    .probeAfter(attemptID: attemptID, networkToken: networkToken),
                ]
            )
        default:
            return unchanged()
        }
    }

    private func holdCountdownTick(
        attemptID: UInt64,
        remainingSeconds: Int
    ) -> CellularIPRotationTransition {
        guard case let .holding(
            currentAttemptID,
            currentRemainingSeconds,
            before,
            returnedNetworkToken
        ) = state, currentAttemptID == attemptID else {
            return unchanged()
        }
        let remainingSeconds = max(0, min(currentRemainingSeconds, remainingSeconds))
        guard remainingSeconds != currentRemainingSeconds else { return unchanged() }
        return CellularIPRotationTransition(
            state: .holding(
                attemptID: attemptID,
                remainingSeconds: remainingSeconds,
                before: before,
                returnedNetworkToken: returnedNetworkToken
            )
        )
    }

    private func holdCountdownFinished(attemptID: UInt64) -> CellularIPRotationTransition {
        guard case let .holding(
            currentAttemptID,
            _,
            before,
            returnedNetworkToken
        ) = state, currentAttemptID == attemptID else {
            return unchanged()
        }
        if let returnedNetworkToken {
            return CellularIPRotationTransition(
                state: .verifying(
                    attemptID: attemptID,
                    before: before,
                    returnedNetworkToken: returnedNetworkToken
                ),
                effects: [
                    .probeAfter(attemptID: attemptID, networkToken: returnedNetworkToken),
                ]
            )
        }
        return CellularIPRotationTransition(
            state: .awaitingCellularReturn(attemptID: attemptID, before: before),
            effects: [
                .scheduleCellularReturnTimeout(
                    attemptID: attemptID,
                    seconds: CellularIPRotationPolicy.cellularReturnTimeoutSeconds
                ),
            ]
        )
    }

    private func completeAfterProbe(
        attemptID: UInt64,
        snapshot: PublicIPSnapshot
    ) -> CellularIPRotationTransition {
        guard case let .verifying(currentAttemptID, before, _) = state,
              currentAttemptID == attemptID else {
            return unchanged()
        }
        return CellularIPRotationTransition(
            state: .completed(
                attemptID: attemptID,
                before: before,
                after: snapshot,
                result: .compare(before: before, after: snapshot)
            ),
            effects: [.resumeAgent(attemptID: attemptID)]
        )
    }

    private func lossTimedOut(attemptID: UInt64) -> CellularIPRotationTransition {
        guard case let .awaitingAirplaneMode(currentAttemptID, _, _, _) = state,
              currentAttemptID == attemptID else {
            return unchanged()
        }
        return terminalFailure(.cellularDidNotDisconnect, attemptID: attemptID)
    }

    private func returnTimedOut(attemptID: UInt64) -> CellularIPRotationTransition {
        guard case let .awaitingCellularReturn(currentAttemptID, _) = state,
              currentAttemptID == attemptID else {
            return unchanged()
        }
        return terminalFailure(.cellularDidNotReturn, attemptID: attemptID)
    }

    private func cancel(attemptID: UInt64) -> CellularIPRotationTransition {
        guard state.attemptID == attemptID else { return unchanged() }
        switch state {
        case .awaitingConfirmation:
            return CellularIPRotationTransition(state: .idle)
        case .preparing:
            return terminalFailure(.cancelled, attemptID: attemptID)
        case .awaitingAirplaneMode:
            return terminalFailure(
                .cancelled,
                attemptID: attemptID,
                effectsBeforeResume: [.cancelCellularLossTimeout(attemptID: attemptID)]
            )
        case .holding, .verifying:
            return terminalFailure(.cancelled, attemptID: attemptID)
        case .awaitingCellularReturn:
            return terminalFailure(
                .cancelled,
                attemptID: attemptID,
                effectsBeforeResume: [.cancelCellularReturnTimeout(attemptID: attemptID)]
            )
        case .idle, .completed, .failed:
            return unchanged()
        }
    }

    private func recover(
        checkpoint: CellularIPRotationCheckpoint,
        at date: Date
    ) -> CellularIPRotationTransition {
        guard state == .idle,
              checkpoint.state.isActive,
              let attemptID = checkpoint.state.attemptID,
              isNewAttemptID(attemptID) else {
            return unchanged()
        }
        guard !checkpoint.isExpired(at: date) else {
            return terminalFailure(.recoveryExpired, attemptID: attemptID)
        }
        let elapsedSeconds = max(
            0,
            Int(date.timeIntervalSince(checkpoint.savedAt))
        )
        return recoveryTransition(
            for: checkpoint.state,
            attemptID: attemptID,
            elapsedSeconds: elapsedSeconds
        )
    }

    private func recoveryTransition(
        for checkpointState: CellularIPRotationState,
        attemptID: UInt64,
        elapsedSeconds: Int
    ) -> CellularIPRotationTransition {
        switch checkpointState {
        case .awaitingConfirmation:
            CellularIPRotationTransition(state: checkpointState)
        case let .preparing(_, networkToken, _, _, _):
            CellularIPRotationTransition(
                state: checkpointState,
                effects: [
                    .pauseAgentAndStreams(attemptID: attemptID),
                    .probeBefore(attemptID: attemptID, networkToken: networkToken),
                ]
            )
        case .awaitingAirplaneMode:
            recoverAwaitingAirplaneMode(
                checkpointState,
                attemptID: attemptID,
                elapsedSeconds: elapsedSeconds
            )
        case let .holding(_, remainingSeconds, before, returnedNetworkToken):
            recoverHolding(
                attemptID: attemptID,
                remainingSeconds: remainingSeconds,
                before: before,
                returnedNetworkToken: returnedNetworkToken,
                elapsedSeconds: elapsedSeconds
            )
        case .awaitingCellularReturn:
            recoverAwaitingCellularReturn(
                checkpointState,
                attemptID: attemptID,
                elapsedSeconds: elapsedSeconds
            )
        case let .verifying(_, _, networkToken):
            CellularIPRotationTransition(
                state: checkpointState,
                effects: [
                    .pauseAgentAndStreams(attemptID: attemptID),
                    .probeAfter(attemptID: attemptID, networkToken: networkToken),
                ]
            )
        case .idle, .completed, .failed:
            unchanged()
        }
    }

    private func recoverAwaitingAirplaneMode(
        _ checkpointState: CellularIPRotationState,
        attemptID: UInt64,
        elapsedSeconds: Int
    ) -> CellularIPRotationTransition {
        let remainingSeconds = CellularIPRotationPolicy.cellularLossTimeoutSeconds
            - elapsedSeconds
        guard remainingSeconds > 0 else {
            return terminalFailure(.cellularDidNotDisconnect, attemptID: attemptID)
        }
        return CellularIPRotationTransition(
            state: checkpointState,
            effects: [
                .pauseAgentAndStreams(attemptID: attemptID),
                .presentAirplaneModeGuidance(attemptID: attemptID),
                .scheduleCellularLossTimeout(
                    attemptID: attemptID,
                    seconds: remainingSeconds
                ),
            ]
        )
    }

    private func recoverHolding(
        attemptID: UInt64,
        remainingSeconds: Int,
        before: PublicIPSnapshot,
        returnedNetworkToken: String?,
        elapsedSeconds: Int
    ) -> CellularIPRotationTransition {
        let remainingHoldSeconds = remainingSeconds - elapsedSeconds
        if remainingHoldSeconds > 0 {
            return CellularIPRotationTransition(
                state: .holding(
                    attemptID: attemptID,
                    remainingSeconds: remainingHoldSeconds,
                    before: before,
                    returnedNetworkToken: returnedNetworkToken
                ),
                effects: [
                    .pauseAgentAndStreams(attemptID: attemptID),
                    .startHoldCountdown(
                        attemptID: attemptID,
                        seconds: remainingHoldSeconds
                    ),
                ]
            )
        }
        if let returnedNetworkToken {
            return CellularIPRotationTransition(
                state: .verifying(
                    attemptID: attemptID,
                    before: before,
                    returnedNetworkToken: returnedNetworkToken
                ),
                effects: [
                    .pauseAgentAndStreams(attemptID: attemptID),
                    .probeAfter(attemptID: attemptID, networkToken: returnedNetworkToken),
                ]
            )
        }
        let returnElapsedSeconds = -remainingHoldSeconds
        let remainingReturnSeconds = CellularIPRotationPolicy.cellularReturnTimeoutSeconds
            - returnElapsedSeconds
        guard remainingReturnSeconds > 0 else {
            return terminalFailure(.cellularDidNotReturn, attemptID: attemptID)
        }
        return CellularIPRotationTransition(
            state: .awaitingCellularReturn(attemptID: attemptID, before: before),
            effects: [
                .pauseAgentAndStreams(attemptID: attemptID),
                .scheduleCellularReturnTimeout(
                    attemptID: attemptID,
                    seconds: remainingReturnSeconds
                ),
            ]
        )
    }

    private func recoverAwaitingCellularReturn(
        _ checkpointState: CellularIPRotationState,
        attemptID: UInt64,
        elapsedSeconds: Int
    ) -> CellularIPRotationTransition {
        let remainingSeconds = CellularIPRotationPolicy.cellularReturnTimeoutSeconds
            - elapsedSeconds
        guard remainingSeconds > 0 else {
            return terminalFailure(.cellularDidNotReturn, attemptID: attemptID)
        }
        return CellularIPRotationTransition(
            state: checkpointState,
            effects: [
                .pauseAgentAndStreams(attemptID: attemptID),
                .scheduleCellularReturnTimeout(
                    attemptID: attemptID,
                    seconds: remainingSeconds
                ),
            ]
        )
    }

    private func resumeFailed(attemptID: UInt64) -> CellularIPRotationTransition {
        guard state.attemptID == attemptID else { return unchanged() }
        switch state {
        case .completed:
            return CellularIPRotationTransition(
                state: .failed(attemptID: attemptID, failure: .tunnelResumeFailed)
            )
        case let .failed(_, failure) where failure != .tunnelResumeFailed:
            return CellularIPRotationTransition(
                state: .failed(attemptID: attemptID, failure: .tunnelResumeFailed)
            )
        case .idle, .awaitingConfirmation, .preparing, .awaitingAirplaneMode, .holding,
             .awaitingCellularReturn, .verifying, .failed:
            return unchanged()
        }
    }

    private func reset() -> CellularIPRotationTransition {
        switch state {
        case .completed, .failed:
            CellularIPRotationTransition(state: .idle)
        case .idle, .awaitingConfirmation, .preparing, .awaitingAirplaneMode, .holding,
             .awaitingCellularReturn, .verifying:
            unchanged()
        }
    }

    private func terminalFailure(
        _ failure: CellularIPRotationFailure,
        attemptID: UInt64,
        effectsBeforeResume: [CellularIPRotationEffect] = []
    ) -> CellularIPRotationTransition {
        CellularIPRotationTransition(
            state: .failed(attemptID: attemptID, failure: failure),
            effects: effectsBeforeResume + [.resumeAgent(attemptID: attemptID)]
        )
    }

    private func unchanged() -> CellularIPRotationTransition {
        CellularIPRotationTransition(state: state)
    }

    private func isNewAttemptID(_ attemptID: UInt64) -> Bool {
        guard let highestAttemptID else { return true }
        return attemptID > highestAttemptID
    }

    private mutating func recordAttemptID(_ attemptID: UInt64?) {
        guard let attemptID else { return }
        highestAttemptID = max(highestAttemptID ?? attemptID, attemptID)
    }
}

private enum CellularIPRotationPolicy {
    static let normalHoldSeconds = 10
    static let retryHoldSeconds = 30
    static let cellularLossTimeoutSeconds = 2 * 60
    static let cellularReturnTimeoutSeconds = 3 * 60
}
