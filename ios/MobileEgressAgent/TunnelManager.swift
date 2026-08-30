import Foundation
import MobileEgressCore
import NetworkExtension

enum TunnelManagerError: Error {
    case configurationUnavailable
    case providerUnavailable
    case invalidProviderResponse
}

@MainActor
final class TunnelManager {
    private let configuration: MobileEgressSystemConfiguration
    private var manager: NETunnelProviderManager?

    init(configuration: MobileEgressSystemConfiguration) {
        self.configuration = configuration
    }

    var status: NEVPNStatus {
        manager?.connection.status ?? .invalid
    }

    func prepare() async throws {
        let managers = try await NETunnelProviderManager.loadAllFromPreferences()
        let existing = managers.first { candidate in
            guard let tunnelProtocol = candidate.protocolConfiguration as? NETunnelProviderProtocol else {
                return false
            }
            return tunnelProtocol.providerBundleIdentifier == configuration.providerBundleIdentifier
        }
        let selected = existing ?? NETunnelProviderManager()
        let onDemandEnabled = existing?.isOnDemandEnabled ?? false

        if existing == nil || needsConfigurationUpdate(selected) {
            applyBaseConfiguration(to: selected)
            selected.isOnDemandEnabled = onDemandEnabled
            try await selected.saveToPreferences()
            try await selected.loadFromPreferences()
        }
        manager = selected
    }

    func start() async throws {
        let manager = try await preparedManager()
        applyBaseConfiguration(to: manager)
        manager.isOnDemandEnabled = true
        try await manager.saveToPreferences()
        try await manager.loadFromPreferences()

        guard let session = manager.connection as? NETunnelProviderSession else {
            throw TunnelManagerError.configurationUnavailable
        }
        switch session.status {
        case .connected, .connecting, .reasserting:
            return
        case .invalid, .disconnected, .disconnecting:
            try session.startTunnel(options: nil)
        @unknown default:
            throw TunnelManagerError.configurationUnavailable
        }
    }

    func stop() async throws {
        guard let manager else { return }
        applyBaseConfiguration(to: manager)
        manager.isOnDemandEnabled = false
        try await manager.saveToPreferences()
        try await manager.loadFromPreferences()
        manager.connection.stopVPNTunnel()
    }

    func providerStatus() async throws -> TunnelProviderStatus {
        guard let session = manager?.connection as? NETunnelProviderSession,
              session.status == .connected
        else {
            throw TunnelManagerError.providerUnavailable
        }
        let request = try TunnelProviderMessageCodec.statusRequest()
        return try await withCheckedThrowingContinuation { continuation in
            do {
                try session.sendProviderMessage(request) { response in
                    guard let response else {
                        continuation.resume(throwing: TunnelManagerError.providerUnavailable)
                        return
                    }
                    do {
                        continuation.resume(returning: try TunnelProviderMessageCodec.decodeStatus(response))
                    } catch {
                        continuation.resume(throwing: TunnelManagerError.invalidProviderResponse)
                    }
                }
            } catch {
                continuation.resume(throwing: TunnelManagerError.providerUnavailable)
            }
        }
    }

    private func preparedManager() async throws -> NETunnelProviderManager {
        if manager == nil {
            try await prepare()
        }
        guard let manager else { throw TunnelManagerError.configurationUnavailable }
        return manager
    }

    private func applyBaseConfiguration(to manager: NETunnelProviderManager) {
        let tunnelProtocol = NETunnelProviderProtocol()
        tunnelProtocol.providerBundleIdentifier = configuration.providerBundleIdentifier
        tunnelProtocol.serverAddress = "Mobile Egress"
        tunnelProtocol.disconnectOnSleep = false
        manager.protocolConfiguration = tunnelProtocol
        manager.localizedDescription = "Mobile Egress"
        manager.isEnabled = true
        manager.onDemandRules = [NEOnDemandRuleConnect()]
    }

    private func needsConfigurationUpdate(_ manager: NETunnelProviderManager) -> Bool {
        guard let tunnelProtocol = manager.protocolConfiguration as? NETunnelProviderProtocol else {
            return true
        }
        return tunnelProtocol.providerBundleIdentifier != configuration.providerBundleIdentifier ||
            tunnelProtocol.serverAddress != "Mobile Egress" ||
            tunnelProtocol.disconnectOnSleep ||
            manager.localizedDescription != "Mobile Egress" ||
            !manager.isEnabled ||
            manager.onDemandRules?.contains(where: { $0 is NEOnDemandRuleConnect }) != true
    }
}
