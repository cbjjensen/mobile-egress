package client

import (
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"mobile-egress/pairing"
	"mobile-egress/windows-client/internal/relayclient"
	"mobile-egress/windows-client/internal/securestore"
)

func TestCoreOwnerBootstrapCreatesClientForProxyAndRetainsOwnerControls(t *testing.T) {
	t.Parallel()

	owner := testIdentity("owner", "OWNER")
	clientIdentity := testIdentity("client", "CLIENT")
	gateway := &bootstrapGateway{owner: owner, client: clientIdentity, tunnel: &fakeTunnel{healthy: true}}
	store := securestore.NewMemoryStore()
	core, err := NewCore(context.Background(), store, gateway)
	if err != nil {
		t.Fatal(err)
	}

	if err := core.Pair(context.Background(), pairing.Bundle{RelayURL: owner.RelayURL, Role: "owner"}); err != nil {
		t.Fatal(err)
	}
	if err := core.StartProxy(availablePort(t)); err != nil {
		t.Fatalf("StartProxy() after owner bootstrap = %v", err)
	}
	if _, err := core.IssuePairing(context.Background(), "agent"); err != nil {
		t.Fatalf("IssuePairing() after owner bootstrap = %v", err)
	}
	if gateway.sessionSerial != clientIdentity.Serial {
		t.Fatalf("proxy session used serial %q, want client serial %q", gateway.sessionSerial, clientIdentity.Serial)
	}
	if gateway.issueSerial != owner.Serial {
		t.Fatalf("owner control used serial %q, want owner serial %q", gateway.issueSerial, owner.Serial)
	}

	repository := NewRepository(store)
	identities, ok := any(repository).(interface {
		LoadOwnerIdentity(context.Context) (relayclient.Identity, uint16, error)
		LoadClientIdentity(context.Context) (relayclient.Identity, uint16, error)
	})
	if !ok {
		t.Fatal("Repository does not expose independent owner and client identity loads")
	}
	if got, _, err := identities.LoadOwnerIdentity(context.Background()); err != nil || got != owner {
		t.Fatalf("stored owner = %#v, %v; want %#v", got, err, owner)
	}
	if got, _, err := identities.LoadClientIdentity(context.Background()); err != nil || got != clientIdentity {
		t.Fatalf("stored client = %#v, %v; want %#v", got, err, clientIdentity)
	}
}

func TestCoreRetriesClientSetupWithoutReenrollingOwner(t *testing.T) {
	t.Parallel()

	owner := testIdentity("owner", "OWNER")
	clientIdentity := testIdentity("client", "CLIENT")
	gateway := &bootstrapGateway{owner: owner, client: clientIdentity, tunnel: &fakeTunnel{healthy: true}, failClientEnroll: true}
	core, err := NewCore(context.Background(), securestore.NewMemoryStore(), gateway)
	if err != nil {
		t.Fatal(err)
	}
	if err := core.Pair(context.Background(), pairing.Bundle{RelayURL: owner.RelayURL, Role: "owner"}); err == nil {
		t.Fatal("Pair() succeeded when automatic client enrollment failed")
	}
	if gateway.ownerEnrollments != 1 {
		t.Fatalf("owner enrollments = %d, want 1", gateway.ownerEnrollments)
	}

	gateway.failClientEnroll = false
	retrier, ok := any(core).(interface{ RetryClientSetup(context.Context) error })
	if !ok {
		t.Fatal("Core does not expose RetryClientSetup")
	}
	if err := retrier.RetryClientSetup(context.Background()); err != nil {
		t.Fatalf("RetryClientSetup() = %v", err)
	}
	if gateway.ownerEnrollments != 1 {
		t.Fatalf("retry re-enrolled owner %d times", gateway.ownerEnrollments)
	}
	if gateway.clientEnrollments != 2 {
		t.Fatalf("client enrollments = %d, want 2", gateway.clientEnrollments)
	}
	if err := core.StartProxy(availablePort(t)); err != nil {
		t.Fatalf("StartProxy() after retry = %v", err)
	}
}

func TestCoreRejectsRepeatedOwnerBootstrapWithoutStoppingClientProxy(t *testing.T) {
	t.Parallel()

	owner := testIdentity("owner", "OWNER")
	gateway := &bootstrapGateway{owner: owner, client: testIdentity("client", "CLIENT"), tunnel: &fakeTunnel{healthy: true}}
	core, err := NewCore(context.Background(), securestore.NewMemoryStore(), gateway)
	if err != nil {
		t.Fatal(err)
	}
	bundle := pairing.Bundle{RelayURL: owner.RelayURL, Role: "owner"}
	if err := core.BootstrapOwner(context.Background(), bundle); err != nil {
		t.Fatal(err)
	}
	if err := core.StartProxy(availablePort(t)); err != nil {
		t.Fatal(err)
	}

	if err := core.BootstrapOwner(context.Background(), bundle); err == nil {
		t.Fatal("repeated BootstrapOwner() succeeded")
	}
	if !core.Status().Running {
		t.Fatal("rejected repeated bootstrap stopped the active client proxy")
	}
}

func testIdentity(role, serial string) relayclient.Identity {
	return relayclient.Identity{
		RelayURL: "https://relay.example", Role: role, Serial: serial,
		PrivateKeyPEM: role + "-key", CertificatePEM: role + "-chain", CACertificatePEM: "ca",
	}
}

type bootstrapGateway struct {
	mu                sync.Mutex
	owner             relayclient.Identity
	client            relayclient.Identity
	tunnel            *fakeTunnel
	failClientEnroll  bool
	ownerEnrollments  int
	clientEnrollments int
	sessionSerial     string
	issueSerial       string
}

func (gateway *bootstrapGateway) Enroll(_ context.Context, bundle pairing.Bundle) (relayclient.Identity, error) {
	gateway.mu.Lock()
	defer gateway.mu.Unlock()
	switch bundle.Role {
	case "owner":
		gateway.ownerEnrollments++
		return gateway.owner, nil
	case "client":
		gateway.clientEnrollments++
		if gateway.failClientEnroll {
			return relayclient.Identity{}, errors.New("client enrollment unavailable")
		}
		return gateway.client, nil
	default:
		return relayclient.Identity{}, errors.New("unexpected enrollment role")
	}
}

func (gateway *bootstrapGateway) DialSession(_ context.Context, identity relayclient.Identity) (Tunnel, error) {
	gateway.mu.Lock()
	gateway.sessionSerial = identity.Serial
	gateway.mu.Unlock()
	return gateway.tunnel, nil
}

func (gateway *bootstrapGateway) IssuePairing(_ context.Context, identity relayclient.Identity, role string) (relayclient.PairingCode, error) {
	gateway.mu.Lock()
	gateway.issueSerial = identity.Serial
	gateway.mu.Unlock()
	return relayclient.PairingCode{Code: "client-pairing-code", Role: role, ExpiresAt: time.Now().Add(time.Minute)}, nil
}

func (gateway *bootstrapGateway) Revoke(context.Context, relayclient.Identity, string) error {
	return nil
}

type fakeGateway struct {
	mu       sync.Mutex
	identity relayclient.Identity
	tunnel   *fakeTunnel
	issued   []string
	revoked  []string
}

func (gateway *fakeGateway) Enroll(context.Context, pairing.Bundle) (relayclient.Identity, error) {
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

func TestCoreStartsStopsAndExposesOnlyRedactedStatusForStoredClient(t *testing.T) {
	t.Parallel()

	identity := relayclient.Identity{
		RelayURL: "https://relay.example", Role: "client", Serial: "ABC",
		PrivateKeyPEM: "key", CertificatePEM: "chain", CACertificatePEM: "ca",
	}
	gateway := &fakeGateway{identity: identity, tunnel: &fakeTunnel{healthy: true}}
	store := securestore.NewMemoryStore()
	if err := NewRepository(store).SaveClientIdentity(context.Background(), identity); err != nil {
		t.Fatal(err)
	}
	core, err := NewCore(context.Background(), store, gateway)
	if err != nil {
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

func TestCoreRequiresOwnerForControlAndClientForProxy(t *testing.T) {
	t.Parallel()

	owner := relayclient.Identity{
		RelayURL: "https://relay.example", Role: "owner", Serial: "ABC",
		PrivateKeyPEM: "key", CertificatePEM: "chain", CACertificatePEM: "ca",
	}
	ownerStore := securestore.NewMemoryStore()
	if err := NewRepository(ownerStore).SaveOwnerIdentity(context.Background(), owner); err != nil {
		t.Fatal(err)
	}
	core, err := NewCore(context.Background(), ownerStore, &fakeGateway{identity: owner, tunnel: &fakeTunnel{healthy: true}})
	if err != nil {
		t.Fatal(err)
	}
	if err := core.StartProxy(availablePort(t)); err == nil {
		t.Fatal("StartProxy accepted an owner-only installation")
	}

	clientIdentity := relayclient.Identity{
		RelayURL: owner.RelayURL, Role: "client", Serial: "DEF",
		PrivateKeyPEM: "key", CertificatePEM: "chain", CACertificatePEM: "ca",
	}
	clientStore := securestore.NewMemoryStore()
	if err := NewRepository(clientStore).SaveClientIdentity(context.Background(), clientIdentity); err != nil {
		t.Fatal(err)
	}
	clientCore, err := NewCore(context.Background(), clientStore, &fakeGateway{identity: clientIdentity, tunnel: &fakeTunnel{healthy: true}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := clientCore.IssuePairing(context.Background(), "agent"); err == nil {
		t.Fatal("client identity issued an owner pairing code")
	}
	if err := clientCore.Revoke(context.Background(), "ABC123"); err == nil {
		t.Fatal("client identity revoked a device")
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
