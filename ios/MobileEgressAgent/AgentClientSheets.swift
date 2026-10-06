import MobileEgressCore
import SwiftUI
import UniformTypeIdentifiers

struct AgentImportSheet: View {
    enum Purpose { case pair, update }
    @ObservedObject var model: AgentViewModel
    let purpose: Purpose
    var clientID: String? = nil
    @Environment(\.dismiss) private var dismiss
    @State private var code = ""
    @State private var showScanner = false
    @State private var showFile = false
    @State private var delivery = ClientCodeDelivery()
    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 18) {
                    AgentCard {
                        Image(systemName: "qrcode.viewfinder").font(.largeTitle).foregroundStyle(AgentDesign.mint).accessibilityHidden(true)
                        Text(purpose == .pair ? "Pair your computer" : "Update a connection").font(.title2.bold())
                        Text(purpose == .pair ? "Open Inevitable Mobile Relay on your computer, choose Pair phone, then scan its QR code here." : "Open the connection-update code on your computer and scan it here.")
                            .foregroundStyle(AgentDesign.secondary)
                        Button("Scan QR code") { delivery = ClientCodeDelivery(); showScanner = true }
                            .buttonStyle(AgentActionStyle(primary: true)).disabled(model.isBusy)
                    }
                    AgentCard {
                        DisclosureGroup("Other options") {
                            VStack(alignment: .leading, spacing: 14) {
                                TextField("Paste a setup or connection-update code", text: $code, axis: .vertical)
                                    .lineLimit(3...6).textInputAutocapitalization(.never).autocorrectionDisabled()
                                    .padding(14).background(AgentDesign.inset, in: RoundedRectangle(cornerRadius: 12))
                                    .accessibilityLabel("Setup or connection-update code")
                                Button("Import pasted code") { delivery = ClientCodeDelivery(); submit(code) }
                                    .buttonStyle(AgentActionStyle()).disabled(model.isBusy || code.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                                Button("Choose a file") { delivery = ClientCodeDelivery(); showFile = true }
                                    .buttonStyle(AgentActionStyle()).disabled(model.isBusy)
                            }.padding(.top, 14)
                        }.frame(minHeight: 44)
                    }
                    if model.isBusy { ProgressView("Connecting…").tint(AgentDesign.mint).frame(maxWidth: .infinity) }
                    if let error = model.errorMessage { AgentCard { AgentNotice(text: error) } }
                }.padding(20)
            }.background(AgentDesign.background).foregroundStyle(AgentDesign.text)
                .navigationTitle(purpose == .pair ? "Add Client" : "Connection update").navigationBarTitleDisplayMode(.inline)
                .toolbar { ToolbarItem(placement: .confirmationAction) { Button("Done") { dismiss() }.frame(minHeight: 44) } }
                .sheet(isPresented: $showScanner) {
                    NavigationStack {
                        QRScannerView(onCode: { value in showScanner = false; submit(value) }, onUnavailable: {
                            showScanner = false
                            model.reportImportError("Camera scanning isn't available. Allow camera access in Settings, or use Other options to paste a code.", clientID: clientID)
                        }).navigationTitle("Scan QR code").navigationBarTitleDisplayMode(.inline)
                            .toolbar { Button("Cancel") { showScanner = false }.frame(minHeight: 44) }
                    }
                }
                .fileImporter(isPresented: $showFile, allowedContentTypes: [.plainText, .json]) { result in
                    switch result {
                    case let .success(url):
                        let scoped = url.startAccessingSecurityScopedResource()
                        defer { if scoped { url.stopAccessingSecurityScopedResource() } }
                        do {
                            let size = try url.resourceValues(forKeys: [.fileSizeKey]).fileSize
                            guard let size, size <= 87_384 else { throw DirectAgentError.invalidBundle }
                            let handle = try FileHandle(forReadingFrom: url); defer { try? handle.close() }
                            let data = try handle.read(upToCount: 87_385) ?? Data()
                            guard data.count <= 87_384, let text = String(data: data, encoding: .utf8), !text.isEmpty else { throw DirectAgentError.invalidBundle }
                            submit(text)
                        } catch { model.reportImportError("This file couldn't be read. Choose a current setup or connection-update file from your Client app.", clientID: clientID) }
                    case .failure:
                        model.reportImportError("The file couldn't be opened. Try again or paste the code instead.", clientID: clientID)
                    }
                }
        }.preferredColorScheme(.dark).tint(AgentDesign.mint)
    }
    private func submit(_ value: String) {
        guard let accepted = delivery.take(value) else { return }
        code = ""
        let destination: ClientCodeImportDestination = purpose == .pair ? .addClient : .connectionUpdate(clientID: clientID)
        model.acceptScannedCode(accepted, destination: destination) { succeeded in if succeeded { dismiss() } }
    }
}

struct AgentClientSheet: View {
    @ObservedObject var model: AgentViewModel
    let clientID: String
    @Environment(\.dismiss) private var dismiss
    @State private var confirmRemove = false
    @State private var showUpdate = false
    private var client: DirectClientRecord? { model.clients.first { $0.id == clientID } }
    private var presentation: DirectDashboardClientPresentation? { model.presentation.clients.first { $0.id == clientID } }
    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 18) {
                    if let client, let presentation {
                        AgentCard {
                            Text(client.displayName).font(.title2.bold()).fixedSize(horizontal: false, vertical: true)
                            AgentBadge(label: presentation.status, tone: presentation.tone)
                            if let guidance = presentation.guidance { AgentNotice(text: guidance) }
                            Toggle("Use this computer", isOn: Binding(get: { client.enabled }, set: { model.setEnabled(client, $0) }))
                                .disabled(!presentation.canEnable).frame(minHeight: 44)
                            Text("Disabled computers stay paired and won't connect until you enable them.").font(.footnote).foregroundStyle(AgentDesign.secondary)
                            if !client.removing {
                                Button(client.isPaired ? "Retry connection" : "Retry pairing") { model.retry(client) }
                                    .buttonStyle(AgentActionStyle()).disabled(!presentation.canRetry)
                            }
                        }
                        AgentCard {
                            Text("Connection information").font(.headline)
                            Text(client.transport.displayName).foregroundStyle(AgentDesign.secondary)
                            LabeledContent("Pairing", value: client.isPaired ? "Paired" : "Pending")
                            Text(client.endpoint).font(.footnote).foregroundStyle(AgentDesign.secondary).textSelection(.enabled)
                                .fixedSize(horizontal: false, vertical: true)
                            Button("Import connection update") { showUpdate = true }.buttonStyle(AgentActionStyle()).disabled(model.isBusy || client.removing)
                        }
                        if let error = model.errorMessage, model.errorClientID == clientID { AgentCard { AgentNotice(text: error) } }
                        Button(client.removing ? "Retry Remove" : "Remove Client", role: .destructive) { confirmRemove = true }
                            .buttonStyle(AgentActionStyle(destructive: true)).disabled(model.isBusy)
                    } else {
                        Text("This Client has been removed.").foregroundStyle(AgentDesign.secondary)
                    }
                }.padding(20)
            }.background(AgentDesign.background).foregroundStyle(AgentDesign.text)
                .navigationTitle("Client details").navigationBarTitleDisplayMode(.inline)
                .toolbar { ToolbarItem(placement: .confirmationAction) { Button("Done") { dismiss() }.frame(minHeight: 44) } }
                .sheet(isPresented: $showUpdate) { AgentImportSheet(model: model, purpose: .update, clientID: clientID) }
                .confirmationDialog("Remove this Client?", isPresented: $confirmRemove, titleVisibility: .visible) {
                    Button("Remove", role: .destructive) { if let client { model.remove(client) } }
                    Button("Cancel", role: .cancel) { }
                } message: { Text("This stops its connection and deletes its pairing from this iPhone. Remove the old phone pairing in the computer's Client app before pairing again.") }
        }.preferredColorScheme(.dark).tint(AgentDesign.mint)
    }
}
