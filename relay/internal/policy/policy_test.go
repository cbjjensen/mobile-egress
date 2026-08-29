package policy

import (
	"net/netip"
	"testing"
)

func TestValidatePublicTCPDestinationAcceptsPublicAddresses(t *testing.T) {
	t.Parallel()

	for _, address := range []string{"1.1.1.1", "2606:4700:4700::1111"} {
		address := address
		t.Run(address, func(t *testing.T) {
			t.Parallel()

			if err := ValidatePublicTCPDestination(netip.MustParseAddr(address), 443); err != nil {
				t.Fatalf("ValidatePublicTCPDestination(%s, 443) returned an error: %v", address, err)
			}
		})
	}
}

func TestValidatePublicTCPAddressPreservesDestinationPolicyAPI(t *testing.T) {
	t.Parallel()

	if err := ValidatePublicTCPAddress(netip.MustParseAddr("1.1.1.1"), 443); err != nil {
		t.Fatalf("ValidatePublicTCPAddress() rejected a public target: %v", err)
	}
	if err := ValidatePublicTCPAddress(netip.MustParseAddr("127.0.0.1"), 443); err == nil {
		t.Fatal("ValidatePublicTCPAddress() accepted loopback")
	}
}

func TestValidatePublicTCPDestinationRejectsNonPublicAddresses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		address netip.Addr
	}{
		{name: "invalid", address: netip.Addr{}},
		{name: "private IPv4", address: netip.MustParseAddr("192.168.1.1")},
		{name: "carrier grade NAT", address: netip.MustParseAddr("100.64.0.1")},
		{name: "loopback", address: netip.MustParseAddr("127.0.0.1")},
		{name: "link local", address: netip.MustParseAddr("169.254.1.1")},
		{name: "multicast", address: netip.MustParseAddr("224.0.0.1")},
		{name: "documentation IPv4", address: netip.MustParseAddr("192.0.2.1")},
		{name: "benchmark", address: netip.MustParseAddr("198.18.0.1")},
		{name: "documentation IPv6", address: netip.MustParseAddr("2001:db8::1")},
		{name: "IPv4 mapped IPv6", address: netip.MustParseAddr("::ffff:1.1.1.1")},
		{name: "NAT64 local use IPv6", address: netip.MustParseAddr("64:ff9b:1::c000:201")},
		{name: "NAT64 well-known IPv6", address: netip.MustParseAddr("64:ff9b::c000:201")},
		{name: "Teredo IPv6", address: netip.MustParseAddr("2001:0::1")},
		{name: "6to4 IPv6", address: netip.MustParseAddr("2002::1")},
		{name: "IANA reserved IPv6", address: netip.MustParseAddr("3fff::1")},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if err := ValidatePublicTCPDestination(test.address, 443); err == nil {
				t.Fatal("ValidatePublicTCPDestination accepted a non-public address")
			}
		})
	}
}

func TestValidatePublicTCPDestinationRejectsOutOfRangePorts(t *testing.T) {
	t.Parallel()

	address := netip.MustParseAddr("1.1.1.1")
	for _, port := range []int{0, 65536} {
		port := port
		t.Run("port", func(t *testing.T) {
			t.Parallel()

			if err := ValidatePublicTCPDestination(address, port); err == nil {
				t.Fatalf("ValidatePublicTCPDestination accepted port %d", port)
			}
		})
	}
}
