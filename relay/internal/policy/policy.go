// Package policy validates destinations accepted by the relay.
package policy

import (
	"fmt"
	"net/netip"
)

var nonPublicPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001:2::/48"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("ff00::/8"),
}

// ValidatePublicTCPDestination confirms that address is a public unicast IP and
// port is a valid TCP port number. Callers must resolve host names before using
// this function.
func ValidatePublicTCPDestination(address netip.Addr, port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("TCP port %d is outside the valid range", port)
	}

	address = address.Unmap()
	if !address.IsValid() || !address.IsGlobalUnicast() || address.IsPrivate() {
		return fmt.Errorf("destination address is not public")
	}

	for _, prefix := range nonPublicPrefixes {
		if prefix.Contains(address) {
			return fmt.Errorf("destination address is not public")
		}
	}

	return nil
}
