import SwiftUI

@main
struct MobileEgressAgentApp: App {
    var body: some Scene {
        WindowGroup {
            #if DEBUG
            if let fixture = AgentVisualFixtures.requested {
                AgentVisualFixtureView(scenario: fixture)
            } else {
                AgentLiveHost()
            }
            #else
            AgentLiveHost()
            #endif
        }
    }
}

/// This single scene owner survives all sheets. Visual fixtures never create it.
private struct AgentLiveHost: View {
    @StateObject private var model = AgentViewModel()
    @Environment(\.scenePhase) private var scenePhase

    var body: some View {
        AgentDashboardView(model: model)
        .onChange(of: scenePhase, initial: true) { _, scenePhase in
            model.sceneChanged(active: scenePhase == .active)
        }
    }
}
