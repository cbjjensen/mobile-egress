public enum TunnelConnectionPhase: Equatable, Sendable {
    case invalid
    case disconnected
    case connecting
    case connected
    case reasserting
    case disconnecting
}

public enum TunnelCommand: Equatable, Sendable {
    case start
    case stop
}

public struct TunnelCommandDecision: Equatable, Sendable {
    public let command: TunnelCommand
    public let isEnabled: Bool

    public var isDestructive: Bool {
        command == .stop
    }

    public static func resolve(
        providerState: TunnelProviderLifecycleState,
        connectionPhase: TunnelConnectionPhase
    ) -> Self {
        if providerState == .stopping || connectionPhase == .disconnecting {
            return Self(command: .stop, isEnabled: false)
        }
        if providerState == .starting || providerState == .running {
            return Self(command: .stop, isEnabled: true)
        }
        switch connectionPhase {
        case .connecting, .connected, .reasserting:
            return Self(command: .stop, isEnabled: true)
        case .invalid, .disconnected:
            return Self(command: .start, isEnabled: true)
        case .disconnecting:
            return Self(command: .stop, isEnabled: false)
        }
    }

    private init(command: TunnelCommand, isEnabled: Bool) {
        self.command = command
        self.isEnabled = isEnabled
    }
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

public struct TunnelConnectionObservationToken: Equatable, Sendable {
    fileprivate let revision: UInt64
}

public struct TunnelConnectionStateReducer: Sendable {
    private enum Expectation: Equatable, Sendable {
        case idle
        case startingAwaitingEvidence
        case startingWithEvidence
        case active
        case explicitStop
    }

    private var expectation: Expectation = .idle
    private var current = TunnelConnectionPresentation(
        providerState: .stopped,
        providerError: .none
    )
    private var revision: UInt64 = 0

    public init() {}

    public var presentation: TunnelConnectionPresentation {
        current
    }

    public var observationToken: TunnelConnectionObservationToken {
        TunnelConnectionObservationToken(revision: revision)
    }

    public func isCurrent(_ token: TunnelConnectionObservationToken) -> Bool {
        token.revision == revision
    }

    @discardableResult
    public mutating func restorePersistentIntent(
        onDemandEnabled: Bool
    ) -> TunnelConnectionPresentation {
        guard onDemandEnabled, expectation == .idle else { return current }
        expectation = .startingWithEvidence
        current = TunnelConnectionPresentation(providerState: .starting, providerError: .none)
        advanceRevision()
        return current
    }

    @discardableResult
    public mutating func startRequested() -> TunnelConnectionPresentation {
        expectation = .startingAwaitingEvidence
        current = TunnelConnectionPresentation(providerState: .starting, providerError: .none)
        advanceRevision()
        return current
    }

    @discardableResult
    public mutating func startTransactionFailed(
        _ error: TunnelProviderErrorClass
    ) -> TunnelConnectionPresentation {
        guard expectation == .startingAwaitingEvidence || expectation == .startingWithEvidence else {
            return current
        }
        expectation = .idle
        current = TunnelConnectionPresentation(providerState: .failed, providerError: error)
        advanceRevision()
        return current
    }

    @discardableResult
    public mutating func stopRequested() -> TunnelConnectionPresentation {
        expectation = .explicitStop
        current = TunnelConnectionPresentation(providerState: .stopping, providerError: .none)
        advanceRevision()
        return current
    }

    @discardableResult
    public mutating func stopTransactionCompleted(
        persistenceSucceeded: Bool
    ) -> TunnelConnectionPresentation {
        guard expectation == .explicitStop else { return current }
        if !persistenceSucceeded {
            expectation = .active
        }
        advanceRevision()
        return current
    }

    @discardableResult
    public mutating func observe(
        _ phase: TunnelConnectionPhase,
        disconnectError: TunnelProviderErrorClass?
    ) -> TunnelConnectionPresentation {
        observe(
            phase,
            disconnectError: disconnectError,
            matching: observationToken
        ) ?? current
    }

    @discardableResult
    public mutating func observe(
        _ phase: TunnelConnectionPhase,
        disconnectError: TunnelProviderErrorClass?,
        matching token: TunnelConnectionObservationToken
    ) -> TunnelConnectionPresentation? {
        guard isCurrent(token) else { return nil }
        let previousExpectation = expectation
        let previousPresentation = current
        switch phase {
        case .connecting:
            guard expectation != .explicitStop else { return current }
            expectation = .startingWithEvidence
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
            if expectation == .startingAwaitingEvidence {
                return current
            }
            if expectation == .startingWithEvidence {
                expectation = .active
            }
            current = TunnelConnectionPresentation(providerState: .stopping, providerError: .none)
        case .invalid, .disconnected:
            observeDisconnected(disconnectError: disconnectError)
        }
        if expectation != previousExpectation || current != previousPresentation {
            advanceRevision()
        }
        return current
    }

    private mutating func advanceRevision() {
        revision &+= 1
    }

    private mutating func observeDisconnected(disconnectError: TunnelProviderErrorClass?) {
        switch expectation {
        case .explicitStop:
            expectation = .idle
            current = TunnelConnectionPresentation(providerState: .stopped, providerError: .none)
        case .startingAwaitingEvidence:
            return
        case .startingWithEvidence:
            expectation = .idle
            current = TunnelConnectionPresentation(
                providerState: .failed,
                providerError: disconnectError ?? .runtimeUnavailable
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
