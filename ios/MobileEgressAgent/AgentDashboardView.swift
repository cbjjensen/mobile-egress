import MobileEgressCore
import SwiftUI
import UIKit

struct AgentDashboardView: View {
    @ObservedObject var model: AgentViewModel
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var isStopConfirmationPresented = false
    @State private var systemAlert: DashboardSystemAlert?

    var body: some View {
        let presentation = model.dashboardPresentation
        NavigationStack {
            ZStack {
                OLED.background.ignoresSafeArea()
                ScrollView {
                    LazyVStack(spacing: 18) {
                        BrandHeader(presentation: presentation)
                        PairingCard(
                            presentation: presentation,
                            scanAction: { model.presentScanner() }
                        )
                        AgentStatusCard(
                            presentation: presentation,
                            primaryAction: performPrimaryAgentAction
                        )
                        RotationCard(
                            presentation: presentation,
                            rotationAction: performRotationAction,
                            cancelAction: { model.cancelRotation() }
                        )
                        DiagnosticCard(
                            copyAction: { copySafeStatus(presentation) }
                        )
                        Text("Cellular only • No Wi-Fi fallback")
                            .font(.footnote)
                            .foregroundStyle(OLED.secondaryText)
                            .multilineTextAlignment(.center)
                            .padding(.vertical, 8)
                    }
                    .frame(maxWidth: 720)
                    .padding(.horizontal, 20)
                    .padding(.vertical, 20)
                    .frame(maxWidth: .infinity)
                }
                .refreshable { await model.refreshNow() }
            }
            .navigationTitle(MobileEgressBranding.displayName)
            .navigationBarTitleDisplayMode(.inline)
            .toolbarBackground(OLED.background, for: .navigationBar)
            .toolbarBackground(.visible, for: .navigationBar)
        }
        .preferredColorScheme(.dark)
        .tint(OLED.mint)
        .transaction { transaction in
            if reduceMotion {
                transaction.disablesAnimations = true
            }
        }
        .task {
            model.startMonitoring()
            if let error = model.userError {
                systemAlert = .userError(error)
            }
        }
        .onChange(of: presentation.rotationConfirmation) { _, confirmation in
            if let confirmation {
                systemAlert = .rotation(confirmation)
            }
        }
        .onChange(of: model.userError?.id) { _, _ in
            if let error = model.userError {
                systemAlert = .userError(error)
            }
        }
        .confirmationDialog(
            "Stop \(MobileEgressBranding.agentName)?",
            isPresented: $isStopConfirmationPresented,
            titleVisibility: .visible
        ) {
            Button("Stop Agent", role: .destructive) {
                model.toggleTunnel()
            }
            Button("Keep Running", role: .cancel) {}
        } message: {
            Text("This disconnects active proxy streams and disables automatic reconnect.")
        }
        .alert(item: $systemAlert) { alert in
            makeSystemAlert(alert)
        }
        .sheet(isPresented: $model.isScannerPresented) {
            NavigationStack {
                QRScannerView(
                    onCode: model.acceptScannedCode,
                    onUnavailable: model.scannerBecameUnavailable
                )
                .ignoresSafeArea(edges: .bottom)
                .navigationTitle("Scan QR")
                .navigationBarTitleDisplayMode(.inline)
                .toolbar {
                    ToolbarItem(placement: .cancellationAction) {
                        Button(action: model.cancelScanner) {
                            Image(systemName: "xmark")
                                .frame(minWidth: 44, minHeight: 44)
                        }
                        .accessibilityLabel("Close scanner")
                    }
                }
            }
        }
    }

    private func performPrimaryAgentAction(_ action: AgentPrimaryAction) {
        switch action {
        case .start:
            model.toggleTunnel()
        case .stop:
            isStopConfirmationPresented = true
        case .none:
            break
        }
    }

    private func performRotationAction(_ action: CellularIPRotationAction) {
        switch action {
        case .rotate:
            model.requestRotation()
        case .retry:
            model.retryRotation()
        case .none:
            break
        }
    }

    private func copySafeStatus(_ presentation: AgentDashboardPresentation) {
        UIPasteboard.general.string = presentation.safeStatusText
        UIAccessibility.post(notification: .announcement, argument: "Diagnostic status copied")
        systemAlert = .statusCopied
    }

    private func makeSystemAlert(_ alert: DashboardSystemAlert) -> Alert {
        switch alert {
        case let .rotation(confirmation):
            Alert(
                title: Text(confirmation.title),
                message: Text(confirmation.message),
                primaryButton: .destructive(Text(confirmation.confirmLabel)) {
                    model.confirmRotationStart()
                },
                secondaryButton: .cancel(Text(confirmation.declineLabel)) {
                    model.declineRotation()
                }
            )
        case let .userError(error):
            Alert(
                title: Text("Action unavailable"),
                message: Text(error.message),
                dismissButton: .default(Text("OK")) {
                    model.dismissUserError()
                }
            )
        case .statusCopied:
            Alert(
                title: Text("Diagnostic status copied"),
                message: Text("Only finite operational state and totals were copied."),
                dismissButton: .default(Text("OK"))
            )
        }
    }
}

private enum DashboardSystemAlert: Identifiable {
    case rotation(CellularIPRotationConfirmationPresentation)
    case userError(AgentUserError)
    case statusCopied

    var id: String {
        switch self {
        case let .rotation(confirmation): "rotation-\(confirmation.title)"
        case let .userError(error): "error-\(error.id)"
        case .statusCopied: "status-copied"
        }
    }
}

private struct BrandHeader: View {
    let presentation: AgentDashboardPresentation

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            ViewThatFits(in: .horizontal) {
                HStack(alignment: .center, spacing: 12) {
                    brandIdentity
                    Spacer(minLength: 12)
                    StatusBadge(text: presentation.badge, tone: presentation.tone)
                }
                VStack(alignment: .leading, spacing: 12) {
                    brandIdentity
                    StatusBadge(text: presentation.badge, tone: presentation.tone)
                }
            }
            Text(presentation.headline)
                .font(.largeTitle.bold())
                .foregroundStyle(OLED.primaryText)
                .fixedSize(horizontal: false, vertical: true)
            Text(presentation.summary)
                .font(.body)
                .foregroundStyle(OLED.secondaryText)
                .fixedSize(horizontal: false, vertical: true)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .accessibilityElement(children: .contain)
    }

    private var brandIdentity: some View {
        HStack(spacing: 12) {
            Image("ZFNFHeader")
                .resizable()
                .scaledToFit()
                .frame(width: 48, height: 48)
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 3) {
                Text(presentation.appTitle)
                    .font(.caption.weight(.bold))
                    .tracking(1.4)
                    .foregroundStyle(OLED.primaryText)
                    .fixedSize(horizontal: false, vertical: true)
                Text("Cellular Agent")
                    .font(.subheadline)
                    .foregroundStyle(OLED.secondaryText)
            }
        }
        .accessibilityElement(children: .combine)
        .accessibilityLabel("\(MobileEgressBranding.displayName), Cellular Agent")
    }
}

private struct PairingCard: View {
    let presentation: AgentDashboardPresentation
    let scanAction: () -> Void

    var body: some View {
        AppCard(borderColor: toneColor(presentation.pairingTone).opacity(0.35)) {
            SectionHeader(step: "01", label: "PAIRING")
            Text("Secure phone identity")
                .font(.title2.weight(.semibold))
                .foregroundStyle(OLED.primaryText)
            Text("Scan a controller QR to pair this phone or securely update its relay endpoint.")
                .font(.body)
                .foregroundStyle(OLED.secondaryText)
                .fixedSize(horizontal: false, vertical: true)
            Button(action: scanAction) {
                Label(presentation.scanLabel, systemImage: "qrcode.viewfinder")
                    .font(.headline)
                    .frame(maxWidth: .infinity)
                    .frame(minHeight: 44)
            }
            .buttonStyle(.borderedProminent)
            .tint(OLED.mint)
            .foregroundStyle(OLED.buttonText)
            .disabled(!presentation.isScanEnabled)
            .accessibilityHint("Opens the native QR scanner")
        }
    }
}

private struct AgentStatusCard: View {
    let presentation: AgentDashboardPresentation
    let primaryAction: (AgentPrimaryAction) -> Void

    var body: some View {
        AppCard {
            SectionHeader(step: "02", label: "AGENT")
            ViewThatFits(in: .horizontal) {
                HStack(spacing: 10) {
                    HealthTile(presentation: presentation.cellularHealth)
                    HealthTile(presentation: presentation.relayHealth)
                }
                VStack(spacing: 10) {
                    HealthTile(presentation: presentation.cellularHealth)
                    HealthTile(presentation: presentation.relayHealth)
                }
            }
            LazyVGrid(
                columns: [GridItem(.adaptive(minimum: 104), spacing: 8)],
                spacing: 8
            ) {
                ForEach(Array(presentation.metrics.enumerated()), id: \.offset) { _, metric in
                    MetricTile(presentation: metric)
                }
            }
            if let finiteErrorCopy = presentation.finiteErrorCopy {
                Label(finiteErrorCopy, systemImage: "exclamationmark.triangle.fill")
                    .font(.body)
                    .foregroundStyle(OLED.error)
                    .fixedSize(horizontal: false, vertical: true)
                    .padding(14)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .background(OLED.error.opacity(0.12), in: RoundedRectangle(cornerRadius: 18))
                    .accessibilityLabel("Agent error")
                    .accessibilityValue(finiteErrorCopy)
            }
            primaryActionControl
        }
    }

    @ViewBuilder
    private var primaryActionControl: some View {
        switch presentation.primaryAgentAction {
        case .start:
            Button(action: { primaryAction(.start) }) {
                Label("Start cellular Agent", systemImage: "play.fill")
                    .font(.headline)
                    .frame(maxWidth: .infinity)
                    .frame(minHeight: 44)
            }
            .buttonStyle(.borderedProminent)
            .tint(OLED.mint)
            .foregroundStyle(OLED.buttonText)
        case .stop:
            Button(role: .destructive, action: { primaryAction(.stop) }) {
                Label("Stop Agent", systemImage: "stop.fill")
                    .font(.headline)
                    .frame(maxWidth: .infinity)
                    .frame(minHeight: 44)
            }
            .buttonStyle(.bordered)
            .tint(OLED.error)
        case .none:
            Text(presentation.inactiveAgentMessage)
                .font(.body)
                .foregroundStyle(OLED.secondaryText)
                .fixedSize(horizontal: false, vertical: true)
                .padding(14)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(OLED.raisedSurface, in: RoundedRectangle(cornerRadius: 17))
        }
    }
}

private struct RotationCard: View {
    let presentation: AgentDashboardPresentation
    let rotationAction: (CellularIPRotationAction) -> Void
    let cancelAction: () -> Void

    var body: some View {
        AppCard {
            SectionHeader(step: "03", label: "CELLULAR IP ROTATION")
            Text("Reset cellular egress manually")
                .font(.title2.weight(.semibold))
                .foregroundStyle(OLED.primaryText)
            Text("Open Control Center manually. Turn Airplane Mode on, wait for the cue and countdown, then turn it off. The app never changes Airplane Mode for you.")
                .font(.body)
                .foregroundStyle(OLED.secondaryText)
                .fixedSize(horizontal: false, vertical: true)

            if presentation.showsRotationCancellation {
                VStack(alignment: .leading, spacing: 6) {
                    Text(presentation.headline)
                        .font(.headline)
                        .foregroundStyle(toneColor(presentation.tone))
                    Text(presentation.summary)
                        .font(.subheadline)
                        .foregroundStyle(OLED.secondaryText)
                        .fixedSize(horizontal: false, vertical: true)
                    if let seconds = presentation.rotationCountdownSeconds {
                        Text("\(seconds) s")
                            .font(.title.monospacedDigit().weight(.semibold))
                            .foregroundStyle(OLED.warning)
                            .accessibilityLabel("Airplane Mode countdown")
                            .accessibilityValue("\(seconds) seconds remaining")
                    }
                }
                .padding(14)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(OLED.raisedSurface, in: RoundedRectangle(cornerRadius: 18))
            }

            Button(action: { rotationAction(presentation.rotationAction) }) {
                Label(presentation.rotationLabel, systemImage: "antenna.radiowaves.left.and.right")
                    .font(.headline)
                    .frame(maxWidth: .infinity)
                    .frame(minHeight: 44)
            }
            .buttonStyle(.borderedProminent)
            .tint(OLED.blue)
            .foregroundStyle(OLED.buttonText)
            .disabled(!presentation.isRotationEnabled)

            if presentation.showsRotationCancellation {
                Button(role: .cancel, action: cancelAction) {
                    Text("Cancel IP rotation")
                        .font(.headline)
                        .frame(maxWidth: .infinity)
                        .frame(minHeight: 44)
                }
                .buttonStyle(.bordered)
                .tint(OLED.secondaryText)
            }
        }
    }
}

private struct DiagnosticCard: View {
    let copyAction: () -> Void

    var body: some View {
        AppCard {
            SectionHeader(step: "04", label: "DIAGNOSTICS")
            Text("Safe status for support")
                .font(.title2.weight(.semibold))
                .foregroundStyle(OLED.primaryText)
            Text("Copies finite states and totals only—never public addresses, relay origins, credentials, certificates, or raw errors.")
                .font(.body)
                .foregroundStyle(OLED.secondaryText)
                .fixedSize(horizontal: false, vertical: true)
            Button(action: copyAction) {
                Label("Copy diagnostic status", systemImage: "doc.on.doc")
                    .font(.headline)
                    .frame(maxWidth: .infinity)
                    .frame(minHeight: 44)
            }
            .buttonStyle(.bordered)
            .tint(OLED.violet)
            .accessibilityHint("Copies privacy-safe Agent status to the clipboard")
        }
    }
}

private struct AppCard<Content: View>: View {
    let borderColor: Color
    let content: Content

    init(
        borderColor: Color = OLED.border,
        @ViewBuilder content: () -> Content
    ) {
        self.borderColor = borderColor
        self.content = content()
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            content
        }
        .padding(20)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(OLED.surface, in: RoundedRectangle(cornerRadius: 28))
        .overlay {
            RoundedRectangle(cornerRadius: 28)
                .stroke(borderColor, lineWidth: 1)
        }
    }
}

private struct SectionHeader: View {
    let step: String
    let label: String

    var body: some View {
        HStack(spacing: 8) {
            Text(step)
                .foregroundStyle(OLED.mint)
            Text(label)
                .foregroundStyle(OLED.secondaryText)
        }
        .font(.caption.weight(.bold))
        .tracking(1.1)
        .accessibilityElement(children: .combine)
        .accessibilityLabel("Section \(step), \(label)")
    }
}

private struct StatusBadge: View {
    let text: String
    let tone: AgentDashboardTone

    var body: some View {
        let color = toneColor(tone)
        HStack(spacing: 7) {
            Circle()
                .fill(color)
                .frame(width: 7, height: 7)
                .accessibilityHidden(true)
            Text(text)
                .font(.caption.weight(.semibold))
                .fixedSize(horizontal: false, vertical: true)
        }
        .foregroundStyle(color)
        .padding(.horizontal, 10)
        .padding(.vertical, 7)
        .background(color.opacity(0.14), in: Capsule())
        .accessibilityLabel("Agent status")
        .accessibilityValue(text)
    }
}

private struct HealthTile: View {
    let presentation: AgentHealthPresentation

    var body: some View {
        let color = toneColor(presentation.tone)
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 7) {
                Circle()
                    .fill(color)
                    .frame(width: 8, height: 8)
                Text(presentation.label.uppercased())
                    .font(.caption.weight(.bold))
                    .tracking(0.8)
                    .foregroundStyle(OLED.secondaryText)
            }
            Text(presentation.value)
                .font(.headline)
                .foregroundStyle(color)
                .fixedSize(horizontal: false, vertical: true)
        }
        .padding(14)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(OLED.raisedSurface, in: RoundedRectangle(cornerRadius: 20))
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(presentation.label)
        .accessibilityValue(presentation.value)
    }
}

private struct MetricTile: View {
    let presentation: AgentMetricPresentation

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(presentation.label.uppercased())
                .font(.caption2.weight(.bold))
                .tracking(0.7)
                .foregroundStyle(OLED.secondaryText)
                .fixedSize(horizontal: false, vertical: true)
            Text(presentation.value)
                .font(.headline.monospacedDigit())
                .foregroundStyle(OLED.primaryText)
                .fixedSize(horizontal: false, vertical: true)
        }
        .padding(12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(OLED.raisedSurface, in: RoundedRectangle(cornerRadius: 17))
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(presentation.label)
        .accessibilityValue(presentation.value)
    }
}

private func toneColor(_ tone: AgentDashboardTone) -> Color {
    switch tone {
    case .neutral: OLED.secondaryText
    case .accent: OLED.violet
    case .info: OLED.blue
    case .success: OLED.mint
    case .warning: OLED.warning
    case .error: OLED.error
    }
}

private enum OLED {
    static let background = Color.black
    static let surface = Color(red: 8 / 255, green: 10 / 255, blue: 15 / 255)
    static let raisedSurface = Color(red: 11 / 255, green: 14 / 255, blue: 20 / 255)
    static let border = Color(red: 28 / 255, green: 29 / 255, blue: 31 / 255)
    static let primaryText = Color(red: 242 / 255, green: 245 / 255, blue: 251 / 255)
    static let secondaryText = Color(red: 174 / 255, green: 183 / 255, blue: 198 / 255)
    static let mint = Color(red: 126 / 255, green: 242 / 255, blue: 197 / 255)
    static let blue = Color(red: 125 / 255, green: 183 / 255, blue: 255 / 255)
    static let violet = Color(red: 214 / 255, green: 179 / 255, blue: 255 / 255)
    static let warning = Color(red: 244 / 255, green: 223 / 255, blue: 116 / 255)
    static let error = Color(red: 255 / 255, green: 141 / 255, blue: 152 / 255)
    static let buttonText = Color(red: 3 / 255, green: 6 / 255, blue: 12 / 255)
}
