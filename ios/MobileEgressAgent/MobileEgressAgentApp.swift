import SwiftUI

@main
struct MobileEgressAgentApp: App {
    @StateObject private var model = AgentViewModel()
    @Environment(\.scenePhase) private var scenePhase

    var body: some Scene {
        WindowGroup {
            AgentDashboardView(model: model)
        }
        .onChange(of: scenePhase) { _, scenePhase in
            if scenePhase == .active {
                model.resumeAfterActivation()
            }
        }
    }
}
