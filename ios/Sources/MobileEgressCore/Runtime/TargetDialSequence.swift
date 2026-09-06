import Foundation

/// Monotonic overall deadline shared by sequential native connection attempts.
struct TargetDialSequence {
    struct Attempt: Equatable {
        let index: Int
        let timeout: TimeInterval
    }

    private let candidateCount: Int
    private let timeout: TimeInterval
    private var deadline: TimeInterval?
    private var activeIndex: Int?
    private var attemptDeadline: TimeInterval?
    private var finished = false

    init(candidateCount: Int, timeout: TimeInterval) {
        precondition(candidateCount > 0 && candidateCount <= 8 && timeout > 0 && timeout.isFinite)
        self.candidateCount = candidateCount
        self.timeout = timeout
    }

    var isConnecting: Bool { activeIndex != nil && !finished }

    mutating func start(now: TimeInterval) -> Attempt? {
        guard deadline == nil, !finished else { return nil }
        deadline = now + timeout
        return attempt(index: 0, now: now)
    }

    mutating func failed(_ index: Int, now: TimeInterval) -> Attempt? {
        guard activeIndex == index, !finished else { return nil }
        return attempt(index: index + 1, now: now)
    }

    mutating func ready(_ index: Int, now: TimeInterval) -> Bool {
        guard activeIndex == index, !finished, let deadline, now < deadline,
              let attemptDeadline, now < attemptDeadline else { return false }
        finished = true
        activeIndex = nil
        return true
    }

    mutating func cancel() {
        finished = true
        activeIndex = nil
    }

    private mutating func attempt(index: Int, now: TimeInterval) -> Attempt? {
        guard index < candidateCount, let deadline, now < deadline else {
            cancel()
            return nil
        }
        activeIndex = index
        let remaining = deadline - now
        let duration = index + 1 < candidateCount ? min(3, remaining) : remaining
        attemptDeadline = now + duration
        return Attempt(index: index, timeout: duration)
    }
}
