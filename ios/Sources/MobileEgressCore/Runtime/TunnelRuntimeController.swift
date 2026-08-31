public protocol TunnelRuntimeLifecycle: Sendable {
    func start() async
    func stop() async
    func snapshot() async -> AgentRuntimeSnapshot
}

extension AgentSessionRuntime: TunnelRuntimeLifecycle {}

public actor TunnelRuntimeController {
    private struct OwnedRuntime: Sendable {
        let generation: UInt64
        let runtime: any TunnelRuntimeLifecycle
    }

    private var generation: UInt64 = 0
    private var providerState: TunnelProviderLifecycleState = .stopped
    private var providerError: TunnelProviderErrorClass = .none
    private var ownedRuntime: OwnedRuntime?

    public init() {}

    public func beginStart() -> UInt64? {
        guard providerState == .stopped || providerState == .failed else { return nil }
        generation &+= 1
        providerState = .starting
        providerError = .none
        ownedRuntime = nil
        return generation
    }

    public func installAndStart(
        _ runtime: any TunnelRuntimeLifecycle,
        generation expected: UInt64
    ) async -> Bool {
        guard generation == expected, providerState == .starting else { return false }
        ownedRuntime = OwnedRuntime(generation: expected, runtime: runtime)
        await runtime.start()
        guard generation == expected,
              providerState == .starting,
              ownedRuntime?.generation == expected
        else {
            if let staleRuntime = takeRuntime(generation: expected) {
                await staleRuntime.stop()
            }
            return false
        }
        providerState = .running
        return true
    }

    public func fail(
        _ error: TunnelProviderErrorClass,
        generation expected: UInt64
    ) async {
        guard generation == expected else { return }
        if let failedRuntime = takeRuntime(generation: expected) {
            await failedRuntime.stop()
        }
        guard generation == expected else { return }
        providerError = error
        providerState = .failed
    }

    public func stop() async {
        generation &+= 1
        let stopGeneration = generation
        providerState = .stopping
        if let activeRuntime = takeRuntime() {
            await activeRuntime.stop()
        }
        guard generation == stopGeneration else { return }
        providerError = .none
        providerState = .stopped
    }

    public func status(
        overriding error: TunnelProviderErrorClass? = nil
    ) async -> TunnelProviderStatus {
        let snapshot: AgentRuntimeSnapshot
        if let runtime = ownedRuntime?.runtime {
            snapshot = await runtime.snapshot()
        } else {
            snapshot = AgentRuntimeSnapshot(
                connectionState: .stopped,
                activeStreamCount: 0,
                bytesUploaded: 0,
                bytesDownloaded: 0,
                errorClass: .none
            )
        }
        return TunnelProviderStatus(
            providerState: providerState,
            runtimeSnapshot: snapshot,
            providerError: error ?? providerError
        )
    }

    private func takeRuntime(
        generation expected: UInt64? = nil
    ) -> (any TunnelRuntimeLifecycle)? {
        guard let ownedRuntime else { return nil }
        if let expected, ownedRuntime.generation != expected { return nil }
        self.ownedRuntime = nil
        return ownedRuntime.runtime
    }
}
