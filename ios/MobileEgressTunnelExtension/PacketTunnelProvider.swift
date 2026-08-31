import Foundation
import MobileEgressCore
import NetworkExtension

final class PacketTunnelProvider: NEPacketTunnelProvider, @unchecked Sendable {
    private let runtimeController = TunnelRuntimeController()

    override func startTunnel(
        options: [String: NSObject]? = nil,
        completionHandler: @escaping @Sendable (Error?) -> Void
    ) {
        Task { [weak self, runtimeController] in
            guard let self else {
                completionHandler(TunnelProviderErrorClass.runtimeUnavailable.providerNSError)
                return
            }
            guard let generation = await runtimeController.beginStart() else {
                completionHandler(TunnelProviderErrorClass.runtimeUnavailable.providerNSError)
                return
            }

            do {
                let runtime = try makeRuntime(generation: generation)
                do {
                    try await setTunnelNetworkSettings(NoRoutesTunnelSettings.make())
                } catch {
                    throw ProviderStartFailure.tunnelSettings
                }
                guard await runtimeController.installAndStart(runtime, generation: generation) else {
                    throw ProviderStartFailure.runtimeUnavailable
                }
                completionHandler(nil)
            } catch {
                let classification = ProviderStartFailure.classify(error)
                await runtimeController.fail(classification, generation: generation)
                completionHandler(classification.providerNSError)
            }
        }
    }

    override func stopTunnel(
        with reason: NEProviderStopReason,
        completionHandler: @escaping @Sendable () -> Void
    ) {
        Task { [runtimeController] in
            await runtimeController.stop()
            completionHandler()
        }
    }

    override func handleAppMessage(_ messageData: Data, completionHandler: ((Data?) -> Void)? = nil) {
        guard let completionHandler else { return }
        let response = ProviderResponseHandler(completionHandler)
        Task { [runtimeController, response] in
            let messageError: TunnelProviderErrorClass?
            do {
                try TunnelProviderMessageCodec.decodeStatusRequest(messageData)
                messageError = nil
            } catch {
                messageError = .invalidMessage
            }
            let status = await runtimeController.status(overriding: messageError)
            response.call(try? TunnelProviderMessageCodec.encodeStatus(status))
        }
    }

    private func makeRuntime(generation: UInt64) throws -> AgentSessionRuntime {
        let configuration: MobileEgressSystemConfiguration
        do {
            configuration = try ExtensionConfiguration.load()
        } catch {
            throw ProviderStartFailure.invalidConfiguration
        }
        guard Bundle.main.bundleIdentifier == configuration.providerBundleIdentifier else {
            throw ProviderStartFailure.invalidConfiguration
        }

        let identityStore: SharedKeychainIdentityStore
        let identity: AgentIdentity
        do {
            identityStore = try SharedKeychainIdentityStore(
                accessGroup: configuration.keychainAccessGroup
            )
            guard let loaded = try identityStore.load() else {
                throw ProviderStartFailure.identityUnavailable
            }
            identity = loaded
        } catch let failure as ProviderStartFailure {
            throw failure
        } catch {
            throw ProviderStartFailure.identityUnavailable
        }

        let relayConfiguration: RelayWebSocketConfiguration
        do {
            relayConfiguration = try RelayWebSocketConfiguration(identity: identity)
        } catch {
            throw ProviderStartFailure.invalidConfiguration
        }
        let relay = NetworkRelayWebSocket(
            configuration: relayConfiguration,
            identityResolver: identityStore
        )
        return AgentSessionRuntime(
            relay: relay,
            targetFactory: NetworkTargetConnectionFactory(),
            terminalFailureHandler: { [weak self] failure in
                self?.handleTerminalFailure(failure, generation: generation)
            }
        )
    }

    private func handleTerminalFailure(
        _ failure: AgentRuntimeErrorClass,
        generation: UInt64
    ) {
        Task { [weak self] in
            guard let self else { return }
            await runtimeController.handleTerminalFailure(failure, generation: generation) { [weak self] error in
                self?.cancelTunnelWithError(error.providerNSError)
            }
        }
    }
}

private enum ProviderStartFailure: Error, Sendable {
    case identityUnavailable
    case invalidConfiguration
    case tunnelSettings
    case runtimeUnavailable

    static func classify(_ error: Error) -> TunnelProviderErrorClass {
        switch error as? ProviderStartFailure {
        case .identityUnavailable: .identityUnavailable
        case .invalidConfiguration: .invalidConfiguration
        case .tunnelSettings: .tunnelSettings
        case .runtimeUnavailable, nil: .runtimeUnavailable
        }
    }
}

private extension TunnelProviderErrorClass {
    var providerNSError: NSError {
        return NSError(
            domain: TunnelProviderErrorClass.providerErrorDomain,
            code: providerErrorCode,
            userInfo: [NSLocalizedDescriptionKey: userMessage ?? "No provider error."]
        )
    }
}

private final class ProviderResponseHandler: @unchecked Sendable {
    private let handler: (Data?) -> Void

    init(_ handler: @escaping (Data?) -> Void) {
        self.handler = handler
    }

    func call(_ data: Data?) {
        handler(data)
    }
}
