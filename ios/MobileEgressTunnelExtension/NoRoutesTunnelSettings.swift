import Foundation
import NetworkExtension

enum NoRoutesTunnelSettings {
    static func make() -> NEPacketTunnelNetworkSettings {
        let settings = NEPacketTunnelNetworkSettings(tunnelRemoteAddress: "127.0.0.1")

        let ipv4 = NEIPv4Settings(
            addresses: ["192.0.2.1"],
            subnetMasks: ["255.255.255.255"]
        )
        ipv4.includedRoutes = []
        settings.ipv4Settings = ipv4

        let ipv6 = NEIPv6Settings(
            addresses: ["2001:db8::1"],
            networkPrefixLengths: [NSNumber(value: 128)]
        )
        ipv6.includedRoutes = []
        settings.ipv6Settings = ipv6
        settings.mtu = NSNumber(value: 1_280)
        return settings
    }
}
