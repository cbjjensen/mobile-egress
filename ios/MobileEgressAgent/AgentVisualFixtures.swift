#if DEBUG
import MobileEgressCore
import SwiftUI
import UIKit

/// Only value snapshots and no-op actions. Never initializes pairing, keychain,
/// path monitoring or the live view model. Excluded from release builds.
enum AgentVisualFixtures {
    static let scenarios = ["empty", "pending", "stopped", "connecting", "connected", "mixed", "disabled", "removal", "cellular", "recovery"]
    static var requested: String? {
        let arguments = ProcessInfo.processInfo.arguments
        guard let index = arguments.firstIndex(of: "--visual-fixture"), arguments.indices.contains(index + 1), scenarios.contains(arguments[index + 1]) else { return nil }
        return arguments[index + 1]
    }
    static func presentation(_ scenario: String) -> DirectDashboardPresentation {
        var lifecycle = DirectSharingLifecycle(startIntent: scenario != "stopped" && scenario != "empty")
        lifecycle.sceneActive = true; lifecycle.migrationReady = true
        var first = DirectDashboardClient(id: "preview-one", name: "MacBook Pro", transport: .hosted, connection: .authenticated)
        first.streams = 4; first.uploaded = 2_500_000; first.downloaded = 48_700_000
        var second = DirectDashboardClient(id: "preview-two", name: "Windows PC", transport: .hosted, connection: .authenticated)
        second.streams = 2; second.uploaded = 350_000; second.downloaded = 8_000_000
        switch scenario {
        case "pending": first.paired = false; first.needsAcknowledgement = true; first.connection = .waiting
        case "connecting": first.connection = .connecting; second.connection = .connecting
        case "mixed": second.connection = .recovering(.pairingRequired)
        case "disabled": first.enabled = false; second.enabled = false
        case "removal": first.removing = true
        case "recovery": first.connection = .recovering(.unlockRequired); second.connection = .recovering(.retrying)
        default: break
        }
        if scenario != "connected" && scenario != "mixed" { first.streams = 0; first.uploaded = 0; first.downloaded = 0; second.streams = 0; second.uploaded = 0; second.downloaded = 0 }
        return .init(clients: scenario == "empty" ? [] : [first, second], lifecycle: lifecycle, cellular: scenario == "cellular" ? .unavailable : .available)
    }
}
struct AgentVisualFixtureView: View {
    let scenario: String
    private var largeText: Bool { ProcessInfo.processInfo.arguments.contains("--visual-large-text") }
    var body: some View {
        ScrollViewReader { proxy in
            ScrollView { content(scenario).padding(20) }
            .background(AgentDesign.background).preferredColorScheme(.dark)
            .dynamicTypeSize(largeText ? .accessibility3 : .large)
            .task {
                let arguments = ProcessInfo.processInfo.arguments
                if arguments.contains("--visual-capture") { await captureGallery() }
                if let index = arguments.firstIndex(of: "--visual-section"), arguments.indices.contains(index + 1) {
                    try? await Task.sleep(for: .milliseconds(200))
                    proxy.scrollTo(arguments[index + 1], anchor: .top)
                }
            }
        }
    }
    private func content(_ name: String) -> some View {
        VStack(alignment: .leading, spacing: 18) {
            AgentDashboardContent(presentation: AgentVisualFixtures.presentation(name), toggleSharing: {}, setKeepAwake: { _ in }, addClient: {}, showClient: { _ in })
            AgentRotationCard(state: .idle, canRotate: true, request: {}, cancel: {})
        }
    }
    @MainActor private func captureGallery() async {
        guard let scene = UIApplication.shared.connectedScenes.first as? UIWindowScene else { return }
        let directory = URL.documentsDirectory.appending(path: "visual-parity-native", directoryHint: .isDirectory)
        do {
            try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
            for name in AgentVisualFixtures.scenarios {
                // Tall accessibility layouts exceed UIKit's snapshot surface;
                // capture those from the actual scrolling simulator instead.
                for (label, width, size) in [("small", 375.0, DynamicTypeSize.large), ("large", 440.0, .large)] {
                    let rendered = content(name).padding(20).frame(width: width)
                        .background(AgentDesign.background).environment(\.colorScheme, .dark)
                        .environment(\.dynamicTypeSize, size)
                    let host = UIHostingController(rootView: rendered)
                    host.overrideUserInterfaceStyle = .dark
                    let size = host.sizeThatFits(in: CGSize(width: width, height: 30_000))
                    let window = UIWindow(windowScene: scene)
                    window.frame = CGRect(origin: .zero, size: size)
                    window.rootViewController = host
                    window.isHidden = false
                    host.view.frame = CGRect(origin: .zero, size: size)
                    host.view.backgroundColor = .black
                    host.view.layoutIfNeeded()
                    try await Task.sleep(for: .milliseconds(100))
                    let format = UIGraphicsImageRendererFormat(); format.scale = 2
                    let renderer = UIGraphicsImageRenderer(size: size, format: format)
                    let image = renderer.image { _ in host.view.drawHierarchy(in: host.view.bounds, afterScreenUpdates: true) }
                    window.isHidden = true
                    guard let data = image.pngData() else { continue }
                    try data.write(to: directory.appending(path: "\(name)-\(label).png"), options: Data.WritingOptions.atomic)
                }
            }
        } catch { assertionFailure("Visual fixture rendering failed") }
    }
}
#Preview("Connected") { AgentVisualFixtureView(scenario: "connected") }
#Preview("Empty") { AgentVisualFixtureView(scenario: "empty") }
#endif
