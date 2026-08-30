package tailscale

import (
	"reflect"
	"testing"
)

func TestParseStatusRequiresOnlineTsNetIdentity(t *testing.T) {
	t.Parallel()

	status, err := ParseStatus([]byte(`{
        "BackendState":"Running",
        "Self":{"DNSName":"friends-bridge.tail123.ts.net.","Online":true},
        "MagicDNSSuffix":"tail123.ts.net"
    }`))
	if err != nil {
		t.Fatal(err)
	}
	if !status.Online || status.BackendState != "Running" || status.FQDN != "friends-bridge.tail123.ts.net" || status.PublicURL != "https://friends-bridge.tail123.ts.net:8443" {
		t.Fatalf("ParseStatus() = %#v", status)
	}
	for name, raw := range map[string]string{
		"offline":      `{"BackendState":"Running","Self":{"DNSName":"node.tail.ts.net.","Online":false}}`,
		"wrong suffix": `{"BackendState":"Running","Self":{"DNSName":"node.example.com.","Online":true}}`,
		"malformed":    `{`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseStatus([]byte(raw)); err == nil {
				t.Fatalf("ParseStatus() accepted %s", name)
			}
		})
	}
}

func TestRawTCPFunnelAndUnattendedCommandsAreExact(t *testing.T) {
	t.Parallel()

	if got, want := FunnelArguments(), []string{"funnel", "--bg", "--yes", "--tcp=8443", "tcp://127.0.0.1:8443"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("FunnelArguments() = %#v, want %#v", got, want)
	}
	if got, want := UnattendedArguments(), []string{"up", "--unattended=true"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("UnattendedArguments() = %#v, want %#v", got, want)
	}
}

func TestParseFunnelStatusRequiresExactRawTCPMapping(t *testing.T) {
	t.Parallel()

	valid := []byte(`{"TCP":{"8443":{"TCPForward":"127.0.0.1:8443"}},"AllowFunnel":{"bridge.tail123.ts.net:8443":true}}`)
	if ready, err := ParseFunnelStatus(valid, "bridge.tail123.ts.net"); err != nil || !ready {
		t.Fatalf("ParseFunnelStatus(valid) = %v/%v", ready, err)
	}
	for name, raw := range map[string]string{
		"reset":              `{}`,
		"wrong target":       `{"TCP":{"8443":{"TCPForward":"127.0.0.1:9443"}},"AllowFunnel":{"bridge.tail123.ts.net:8443":true}}`,
		"TLS terminated":     `{"TCP":{"8443":{"TCPForward":"127.0.0.1:8443","TerminateTLS":"bridge.tail123.ts.net"}},"AllowFunnel":{"bridge.tail123.ts.net:8443":true}}`,
		"Funnel not allowed": `{"TCP":{"8443":{"TCPForward":"127.0.0.1:8443"}},"AllowFunnel":{}}`,
	} {
		t.Run(name, func(t *testing.T) {
			if ready, err := ParseFunnelStatus([]byte(raw), "bridge.tail123.ts.net"); err != nil || ready {
				t.Fatalf("ParseFunnelStatus(%s) = %v/%v, want not ready", name, ready, err)
			}
		})
	}
}

func TestParseStablePackagePageSelectsNewestAMD64MSI(t *testing.T) {
	t.Parallel()

	page := []byte(`
        <a href="tailscale-setup-1.98.2-amd64.msi">old</a>
        <a href="tailscale-setup-1.100.1-amd64.msi">new</a>
        <a href="tailscale-setup-1.100.1-arm64.msi">wrong architecture</a>
    `)
	release, err := ParseStablePackagePage(page)
	if err != nil {
		t.Fatal(err)
	}
	if release.Version != "1.100.1" || release.MSIURL != "https://pkgs.tailscale.com/stable/tailscale-setup-1.100.1-amd64.msi" || release.ChecksumURL != release.MSIURL+".sha256" {
		t.Fatalf("release = %#v", release)
	}
}

func TestParseSHA256RequiresExpectedFilenameAndDigest(t *testing.T) {
	t.Parallel()

	const digest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	got, err := ParseSHA256([]byte(digest+"  tailscale-setup-1.100.1-amd64.msi\n"), "tailscale-setup-1.100.1-amd64.msi")
	if err != nil || got != digest {
		t.Fatalf("ParseSHA256() = %q/%v", got, err)
	}
	if _, err := ParseSHA256([]byte(digest+"  another.msi\n"), "tailscale-setup-1.100.1-amd64.msi"); err == nil {
		t.Fatal("ParseSHA256() accepted a checksum for another file")
	}
}

func TestParseSHA256AcceptsDigestOnlyResponse(t *testing.T) {
	t.Parallel()

	const digest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	got, err := ParseSHA256([]byte(digest+"\n"), "tailscale-setup-1.102.3-amd64.msi")
	if err != nil || got != digest {
		t.Fatalf("ParseSHA256() = %q/%v", got, err)
	}
}
