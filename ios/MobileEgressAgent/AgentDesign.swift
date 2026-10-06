import MobileEgressCore
import SwiftUI

enum AgentDesign {
    static let background = Color.black
    static let card = Color(hex: 0x080A0F)
    static let inset = Color(hex: 0x0B0E14)
    static let mint = Color(hex: 0x7EF2C5)
    static let blue = Color(hex: 0x7DB7FF)
    static let warning = Color(hex: 0xF4DF74)
    static let error = Color(hex: 0xFF8D98)
    static let text = Color(hex: 0xF2F5FB)
    static let secondary = Color(hex: 0xAEB7C6)
    static let border = Color(hex: 0x1C1D1F)
    static func color(_ tone: DashboardTone) -> Color {
        switch tone { case .mint: mint; case .blue: blue; case .warning: warning; case .neutral: secondary }
    }
}
private extension Color {
    init(hex: UInt32) { self.init(.sRGB, red: Double(hex >> 16 & 255) / 255, green: Double(hex >> 8 & 255) / 255, blue: Double(hex & 255) / 255, opacity: 1) }
}
struct AgentCard<Content: View>: View {
    @Environment(\.colorSchemeContrast) private var contrast
    @ViewBuilder let content: Content
    var body: some View {
        VStack(alignment: .leading, spacing: 14) { content }
            .fixedSize(horizontal: false, vertical: true)
            .frame(maxWidth: .infinity, alignment: .leading).padding(20)
            .background(AgentDesign.card, in: RoundedRectangle(cornerRadius: 28))
            .overlay(RoundedRectangle(cornerRadius: 28).stroke(contrast == .increased ? AgentDesign.secondary : AgentDesign.border, lineWidth: 1))
    }
}
struct AgentBadge: View {
    let label: String
    let tone: DashboardTone
    var body: some View {
        HStack(spacing: 7) {
            Circle().fill(AgentDesign.color(tone)).frame(width: 7, height: 7).accessibilityHidden(true)
            Text(label).font(.caption.weight(.semibold)).fixedSize(horizontal: false, vertical: true)
        }.foregroundStyle(AgentDesign.color(tone)).padding(.horizontal, 12).padding(.vertical, 8)
            .background(AgentDesign.color(tone).opacity(0.14), in: Capsule())
            .accessibilityElement(children: .combine)
    }
}
struct AgentActionStyle: ButtonStyle {
    var primary = false
    var destructive = false
    @Environment(\.isEnabled) private var enabled
    @Environment(\.colorSchemeContrast) private var contrast
    func makeBody(configuration: Configuration) -> some View {
        configuration.label.font(.body.weight(.semibold))
            .frame(maxWidth: .infinity, minHeight: 30).padding(.horizontal, 16).padding(.vertical, 12)
            .foregroundStyle(primary ? Color(hex: 0x03060C) : destructive ? AgentDesign.error : AgentDesign.text)
            .background(primary ? AgentDesign.mint : AgentDesign.inset, in: RoundedRectangle(cornerRadius: 17))
            .overlay(RoundedRectangle(cornerRadius: 17).stroke(primary ? Color.clear : contrast == .increased ? AgentDesign.secondary : AgentDesign.border, lineWidth: 1))
            .opacity(!enabled ? 0.5 : configuration.isPressed ? 0.8 : 1)
    }
}
struct AgentMetric: View {
    let title: String
    let value: String
    let symbol: String
    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Image(systemName: symbol).foregroundStyle(AgentDesign.blue).accessibilityHidden(true)
            Text(value).font(.title3.weight(.semibold)).monospacedDigit().foregroundStyle(AgentDesign.text)
            Text(title).font(.caption).foregroundStyle(AgentDesign.secondary)
        }.fixedSize(horizontal: false, vertical: true).frame(maxWidth: .infinity, alignment: .leading).padding(14)
            .background(AgentDesign.inset, in: RoundedRectangle(cornerRadius: 18))
            .accessibilityElement(children: .ignore).accessibilityLabel("\(title), \(value)")
    }
}
struct AgentNotice: View {
    let text: String
    var body: some View {
        Label(text, systemImage: "exclamationmark.circle")
            .font(.subheadline).foregroundStyle(AgentDesign.warning)
            .fixedSize(horizontal: false, vertical: true).accessibilityElement(children: .combine)
    }
}
