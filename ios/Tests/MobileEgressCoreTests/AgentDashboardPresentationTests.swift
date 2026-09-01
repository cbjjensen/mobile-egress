import XCTest
@testable import MobileEgressCore

final class AgentDashboardPresentationTests: XCTestCase {
    func testBrandingUsesTheApprovedZFNFNames() {
        XCTAssertEqual(MobileEgressBranding.displayName, "ZFNF Mobile Egress")
        XCTAssertEqual(MobileEgressBranding.agentName, "ZFNF Mobile Egress Agent")
        XCTAssertEqual(MobileEgressBranding.headerTitle, "ZFNF MOBILE EGRESS")
        XCTAssertEqual(MobileEgressBranding.statusClipboardLabel, "ZFNF Mobile Egress status")
    }

    func testSafeStatusIncludesFiniteOperationsDataAndRedactsRotationSecrets() {
        let status = AgentStatusSnapshot(
            agentState: .running,
            cellular: .available,
            relay: .connected,
            activeStreamCount: 3,
            bytesUploaded: 1_024,
            bytesDownloaded: 2_048,
            errorClass: .relayTLS,
            rotation: .completed(
                attemptID: 41,
                before: PublicIPSnapshot(ipv4: "198.51.100.10", ipv6: "2001:db8::1"),
                after: PublicIPSnapshot(ipv4: "198.51.100.11", ipv6: "2001:db8::2"),
                result: .changed
            )
        )

        let copied = status.safeCopiedStatus(isEnrolled: true)

        XCTAssertEqual(
            copied,
            """
            ZFNF Mobile Egress status
            ZFNF Mobile Egress Agent
            Enrolled: yes
            Agent: running
            Cellular: available
            Relay: connected
            Active streams: 3
            Bytes uploaded: 1024
            Bytes downloaded: 2048
            Error class: relay tls
            IP rotation: changed
            """
        )
        XCTAssertFalse(copied.contains("198.51.100"))
        XCTAssertFalse(copied.contains("2001:db8"))
    }

    func testSafeStatusOmitsNetworkTokensAndNeverAcceptsRawErrorsOrCredentials() {
        let copied = AgentStatusSnapshot(
            agentState: .running,
            cellular: .available,
            relay: .disconnected,
            activeStreamCount: 0,
            bytesUploaded: 0,
            bytesDownloaded: 0,
            errorClass: .relayUnavailable,
            rotation: .awaitingAirplaneMode(
                attemptID: 41,
                originalNetworkToken: "private-cellular-token",
                holdSeconds: 10,
                before: PublicIPSnapshot(ipv4: "198.51.100.10", ipv6: nil)
            )
        ).safeCopiedStatus(isEnrolled: true)

        XCTAssertTrue(copied.contains("IP rotation: waiting for airplane mode"))
        XCTAssertFalse(copied.contains("private-cellular-token"))
        XCTAssertFalse(copied.contains("198.51.100.10"))
        XCTAssertFalse(copied.lowercased().contains("certificate"))
        XCTAssertFalse(copied.lowercased().contains("capability"))
        XCTAssertFalse(copied.lowercased().contains("origin"))
    }

    func testUnenrolledDashboardMakesPairingTheOnlyNextAction() {
        let presentation = AgentDashboardPresentation.present(
            AgentDashboardState(isEnrolled: false, status: .idle)
        )

        XCTAssertEqual(presentation.appTitle, "ZFNF MOBILE EGRESS")
        XCTAssertEqual(presentation.headline, "Ready to pair")
        XCTAssertEqual(presentation.scanLabel, "Scan QR")
        XCTAssertTrue(presentation.isScanEnabled)
        XCTAssertEqual(presentation.primaryAgentAction, .none)
        XCTAssertEqual(presentation.rotationAction, .none)
        XCTAssertFalse(presentation.isRotationEnabled)
    }

    func testConnectedDashboardSeparatesCellularAndRelayHealthAndOwnsMetrics() {
        let status = AgentStatusSnapshot(
            agentState: .running,
            cellular: .available,
            relay: .connected,
            activeStreamCount: 2,
            bytesUploaded: 1_536,
            bytesDownloaded: 2 * 1_024 * 1_024,
            errorClass: .none,
            rotation: .idle
        )

        let presentation = AgentDashboardPresentation.present(
            AgentDashboardState(isEnrolled: true, status: status)
        )

        XCTAssertEqual(presentation.headline, "Cellular relay active")
        XCTAssertEqual(presentation.tone, .success)
        XCTAssertEqual(
            presentation.cellularHealth,
            AgentHealthPresentation(label: "Cellular", value: "Available", tone: .success)
        )
        XCTAssertEqual(
            presentation.relayHealth,
            AgentHealthPresentation(label: "Relay", value: "Connected", tone: .success)
        )
        XCTAssertEqual(
            presentation.metrics,
            [
                AgentMetricPresentation(label: "Active streams", value: "2"),
                AgentMetricPresentation(label: "Uploaded", value: "1.5 KB"),
                AgentMetricPresentation(label: "Downloaded", value: "2.0 MB"),
            ]
        )
        XCTAssertEqual(presentation.primaryAgentAction, .stop)
        XCTAssertEqual(presentation.rotationAction, .rotate)
        XCTAssertTrue(presentation.isRotationEnabled)
        XCTAssertTrue(presentation.requiresActiveStreamConfirmation)
        XCTAssertNil(presentation.finiteErrorCopy)
    }

    func testActiveStreamsEnableRotationButRequirePresentationConfirmation() {
        let status = AgentStatusSnapshot(
            agentState: .running,
            cellular: .available,
            relay: .connected,
            activeStreamCount: 4,
            bytesUploaded: 0,
            bytesDownloaded: 0,
            errorClass: .none,
            rotation: .idle
        )

        let presentation = AgentDashboardPresentation.present(
            AgentDashboardState(isEnrolled: true, status: status)
        )

        XCTAssertTrue(presentation.isRotationEnabled)
        XCTAssertTrue(presentation.requiresActiveStreamConfirmation)
    }

    func testAwaitingConfirmationRequiresDecisionInsteadOfGenericRotationAction() {
        let status = AgentStatusSnapshot(
            agentState: .running,
            cellular: .available,
            relay: .connected,
            activeStreamCount: 4,
            bytesUploaded: 0,
            bytesDownloaded: 0,
            errorClass: .none,
            rotation: .awaitingConfirmation(
                attemptID: 41,
                originalNetworkToken: "private-token",
                holdSeconds: 10,
                activeStreamCount: 4
            )
        )

        let presentation = AgentDashboardPresentation.present(
            AgentDashboardState(isEnrolled: true, status: status)
        )

        XCTAssertTrue(presentation.requiresActiveStreamConfirmation)
        XCTAssertEqual(presentation.rotationAction, .none)
        XCTAssertFalse(presentation.isRotationEnabled)
        XCTAssertEqual(presentation.headline, "Confirm IP rotation")
        XCTAssertEqual(
            presentation.rotationConfirmation,
            CellularIPRotationConfirmationPresentation(
                title: "Disconnect 4 active streams?",
                message: "Rotating the cellular IP will close every active proxy stream.",
                confirmLabel: "Disconnect and rotate",
                declineLabel: "Keep current connection"
            )
        )
    }

    func testStartingAgentUsesFinitePendingPresentationAndDisablesConflictingActions() {
        let presentation = AgentDashboardPresentation.present(
            AgentDashboardState(
                isEnrolled: true,
                status: status(agentState: .starting, errorClass: .none)
            )
        )

        XCTAssertEqual(presentation.headline, "Starting Agent")
        XCTAssertEqual(presentation.badge, "Starting")
        XCTAssertEqual(presentation.tone, .info)
        XCTAssertEqual(presentation.primaryAgentAction, .none)
        XCTAssertFalse(presentation.isScanEnabled)
        XCTAssertEqual(presentation.rotationAction, .none)
    }

    func testStoppingAgentUsesFinitePendingPresentationAndDisablesConflictingActions() {
        let presentation = AgentDashboardPresentation.present(
            AgentDashboardState(
                isEnrolled: true,
                status: status(agentState: .stopping, errorClass: .none)
            )
        )

        XCTAssertEqual(presentation.headline, "Stopping Agent")
        XCTAssertEqual(presentation.badge, "Stopping")
        XCTAssertEqual(presentation.tone, .info)
        XCTAssertEqual(presentation.primaryAgentAction, .none)
        XCTAssertFalse(presentation.isScanEnabled)
        XCTAssertEqual(presentation.rotationAction, .none)
    }

    func testFailedDisconnectedAgentOffersStartWithMatchingRecoveryCopy() {
        let presentation = AgentDashboardPresentation.present(
            AgentDashboardState(
                isEnrolled: true,
                tunnelConnectionPhase: .disconnected,
                status: status(agentState: .failed, errorClass: .internalFailure)
            )
        )

        XCTAssertEqual(presentation.headline, "Agent needs attention")
        XCTAssertEqual(presentation.badge, "Agent error")
        XCTAssertEqual(presentation.tone, .error)
        XCTAssertEqual(presentation.finiteErrorCopy, "The Agent stopped because of an internal error.")
        XCTAssertEqual(presentation.primaryAgentAction, .start)
        XCTAssertTrue(presentation.summary.contains("start"))
        XCTAssertFalse(presentation.summary.lowercased().contains("pair"))
        XCTAssertFalse(presentation.isScanEnabled)
        XCTAssertEqual(presentation.rotationAction, .none)
    }

    func testFailedConnectedAgentOffersStopWithMatchingRecoveryCopy() {
        let presentation = AgentDashboardPresentation.present(
            AgentDashboardState(
                isEnrolled: true,
                tunnelConnectionPhase: .connected,
                status: status(agentState: .failed, errorClass: .internalFailure)
            )
        )

        XCTAssertEqual(presentation.primaryAgentAction, .stop)
        XCTAssertTrue(presentation.summary.contains("Stop"))
        XCTAssertFalse(presentation.summary.lowercased().contains("pair"))
    }

    func testFailedAgentDisablesActionsDuringEveryTunnelTransitionWithoutPairingCopy() {
        for phase in [
            TunnelConnectionPhase.connecting,
            .reasserting,
            .disconnecting,
        ] {
            let presentation = AgentDashboardPresentation.present(
                AgentDashboardState(
                    isEnrolled: true,
                    tunnelConnectionPhase: phase,
                    status: status(agentState: .failed, errorClass: .internalFailure)
                )
            )

            XCTAssertEqual(presentation.primaryAgentAction, .none, "phase=\(phase)")
            XCTAssertTrue(presentation.inactiveAgentMessage.contains("transition"), "phase=\(phase)")
            XCTAssertFalse(
                presentation.inactiveAgentMessage.lowercased().contains("pair"),
                "phase=\(phase)"
            )
        }
    }

    func testCellularLossTakesPrecedenceOverAnUnrelatedStreamError() {
        let status = AgentStatusSnapshot(
            agentState: .running,
            cellular: .unavailable,
            relay: .disconnected,
            activeStreamCount: 0,
            bytesUploaded: 0,
            bytesDownloaded: 0,
            errorClass: .targetConnect,
            rotation: .idle
        )

        let presentation = AgentDashboardPresentation.present(
            AgentDashboardState(isEnrolled: true, status: status)
        )

        XCTAssertEqual(presentation.headline, "Waiting for cellular")
        XCTAssertEqual(presentation.tone, .warning)
        XCTAssertEqual(presentation.cellularHealth.value, "Unavailable")
        XCTAssertEqual(presentation.relayHealth.value, "Disconnected")
        XCTAssertFalse(presentation.isRotationEnabled)
        XCTAssertEqual(presentation.finiteErrorCopy, "A target connection could not be opened.")
    }

    func testBlockingRelayFailureHasFiniteCopyWithoutRawErrorText() {
        let status = AgentStatusSnapshot(
            agentState: .running,
            cellular: .available,
            relay: .disconnected,
            activeStreamCount: 0,
            bytesUploaded: 0,
            bytesDownloaded: 0,
            errorClass: .relayTLS,
            rotation: .idle
        )

        let presentation = AgentDashboardPresentation.present(
            AgentDashboardState(isEnrolled: true, status: status)
        )

        XCTAssertEqual(presentation.headline, "Connection needs attention")
        XCTAssertEqual(presentation.tone, .error)
        XCTAssertEqual(presentation.finiteErrorCopy, "The secure relay connection failed TLS validation.")
    }

    func testRotationPresentationUsesControlCenterGuidanceAndDisablesDuplicates() {
        let status = AgentStatusSnapshot(
            agentState: .running,
            cellular: .available,
            relay: .disconnected,
            activeStreamCount: 0,
            bytesUploaded: 0,
            bytesDownloaded: 0,
            errorClass: .none,
            rotation: .awaitingAirplaneMode(
                attemptID: 41,
                originalNetworkToken: "private-token",
                holdSeconds: 10,
                before: PublicIPSnapshot(ipv4: "198.51.100.10", ipv6: nil)
            )
        )

        let presentation = AgentDashboardPresentation.present(
            AgentDashboardState(isEnrolled: true, status: status)
        )

        XCTAssertEqual(presentation.headline, "Turn Airplane Mode on")
        XCTAssertTrue(presentation.summary.contains("Control Center"))
        XCTAssertEqual(presentation.rotationLabel, "Waiting for Airplane Mode")
        XCTAssertFalse(presentation.isRotationEnabled)
        XCTAssertFalse(presentation.requiresActiveStreamConfirmation)
    }

    func testHoldingPresentationExposesCountdownAndCancellationWithoutLeakingAddresses() {
        let presentation = AgentDashboardPresentation.present(
            AgentDashboardState(
                isEnrolled: true,
                status: AgentStatusSnapshot(
                    agentState: .running,
                    cellular: .unavailable,
                    relay: .disconnected,
                    activeStreamCount: 0,
                    bytesUploaded: 0,
                    bytesDownloaded: 0,
                    errorClass: .none,
                    rotation: .holding(
                        attemptID: 41,
                        remainingSeconds: 7,
                        before: PublicIPSnapshot(ipv4: "198.51.100.10", ipv6: "2001:db8::1"),
                        returnedNetworkToken: nil
                    )
                )
            )
        )

        XCTAssertEqual(presentation.rotationCountdownSeconds, 7)
        XCTAssertTrue(presentation.showsRotationCancellation)
        XCTAssertFalse(presentation.safeStatusText.contains("198.51.100.10"))
        XCTAssertFalse(presentation.safeStatusText.contains("2001:db8::1"))
    }

    func testInactiveRotationHasNoCountdownOrCancellationControl() {
        let presentation = AgentDashboardPresentation.present(
            AgentDashboardState(isEnrolled: true, status: status(agentState: .stopped, errorClass: .none))
        )

        XCTAssertNil(presentation.rotationCountdownSeconds)
        XCTAssertFalse(presentation.showsRotationCancellation)
    }

    func testUnchangedRotationOffersThirtySecondRetry() {
        let unchanged = PublicIPSnapshot(ipv4: "198.51.100.10", ipv6: nil)
        let status = AgentStatusSnapshot(
            agentState: .running,
            cellular: .available,
            relay: .connected,
            activeStreamCount: 0,
            bytesUploaded: 0,
            bytesDownloaded: 0,
            errorClass: .none,
            rotation: .completed(
                attemptID: 41,
                before: unchanged,
                after: unchanged,
                result: .unchanged
            )
        )

        let presentation = AgentDashboardPresentation.present(
            AgentDashboardState(isEnrolled: true, status: status)
        )

        XCTAssertEqual(presentation.headline, "Carrier reused the IP")
        XCTAssertEqual(presentation.rotationAction, .retry)
        XCTAssertEqual(presentation.rotationLabel, "Retry with 30-second reset")
        XCTAssertTrue(presentation.isRotationEnabled)
    }

    func testCheckpointRetirementFailureHasFiniteSafeRecoveryCopy() {
        let status = AgentStatusSnapshot(
            agentState: .running,
            cellular: .available,
            relay: .connected,
            activeStreamCount: 0,
            bytesUploaded: 0,
            bytesDownloaded: 0,
            errorClass: .none,
            rotation: .failed(
                attemptID: 41,
                failure: .checkpointRetirementFailed
            )
        )

        let presentation = AgentDashboardPresentation.present(
            AgentDashboardState(isEnrolled: true, status: status)
        )
        let copied = status.safeCopiedStatus(isEnrolled: true)

        XCTAssertEqual(presentation.headline, "Rotation storage needs attention")
        XCTAssertEqual(presentation.badge, "Storage failed")
        XCTAssertTrue(presentation.summary.contains("Agent restoration was attempted"))
        XCTAssertEqual(presentation.rotationAction, .none)
        XCTAssertEqual(presentation.rotationLabel, "Restart Agent before rotating")
        XCTAssertFalse(presentation.isRotationEnabled)
        XCTAssertTrue(copied.contains("IP rotation: checkpoint retirement failed"))
    }

    func testPendingRestorationHasFiniteCopyAndNoCancellationOrRotationAction() {
        let presentation = AgentDashboardPresentation.present(
            AgentDashboardState(
                isEnrolled: true,
                status: AgentStatusSnapshot(
                    agentState: .running,
                    cellular: .available,
                    relay: .disconnected,
                    activeStreamCount: 0,
                    bytesUploaded: 0,
                    bytesDownloaded: 0,
                    errorClass: .none,
                    rotation: .restoring(
                        attemptID: 41,
                        outcome: .failed(.cancelled)
                    )
                )
            )
        )

        XCTAssertEqual(presentation.headline, "Restoring Agent")
        XCTAssertEqual(presentation.rotationAction, .none)
        XCTAssertEqual(presentation.rotationLabel, "Restoring Agent…")
        XCTAssertFalse(presentation.isRotationEnabled)
        XCTAssertFalse(presentation.showsRotationCancellation)
        XCTAssertTrue(presentation.safeStatusText.contains("IP rotation: restoring agent"))
    }

    func testByteMetricsUseStableHumanReadableUnits() {
        XCTAssertEqual(formatMobileEgressByteCount(0), "0 B")
        XCTAssertEqual(formatMobileEgressByteCount(1_023), "1023 B")
        XCTAssertEqual(formatMobileEgressByteCount(1_024), "1.0 KB")
        XCTAssertEqual(formatMobileEgressByteCount(1_536), "1.5 KB")
        XCTAssertEqual(formatMobileEgressByteCount(2 * 1_024 * 1_024), "2.0 MB")
        XCTAssertEqual(formatMobileEgressByteCount(1_024 * 1_024 * 1_024), "1.0 GB")
    }

    private func status(
        agentState: AgentOperationalState,
        errorClass: AgentStatusErrorClass
    ) -> AgentStatusSnapshot {
        AgentStatusSnapshot(
            agentState: agentState,
            cellular: .available,
            relay: .disconnected,
            activeStreamCount: 0,
            bytesUploaded: 0,
            bytesDownloaded: 0,
            errorClass: errorClass,
            rotation: .idle
        )
    }
}
