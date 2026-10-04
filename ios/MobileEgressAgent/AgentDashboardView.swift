import MobileEgressCore
import SwiftUI
import UIKit
import UniformTypeIdentifiers

struct AgentDashboardView: View {
    @ObservedObject var model: AgentViewModel
    @State private var importedCode = ""
    @State private var importFile = false
    @State private var removeCandidate: DirectClientRecord?
    var body: some View {
        NavigationStack {
            List {
                Section {
                    Text(model.title).font(.title2.bold())
                    LabeledContent("Cellular", value: model.cellularAvailability.rawValue)
                    Text("The iPhone must stay in this app and unlocked. Leaving the app or locking the screen disconnects Clients.").font(.footnote)
                    Button(model.lifecycle.startIntent ? "Stop sharing" : "Start sharing", action: model.toggleSharing)
                        .disabled(model.isBusy && !model.lifecycle.startIntent)
                    Toggle("Keep screen awake while sharing", isOn: Binding(get: { model.lifecycle.keepAwake }, set: { model.setKeepAwake($0) }))
                    Text(model.awakeStatus).font(.caption).foregroundStyle(.secondary)
                }
                Section("Clients (\(model.clients.count)/10)") {
                    ForEach(model.clients) { client in
                        VStack(alignment: .leading, spacing: 8) {
                            Text(client.displayName).font(.headline)
                            Text(client.transport.displayName).font(.caption)
                            Text(model.status(client)).font(.caption)
                            Toggle("Enabled", isOn: Binding(get: { client.enabled }, set: { model.setEnabled(client, $0) }))
                                .disabled(client.removing)
                            HStack {
                                if !client.removing { Button(client.isPaired ? "Retry" : "Retry pairing") { model.retry(client) }.disabled(model.isBusy || !client.enabled) }
                                Button("Remove", role: .destructive) { removeCandidate = client }
                            }
                        }.padding(.vertical, 4)
                    }
                    if model.clients.isEmpty { Text("Scan the QR code from your Client app. Previous relay enrollment requires fresh pairing.").font(.footnote) }
                }
                Section("Pair or update a Client") {
                    Button("Scan Client QR") { model.isScannerPresented = true }.disabled(model.isBusy)
                    TextField("Paste invitation or connection update", text: $importedCode, axis: .vertical).textInputAutocapitalization(.never).autocorrectionDisabled()
                    Button("Import code") { let code = importedCode.trimmingCharacters(in: .whitespacesAndNewlines); importedCode = ""; model.acceptScannedCode(code) }.disabled(model.isBusy || importedCode.isEmpty)
                    Button("Import update file") { importFile = true }.disabled(model.isBusy)
                }
                Section("Cellular IP rotation") {
                    Text(rotationPrompt).font(.caption)
                    Button("Rotate cellular IP") { model.requestRotation() }.disabled(!model.lifecycle.shouldServe || model.cellularAvailability != .available || model.rotationState.isActive)
                    if model.rotationState.isActive { Button("Cancel rotation") { model.cancelRotation() } }
                    Text("Return here with Airplane Mode on to see the countdown. When prompted, turn it off and return again. Sharing resumes only while this app is active.").font(.footnote)
                }
                if let error = model.errorMessage { Section { Text(error).foregroundStyle(.red) } }
                Section {
                    Button("Copy safe status") { UIPasteboard.general.string = model.safeStatusForCopy() }
                    Text("Cellular only • Encrypted to your Clients").font(.caption)
                }
            }
            .navigationTitle("Mobile Egress")
            .sheet(isPresented: $model.isScannerPresented) {
                NavigationStack {
                    QRScannerView(onCode: model.acceptScannedCode, onUnavailable: { model.isScannerPresented = false })
                        .navigationTitle("Scan Client QR")
                        .toolbar { Button("Cancel") { model.isScannerPresented = false } }
                }
            }
            .fileImporter(isPresented: $importFile, allowedContentTypes: [.plainText, .json]) { result in
                guard case let .success(url) = result else { return }
                let scoped = url.startAccessingSecurityScopedResource()
                defer { if scoped { url.stopAccessingSecurityScopedResource() } }
                guard let size = try? url.resourceValues(forKeys: [.fileSizeKey]).fileSize, size <= 90_000,
                      let code = try? String(contentsOf: url, encoding: .utf8) else { return }
                model.acceptScannedCode(code.trimmingCharacters(in: .whitespacesAndNewlines))
            }
            .confirmationDialog("Remove this Client from the phone?", isPresented: Binding(get: { removeCandidate != nil }, set: { if !$0 { removeCandidate = nil } })) {
                if let client = removeCandidate { Button("Remove", role: .destructive) { model.remove(client); removeCandidate = nil } }
            } message: { Text("This deletes this phone’s credentials. Revoke the old pairing on the Client before pairing another phone.") }
            .alert("Disconnect all active streams to rotate?", isPresented: Binding(get: { if case .awaitingConfirmation = model.rotationState { return true }; return false }, set: { if !$0 { model.confirmRotation(false) } })) {
                Button("Rotate") { model.confirmRotation(true) }
                Button("Cancel", role: .cancel) { model.confirmRotation(false) }
            }
        }
        .preferredColorScheme(.dark)
    }
    private var rotationPrompt: String {
        switch model.rotationState {
        case .idle: "Ready to rotate all connected Clients."
        case .awaitingConfirmation: "Confirm disconnection of active streams."
        case .preparing: "Stopping sharing and checking the cellular address."
        case .awaitingAirplaneMode: "Turn Airplane Mode on in Control Center, then return to this app while it is on."
        case let .holding(_, seconds, _, _): "Keep Airplane Mode on for \(seconds) more seconds."
        case .awaitingCellularReturn: "Turn Airplane Mode off, then return to this app."
        case .verifying: "Checking the new cellular address."
        case .restoring: "Restoring enabled Clients."
        case let .completed(_, _, _, result): result == .changed ? "Cellular IP changed." : result == .unchanged ? "The carrier reused the IP. You can retry." : "Cellular returned; address change could not be verified."
        case .failed: "Rotation did not complete. Check cellular access and retry."
        }
    }
}
