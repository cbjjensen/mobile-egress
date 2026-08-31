import Foundation
import MobileEgressCore
import NetworkExtension

enum TunnelManagerError: Error {
    case configurationUnavailable
    case providerUnavailable
    case invalidProviderResponse
}

struct TunnelConnectionRefresh {
    let status: NEVPNStatus
    let phase: TunnelConnectionPhase
    let disconnectError: TunnelProviderErrorClass?
}

@MainActor
final class TunnelManager: TunnelPreferenceSession {
    private let configuration: MobileEgressSystemConfiguration
    private var manager: NETunnelProviderManager?

    init(configuration: MobileEgressSystemConfiguration) {
        self.configuration = configuration
    }

    var status: NEVPNStatus {
        manager?.connection.status ?? .invalid
    }

    var isOnDemandEnabled: Bool {
        manager?.isOnDemandEnabled ?? false
    }

    func statusUpdates() -> AsyncStream<TunnelConnectionPhase> {
        guard let connection = manager?.connection else {
            return AsyncStream { continuation in continuation.finish() }
        }
        return AsyncStream { continuation in
            let observer = NotificationObserverToken(
                NotificationCenter.default.addObserver(
                    forName: .NEVPNStatusDidChange,
                    object: connection,
                    queue: nil
                ) { notification in
                    guard let changedConnection = notification.object as? NEVPNConnection else { return }
                    continuation.yield(changedConnection.status.connectionPhase)
                }
            )
            continuation.onTermination = { _ in
                NotificationCenter.default.removeObserver(observer.value)
            }
        }
    }

    func connectionRefresh(
        observedPhase: TunnelConnectionPhase? = nil
    ) async -> TunnelConnectionRefresh {
        guard let connection = manager?.connection else {
            return TunnelConnectionRefresh(
                status: .invalid,
                phase: .invalid,
                disconnectError: nil
            )
        }
        let status = connection.status
        let phase = observedPhase ?? status.connectionPhase
        let disconnectError: TunnelProviderErrorClass?
        switch phase {
        case .invalid, .disconnected:
            disconnectError = await finiteLastDisconnectError(from: connection)
        case .connecting, .connected, .reasserting, .disconnecting:
            disconnectError = nil
        }
        return TunnelConnectionRefresh(
            status: status,
            phase: phase,
            disconnectError: disconnectError
        )
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
        _ = try await preparedManager()
        try await TunnelPreferenceTransaction.start(using: self)
    }

    func stop() async throws {
        guard manager != nil else { return }
        try await TunnelPreferenceTransaction.stop(using: self)
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

    func loadPreferences() async throws {
        guard let manager else { throw TunnelManagerError.configurationUnavailable }
        try await manager.loadFromPreferences()
    }

    func applyConfiguration(onDemandEnabled: Bool) {
        guard let manager else { return }
        applyBaseConfiguration(to: manager)
        manager.isOnDemandEnabled = onDemandEnabled
    }

    func savePreferences() async throws {
        guard let manager else { throw TunnelManagerError.configurationUnavailable }
        try await manager.saveToPreferences()
    }

    func startTunnelSession() throws {
        guard let session = manager?.connection as? NETunnelProviderSession else {
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

    func stopTunnelSession() {
        manager?.connection.stopVPNTunnel()
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

    private func finiteLastDisconnectError(
        from connection: NEVPNConnection
    ) async -> TunnelProviderErrorClass? {
        await withCheckedContinuation { continuation in
            connection.fetchLastDisconnectError { error in
                guard let error else {
                    continuation.resume(returning: nil)
                    return
                }
                let nsError = error as NSError
                continuation.resume(returning: TunnelProviderErrorClass.classifyDisconnectError(
                    domain: nsError.domain,
                    code: nsError.code
                ))
            }
        }
    }
}

private extension NEVPNStatus {
    var connectionPhase: TunnelConnectionPhase {
        switch self {
        case .invalid: .invalid
        case .disconnected: .disconnected
        case .connecting: .connecting
        case .connected: .connected
        case .reasserting: .reasserting
        case .disconnecting: .disconnecting
        @unknown default: .invalid
        }
    }
}

private final class NotificationObserverToken: @unchecked Sendable {
    let value: NSObjectProtocol

    init(_ value: NSObjectProtocol) {
        self.value = value
    }
}
