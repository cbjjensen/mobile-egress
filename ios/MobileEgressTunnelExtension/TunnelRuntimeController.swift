import MobileEgressCore

actor TunnelRuntimeController {
    private var generation: UInt64 = 0
    private var providerState: TunnelProviderLifecycleState = .stopped
    private var providerError: TunnelProviderErrorClass = .none
    private var runtime: AgentSessionRuntime?

    func beginStart() -> UInt64? {
        guard providerState == .stopped || providerState == .failed else { return nil }
        generation &+= 1
        providerState = .starting
        providerError = .none
        runtime = nil
        return generation
    }

    func installAndStart(_ runtime: AgentSessionRuntime, generation expected: UInt64) async -> Bool {
        guard generation == expected, providerState == .starting else { return false }
        self.runtime = runtime
        await runtime.start()
        guard generation == expected, providerState == .starting else {
            await runtime.stop()
            return false
        }
        providerState = .running
        return true
    }

    func fail(_ error: TunnelProviderErrorClass, generation expected: UInt64) async {
        guard generation == expected else { return }
        if let runtime { await runtime.stop() }
        runtime = nil
        providerError = error
        providerState = .failed
    }

    func stop() async {
        generation &+= 1
        let stopGeneration = generation
        providerState = .stopping
        let activeRuntime = runtime
        runtime = nil
        if let activeRuntime { await activeRuntime.stop() }
        guard generation == stopGeneration else { return }
        providerError = .none
        providerState = .stopped
    }

    func status(overriding error: TunnelProviderErrorClass? = nil) async -> TunnelProviderStatus {
        let snapshot: AgentRuntimeSnapshot
        if let runtime {
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
}
