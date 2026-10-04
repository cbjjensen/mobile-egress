import NetworkExtension

/// Upgrade compatibility only. Old on-demand profiles must never resume serving.
final class PacketTunnelProvider: NEPacketTunnelProvider, @unchecked Sendable {
    override func startTunnel(options: [String: NSObject]? = nil, completionHandler: @escaping @Sendable (Error?) -> Void) {
        completionHandler(NSError(domain: "com.mobileegress.agent.retired-vpn", code: 1, userInfo: [NSLocalizedDescriptionKey: "Open Mobile Egress to remove the retired VPN configuration and pair direct Clients."]))
    }
    override func stopTunnel(with reason: NEProviderStopReason, completionHandler: @escaping @Sendable () -> Void) { completionHandler() }
    override func handleAppMessage(_ messageData: Data, completionHandler: ((Data?) -> Void)? = nil) { completionHandler?(nil) }
}