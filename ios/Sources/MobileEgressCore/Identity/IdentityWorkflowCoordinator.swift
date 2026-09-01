public actor IdentityWorkflowCoordinator {
    public static let shared = IdentityWorkflowCoordinator()

    private var isRunning = false
    private var waiters: [CheckedContinuation<Void, Never>] = []
    private var queueCountObservers: [(Int, CheckedContinuation<Void, Never>)] = []

    public init() {}

    public func withExclusiveAccess<Value: Sendable>(
        _ operation: @Sendable () async throws -> Value
    ) async rethrows -> Value {
        await acquire()
        do {
            let value = try await operation()
            release()
            return value
        } catch {
            release()
            throw error
        }
    }

    func waitUntilQueuedOperationCount(_ expected: Int) async {
        if waiters.count >= expected { return }
        await withCheckedContinuation { continuation in
            queueCountObservers.append((expected, continuation))
        }
    }

    private func acquire() async {
        guard isRunning else {
            isRunning = true
            return
        }
        await withCheckedContinuation { continuation in
            waiters.append(continuation)
            resumeSatisfiedQueueCountObservers()
        }
    }

    private func release() {
        guard !waiters.isEmpty else {
            isRunning = false
            return
        }
        waiters.removeFirst().resume()
        resumeSatisfiedQueueCountObservers()
    }

    private func resumeSatisfiedQueueCountObservers() {
        let satisfied = queueCountObservers.filter { $0.0 <= waiters.count }
        queueCountObservers.removeAll { $0.0 <= waiters.count }
        satisfied.forEach { $0.1.resume() }
    }
}
