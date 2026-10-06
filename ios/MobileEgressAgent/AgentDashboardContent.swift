import MobileEgressCore
import SwiftUI

/// Pure screen composition shared by the live host and isolated visual fixtures.
struct AgentDashboardContent: View {
    let presentation: DirectDashboardPresentation
    let toggleSharing: () -> Void
    let setKeepAwake: (Bool) -> Void
    let addClient: () -> Void
    let showClient: (String) -> Void
    @Environment(\.dynamicTypeSize) private var textSize
    var body: some View {
        VStack(alignment: .leading, spacing: 18) {
            header
            Button(action: toggleSharing) {
                Label(presentation.sharingRequested ? "Stop sharing" : "Start sharing", systemImage: presentation.sharingRequested ? "stop.fill" : "play.fill")
            }.buttonStyle(AgentActionStyle(primary: !presentation.sharingRequested))
                .disabled(!presentation.canToggleSharing).accessibilityIdentifier("sharing-control")
            availability.id("availability")
            if presentation.isEmpty {
                AgentCard {
                    Image(systemName: "qrcode.viewfinder").font(.largeTitle).foregroundStyle(AgentDesign.mint).accessibilityHidden(true)
                    Text("Pair your first computer").font(.title2.bold())
                    Text("Open Mobile Egress on your computer and scan its pairing code.").foregroundStyle(AgentDesign.secondary)
                    Button("Scan a QR code", action: addClient).buttonStyle(AgentActionStyle(primary: true))
                }
            } else { activity.id("activity") }
            clients.id("clients")
        }.foregroundStyle(AgentDesign.text)
    }
    private var header: some View {
        VStack(alignment: .leading, spacing: 16) {
            HStack(spacing: 12) {
                Image("ZFNFHeader").resizable().scaledToFit().frame(width: 42, height: 42).accessibilityHidden(true)
                Text("MOBILE EGRESS").font(.footnote.weight(.bold)).tracking(1.4).foregroundStyle(AgentDesign.secondary)
            }
            AgentBadge(label: presentation.badge, tone: presentation.tone)
            Text(presentation.headline).font(.largeTitle.bold()).fixedSize(horizontal: false, vertical: true)
            if !presentation.isEmpty {
                Text("\(presentation.connectedCount) of \(presentation.clients.count) Clients connected").font(.headline)
            }
            Text(presentation.explanation).font(.body).foregroundStyle(AgentDesign.secondary).fixedSize(horizontal: false, vertical: true)
        }.padding(.vertical, 8)
    }
    private var availability: some View {
        AgentCard {
            Label("Keep this app open while sharing", systemImage: "iphone").font(.headline)
            Text("Locking your iPhone or switching apps pauses sharing.").font(.subheadline).foregroundStyle(AgentDesign.secondary)
            Toggle("Keep screen awake while sharing", isOn: Binding(get: { presentation.keepAwake }, set: setKeepAwake))
                .tint(AgentDesign.mint).frame(minHeight: 44)
            Text(presentation.preventsAutoLock ? "Automatic locking is prevented while you share." : presentation.keepAwake ? "Your screen will stay awake when sharing starts." : "Normal auto-lock is on. When your iPhone locks, sharing pauses.")
                .font(.footnote).foregroundStyle(presentation.preventsAutoLock ? AgentDesign.mint : AgentDesign.secondary)
        }
    }
    private var activity: some View {
        AgentCard {
            Text("Activity").font(.title2.bold())
            Text("Current sessions · resets when connections end").font(.caption).foregroundStyle(AgentDesign.secondary).fixedSize(horizontal: false, vertical: true)
            ViewThatFits(in: .horizontal) {
                HStack { Label("Active connections", systemImage: "arrow.left.arrow.right"); Spacer(); Text(String(presentation.streams)).font(.title3.bold()).monospacedDigit() }
                VStack(alignment: .leading, spacing: 10) { Text("Active connections"); Text(String(presentation.streams)).font(.title3.bold()).monospacedDigit() }
            }.accessibilityElement(children: .ignore).accessibilityLabel("Active connections, \(presentation.streams)")
            let columns = Array(repeating: GridItem(.flexible(), alignment: .top), count: textSize.isAccessibilitySize ? 1 : 2)
            LazyVGrid(columns: columns, alignment: .leading, spacing: 10) {
                AgentMetric(title: "Uploaded", value: formatMobileEgressByteCount(presentation.uploaded), symbol: "arrow.up")
                AgentMetric(title: "Downloaded", value: formatMobileEgressByteCount(presentation.downloaded), symbol: "arrow.down")
            }
        }
    }
    private var clients: some View {
        VStack(alignment: .leading, spacing: 14) {
            HStack { Text("Clients").font(.title2.bold()); Spacer(); Text("\(presentation.clients.count)/10").foregroundStyle(AgentDesign.secondary) }.padding(.top, 6)
            ForEach(presentation.clients) { client in
                AgentCard {
                    Text(client.client.name).font(.headline).fixedSize(horizontal: false, vertical: true)
                    AgentBadge(label: client.status, tone: client.tone)
                    if let guidance = client.guidance { AgentNotice(text: guidance) }
                    Button { showClient(client.id) } label: {
                        HStack { Text("Details"); Spacer(); Image(systemName: "chevron.right") }
                    }.buttonStyle(AgentActionStyle()).accessibilityLabel("Details for \(client.client.name)")
                }.id(client.id)
            }
            Button(action: addClient) { Label("Add Client", systemImage: "plus") }
                .buttonStyle(AgentActionStyle()).disabled(presentation.clients.count >= 10)
            if presentation.clients.count >= 10 { Text("Ten Clients saved. Remove one before adding another.").font(.footnote).foregroundStyle(AgentDesign.secondary) }
        }
    }
}
