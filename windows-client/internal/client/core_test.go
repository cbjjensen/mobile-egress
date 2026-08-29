package client

import (
	"context"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"mobile-egress/windows-client/internal/relayclient"
	"mobile-egress/windows-client/internal/securestore"
)

type fakeGateway struct {
	mu       sync.Mutex
	identity relayclient.Identity
	tunnel   *fakeTunnel
	issued   []string
	revoked  []string
}

func (gateway *fakeGateway) Enroll(context.Context, string, string, string) (relayclient.Identity, error) {
	return gateway.identity, nil
}

func (gateway *fakeGateway) DialSession(context.Context, relayclient.Identity) (Tunnel, error) {
	return gateway.tunnel, nil
}

func (gateway *fakeGateway) IssuePairing(_ context.Context, _ relayclient.Identity, role string) (relayclient.PairingCode, error) {
	gateway.mu.Lock()
	gateway.issued = append(gateway.issued, role)
	gateway.mu.Unlock()
	return relayclient.PairingCode{Code: "pairing-code", Role: role, ExpiresAt: time.Now().Add(time.Minute)}, nil
}

func (gateway *fakeGateway) Revoke(_ context.Context, _ relayclient.Identity, serial string) error {
	gateway.mu.Lock()
	gateway.revoked = append(gateway.revoked, serial)
	gateway.mu.Unlock()
	return nil
}

type fakeTunnel struct {
	mu      sync.Mutex
	healthy bool
	closed  bool
}

func (tunnel *fakeTunnel) Healthy() bool {
	tunnel.mu.Lock()
	defer tunnel.mu.Unlock()
	return tunnel.healthy && !tunnel.closed
}

func (tunnel *fakeTunnel) OpenStream(context.Context, string, uint16) (io.ReadWriteCloser, error) {
	client, remote := net.Pipe()
	go func() {
		_, _ = io.Copy(remote, remote)
		_ = remote.Close()
	}()
	return client, nil
}

func (tunnel *fakeTunnel) Status() relayclient.SessionStatus {
	healthy := tunnel.Healthy()
	return relayclient.SessionStatus{Connected: healthy, AgentAvailable: healthy}
}

func (tunnel *fakeTunnel) Close() error {
	tunnel.mu.Lock()
	tunnel.closed = true
	tunnel.mu.Unlock()
	return nil
}

func TestCorePairsStartsStopsAndExposesOnlyRedactedStatus(t *testing.T) {
	t.Parallel()

	identity := relayclient.Identity{
		RelayURL: "https://relay.example", Role: "client", Serial: "ABC",
		PrivateKeyPEM: "key", CertificatePEM: "chain", CACertificatePEM: "ca",
	}
	gateway := &fakeGateway{identity: identity, tunnel: &fakeTunnel{healthy: true}}
	core, err := NewCore(context.Background(), securestore.NewMemoryStore(), gateway)
	if err != nil {
		t.Fatal(err)
	}
	if err := core.Pair(context.Background(), identity.RelayURL, "one-use-code", "client"); err != nil {
		t.Fatal(err)
	}
	port := availablePort(t)
	if err := core.StartProxy(port); err != nil {
		t.Fatal(err)
	}
	status := core.Status()
	if !status.Paired || !status.Running || status.Relay != "connected" || !status.AgentAvailable || status.Port != port {
		t.Fatalf("Status() = %#v", status)
	}
	if status.Proxy != "socks5://***:***@127.0.0.1:"+strconv.Itoa(int(port)) {
		t.Fatalf("redacted proxy = %q", status.Proxy)
	}
	proxyLine, err := core.ProxyLine()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(proxyLine, "socks5://") || strings.Contains(proxyLine, "***") {
		t.Fatalf("copyable proxy line = %q", proxyLine)
	}
	if err := core.StopProxy(); err != nil {
		t.Fatal(err)
	}
	if core.Status().Running {
		t.Fatal("proxy remains running after StopProxy")
	}
}

func TestCoreRestrictsOwnerOperationsAndTunnelRole(t *testing.T) {
	t.Parallel()

	owner := relayclient.Identity{
		RelayURL: "https://relay.example", Role: "owner", Serial: "ABC",
		PrivateKeyPEM: "key", CertificatePEM: "chain", CACertificatePEM: "ca",
	}
	gateway := &fakeGateway{identity: owner, tunnel: &fakeTunnel{healthy: true}}
	core, err := NewCore(context.Background(), securestore.NewMemoryStore(), gateway)
	if err != nil {
		t.Fatal(err)
	}
	if err := core.Pair(context.Background(), owner.RelayURL, "owner-code", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := core.StartProxy(availablePort(t)); err == nil {
		t.Fatal("StartProxy accepted an owner identity")
	}
	if _, err := core.IssuePairing(context.Background(), "agent"); err != nil {
		t.Fatal(err)
	}
	if err := core.Revoke(context.Background(), "ABC123"); err != nil {
		t.Fatal(err)
	}

	clientGateway := &fakeGateway{identity: relayclient.Identity{
		RelayURL: owner.RelayURL, Role: "client", Serial: "DEF",
		PrivateKeyPEM: "key", CertificatePEM: "chain", CACertificatePEM: "ca",
	}, tunnel: &fakeTunnel{healthy: true}}
	clientCore, err := NewCore(context.Background(), securestore.NewMemoryStore(), clientGateway)
	if err != nil {
		t.Fatal(err)
	}
	if err := clientCore.Pair(context.Background(), owner.RelayURL, "client-code", "client"); err != nil {
		t.Fatal(err)
	}
	if _, err := clientCore.IssuePairing(context.Background(), "agent"); err == nil {
		t.Fatal("client identity issued an owner pairing code")
	}
}

func availablePort(t *testing.T) uint16 {
	t.Helper()
	listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	port := uint16(listener.Addr().(*net.TCPAddr).Port)
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}
