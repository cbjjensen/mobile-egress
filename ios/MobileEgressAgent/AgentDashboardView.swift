import MobileEgressCore
import SwiftUI
import UIKit

struct AgentDashboardView: View {
    @ObservedObject var model: AgentViewModel
    @State private var sheet: DashboardSheet?
    @State private var copiedStatus = false
    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 18) {
                AgentDashboardContent(presentation: model.presentation, toggleSharing: model.toggleSharing,
                    setKeepAwake: model.setKeepAwake, addClient: { sheet = .add }, showClient: { sheet = .client($0) })
                AgentRotationCard(state: model.rotationState,
                    canRotate: model.lifecycle.shouldServe && model.cellularAvailability == .available && !model.rotationState.isActive,
                    request: model.requestRotation, cancel: model.cancelRotation)
                if let error = model.errorMessage { AgentCard { AgentNotice(text: error) } }
                Menu {
                    Button("Copy status", systemImage: "doc.on.doc") {
                        UIPasteboard.general.string = model.safeStatusForCopy(); copiedStatus = true
                    }
                    Button("Import connection update", systemImage: "arrow.down.doc") { sheet = .update }
                        .disabled(model.isBusy)
                } label: { Label("More options", systemImage: "ellipsis.circle").frame(maxWidth: .infinity, minHeight: 44) }
                    .foregroundStyle(AgentDesign.secondary)
                if copiedStatus { Text("Status copied. Pairing codes and credentials are excluded.").font(.footnote).foregroundStyle(AgentDesign.secondary) }
            }.padding(20)
        }.background(AgentDesign.background).preferredColorScheme(.dark).tint(AgentDesign.mint)
            .sheet(item: $sheet) { item in
                switch item {
                case .add: AgentImportSheet(model: model, purpose: .pair)
                case .update: AgentImportSheet(model: model, purpose: .update)
                case let .client(id): AgentClientSheet(model: model, clientID: id)
                }
            }
            .alert("Disconnect active connections to rotate?", isPresented: Binding(get: {
                if case .awaitingConfirmation = model.rotationState { return true }; return false
            }, set: { if !$0 { model.confirmRotation(false) } })) {
                Button("Rotate") { model.confirmRotation(true) }
                Button("Cancel", role: .cancel) { model.confirmRotation(false) }
            } message: { Text("All connected computers will pause while you change your cellular IP.") }
    }
}
private enum DashboardSheet: Identifiable {
    case add, update, client(String)
    var id: String { switch self { case .add: "add"; case .update: "update"; case let .client(id): "client-" + id } }
}

struct AgentRotationCard: View {
    let state: CellularIPRotationState
    let canRotate: Bool
    let request: () -> Void
    let cancel: () -> Void
    @State private var expanded = false
    var body: some View {
        AgentCard {
            Button { expanded.toggle() } label: {
                HStack(spacing: 12) {
                    Image(systemName: "arrow.triangle.2.circlepath").foregroundStyle(AgentDesign.blue)
                    Text("Change cellular IP").font(.headline).foregroundStyle(AgentDesign.text)
                    Spacer()
                    Image(systemName: expanded || state.isActive ? "chevron.up" : "chevron.down").foregroundStyle(AgentDesign.secondary)
                }.frame(minHeight: 44)
            }.accessibilityValue(expanded || state.isActive ? "Expanded" : "Collapsed")
            if expanded || state.isActive || hasResult {
                Text(prompt).font(.subheadline).foregroundStyle(AgentDesign.secondary).fixedSize(horizontal: false, vertical: true)
                if state.isActive {
                    Button("Cancel rotation", action: cancel).buttonStyle(AgentActionStyle())
                } else {
                    Text("We'll guide you through Airplane Mode. Your carrier may reuse the same address.").font(.footnote).foregroundStyle(AgentDesign.secondary)
                    Button("Rotate cellular IP", action: request).buttonStyle(AgentActionStyle()).disabled(!canRotate)
                    if !canRotate { Text("Start sharing with cellular data available to rotate.").font(.footnote).foregroundStyle(AgentDesign.secondary) }
                }
            }
        }
    }
    private var hasResult: Bool { switch state { case .completed, .failed: true; default: false } }
    private var prompt: String {
        switch state {
        case .idle: "Temporarily pause your computers to request a new mobile address."
        case .awaitingConfirmation: "Confirm that your active connections can pause."
        case .preparing: "Pausing sharing and checking your cellular address."
        case .awaitingAirplaneMode: "Turn Airplane Mode on in Control Center, then return here while it is on."
        case let .holding(_, seconds, _, _): "Keep Airplane Mode on for \(seconds) more seconds."
        case .awaitingCellularReturn: "Turn Airplane Mode off, then return here."
        case .verifying: "Checking your new cellular address."
        case .restoring: "Reconnecting your enabled computers."
        case let .completed(_, _, _, result): result == .changed ? "Your cellular IP changed." : result == .unchanged ? "Your carrier reused the IP. You can try again." : "Cellular is back. We couldn't verify an address change."
        case .failed: "Rotation didn't finish. Check cellular access and try again."
        }
    }
}
