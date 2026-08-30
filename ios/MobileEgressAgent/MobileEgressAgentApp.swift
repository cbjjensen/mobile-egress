import SwiftUI

@main
struct MobileEgressAgentApp: App {
    @StateObject private var model = AgentViewModel()

    var body: some Scene {
        WindowGroup {
            AgentDashboardView(model: model)
        }
    }
}
