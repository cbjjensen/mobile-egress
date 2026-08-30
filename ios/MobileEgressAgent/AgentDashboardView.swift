import Foundation
import NetworkExtension
import SwiftUI

struct AgentDashboardView: View {
    @ObservedObject var model: AgentViewModel

    var body: some View {
        NavigationStack {
            List {
                Section("Status") {
                    Label(model.statusTitle, systemImage: statusSymbol)
                        .foregroundStyle(statusTint)
                    LabeledContent("Enrollment", value: model.isEnrolled ? "Ready" : "Required")
                }

                Section("Activity") {
                    metricRow("Active streams", value: "\(model.activeStreamCount)", symbol: "point.3.connected.trianglepath.dotted")
                    metricRow("Uploaded", value: formatBytes(model.bytesUploaded), symbol: "arrow.up")
                    metricRow("Downloaded", value: formatBytes(model.bytesDownloaded), symbol: "arrow.down")
                }

                if let error = model.errorMessage {
                    Section {
                        Label(error, systemImage: "exclamationmark.triangle.fill")
                            .foregroundStyle(.red)
                    }
                }

                Section {
                    Button(action: model.presentScanner) {
                        Label(model.isProcessingScan ? "Processing" : "Scan QR", systemImage: "qrcode.viewfinder")
                    }
                    .disabled(!model.canScan)

                    Button(role: model.isTunnelActive ? .destructive : nil, action: model.toggleTunnel) {
                        Label(
                            model.isTunnelActive ? "Stop" : "Start",
                            systemImage: model.isTunnelActive ? "stop.fill" : "play.fill"
                        )
                    }
                    .disabled(!model.canToggleTunnel)
                }
            }
            .navigationTitle("Mobile Egress")
            .refreshable { await model.refreshNow() }
        }
        .task { model.startMonitoring() }
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
                        }
                        .accessibilityLabel("Close scanner")
                    }
                }
            }
        }
    }

    private var statusSymbol: String {
        switch model.vpnStatus {
        case .connected: "checkmark.circle.fill"
        case .connecting, .reasserting: "arrow.triangle.2.circlepath"
        case .disconnecting: "stop.circle"
        case .invalid, .disconnected: "circle"
        @unknown default: "questionmark.circle"
        }
    }

    private var statusTint: Color {
        switch model.vpnStatus {
        case .connected: .green
        case .connecting, .reasserting: .orange
        case .disconnecting, .invalid, .disconnected: .secondary
        @unknown default: .secondary
        }
    }

    private func metricRow(_ title: String, value: String, symbol: String) -> some View {
        LabeledContent {
            Text(value).monospacedDigit()
        } label: {
            Label(title, systemImage: symbol)
        }
    }

    private func formatBytes(_ count: UInt64) -> String {
        ByteCountFormatter.string(fromByteCount: Int64(clamping: count), countStyle: .binary)
    }
}
