import SwiftUI

@main
struct MobileEgressAgentApp: App {
    @StateObject private var model = AgentViewModel()
    @Environment(\.scenePhase) private var scenePhase

    var body: some Scene {
        WindowGroup {
            AgentDashboardView(model: model)
        }
        .onChange(of: scenePhase, initial: true) { _, scenePhase in
            model.sceneChanged(active: scenePhase == .active)
        }
    }
}
