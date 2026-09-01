import XCTest
@testable import MobileEgressCore

final class TunnelCommandDecisionTests: XCTestCase {
    func testCommandDecisionUsesProviderLifecycleAndConnectionPhase() {
        let cases: [Case] = [
            Case(
                providerState: .starting,
                phase: .disconnected,
                command: .stop,
                isEnabled: true,
                isDestructive: true
            ),
            Case(
                providerState: .stopping,
                phase: .disconnected,
                command: .stop,
                isEnabled: false,
                isDestructive: true
            ),
            Case(
                providerState: .failed,
                phase: .disconnected,
                command: .start,
                isEnabled: true,
                isDestructive: false
            ),
            Case(
                providerState: .stopped,
                phase: .invalid,
                command: .start,
                isEnabled: true,
                isDestructive: false
            ),
            Case(
                providerState: .running,
                phase: .disconnected,
                command: .stop,
                isEnabled: true,
                isDestructive: true
            ),
            Case(
                providerState: .stopped,
                phase: .connecting,
                command: .stop,
                isEnabled: true,
                isDestructive: true
            ),
            Case(
                providerState: .failed,
                phase: .connected,
                command: .stop,
                isEnabled: true,
                isDestructive: true
            ),
            Case(
                providerState: .stopped,
                phase: .reasserting,
                command: .stop,
                isEnabled: true,
                isDestructive: true
            ),
            Case(
                providerState: .running,
                phase: .disconnecting,
                command: .stop,
                isEnabled: false,
                isDestructive: true
            ),
        ]

        for value in cases {
            let decision = TunnelCommandDecision.resolve(
                providerState: value.providerState,
                connectionPhase: value.phase
            )
            XCTAssertEqual(decision.command, value.command, value.description)
            XCTAssertEqual(decision.isEnabled, value.isEnabled, value.description)
            XCTAssertEqual(decision.isDestructive, value.isDestructive, value.description)
        }
    }

    func testConfirmedStopCommandBecomesNoOpWhenCurrentStateNoLongerPermitsStop() {
        XCTAssertEqual(
            TunnelCommandDecision.confirmedStopCommand(
                providerState: .running,
                connectionPhase: .connected
            ),
            .stop
        )

        let driftedCases: [(TunnelProviderLifecycleState, TunnelConnectionPhase)] = [
            (.stopped, .disconnected),
            (.failed, .invalid),
            (.stopping, .disconnecting),
        ]
        for (providerState, connectionPhase) in driftedCases {
            XCTAssertNil(
                TunnelCommandDecision.confirmedStopCommand(
                    providerState: providerState,
                    connectionPhase: connectionPhase
                ),
                "A stale destructive confirmation must never resolve to Start"
            )
        }
    }
}

private struct Case {
    let providerState: TunnelProviderLifecycleState
    let phase: TunnelConnectionPhase
    let command: TunnelCommand
    let isEnabled: Bool
    let isDestructive: Bool

    var description: String {
        "provider=\(providerState.rawValue), phase=\(phase)"
    }
}
