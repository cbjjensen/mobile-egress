public enum TunnelConnectionPhase: Equatable, Sendable {
    case invalid
    case disconnected
    case connecting
    case connected
    case reasserting
    case disconnecting
}

public struct TunnelConnectionPresentation: Equatable, Sendable {
    public let providerState: TunnelProviderLifecycleState
    public let providerError: TunnelProviderErrorClass

    public init(
        providerState: TunnelProviderLifecycleState,
        providerError: TunnelProviderErrorClass
    ) {
        self.providerState = providerState
        self.providerError = providerError
    }
}

public struct TunnelConnectionStateReducer: Sendable {
    private enum Expectation: Sendable {
        case idle
        case starting
        case active
        case explicitStop
    }

    private var expectation: Expectation = .idle
    private var current = TunnelConnectionPresentation(
        providerState: .stopped,
        providerError: .none
    )

    public init() {}

    @discardableResult
    public mutating func restorePersistentIntent(
        onDemandEnabled: Bool
    ) -> TunnelConnectionPresentation {
        guard onDemandEnabled, expectation == .idle else { return current }
        expectation = .starting
        current = TunnelConnectionPresentation(providerState: .starting, providerError: .none)
        return current
    }

    @discardableResult
    public mutating func startRequested() -> TunnelConnectionPresentation {
        expectation = .starting
        current = TunnelConnectionPresentation(providerState: .starting, providerError: .none)
        return current
    }

    @discardableResult
    public mutating func stopRequested() -> TunnelConnectionPresentation {
        expectation = .explicitStop
        current = TunnelConnectionPresentation(providerState: .stopping, providerError: .none)
        return current
    }

    @discardableResult
    public mutating func observe(
        _ phase: TunnelConnectionPhase,
        disconnectError: TunnelProviderErrorClass?
    ) -> TunnelConnectionPresentation {
        switch phase {
        case .connecting:
            guard expectation != .explicitStop else { return current }
            expectation = .starting
            current = TunnelConnectionPresentation(providerState: .starting, providerError: .none)
        case .connected:
            guard expectation != .explicitStop else { return current }
            expectation = .active
            current = TunnelConnectionPresentation(providerState: .running, providerError: .none)
        case .reasserting:
            guard expectation != .explicitStop else { return current }
            expectation = .active
            current = TunnelConnectionPresentation(providerState: .starting, providerError: .none)
        case .disconnecting:
            if expectation != .idle && expectation != .explicitStop {
                expectation = .active
            }
            current = TunnelConnectionPresentation(providerState: .stopping, providerError: .none)
        case .invalid, .disconnected:
            observeDisconnected(disconnectError: disconnectError)
        }
        return current
    }

    private mutating func observeDisconnected(disconnectError: TunnelProviderErrorClass?) {
        switch expectation {
        case .explicitStop:
            expectation = .idle
            current = TunnelConnectionPresentation(providerState: .stopped, providerError: .none)
        case .starting:
            guard let disconnectError else { return }
            expectation = .idle
            current = TunnelConnectionPresentation(
                providerState: .failed,
                providerError: disconnectError
            )
        case .active:
            expectation = .idle
            current = TunnelConnectionPresentation(
                providerState: .failed,
                providerError: disconnectError ?? .runtimeUnavailable
            )
        case .idle:
            guard current.providerState != .failed else { return }
            current = TunnelConnectionPresentation(providerState: .stopped, providerError: .none)
        }
    }
}
