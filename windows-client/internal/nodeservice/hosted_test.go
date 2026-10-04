package nodeservice

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"testing"

	"mobile-egress/internal/hostedgateway"
	"mobile-egress/windows-client/internal/securestore"
)

func TestHostedConfigurationPreservesIdentityAndWireMode(t *testing.T) {
	m, _, _ := directTestConfigured(t)
	id, ca, password := m.state.ClientID, m.state.CACertificatePEM, m.state.Password
	generation := m.state.Generation
	m.state.Hosted = &hostedState{DeviceID: "device", DeviceToken: "token", BrokerEndpoint: "https://broker.example", GatewayHostname: "route.example", GatewayPort: 443}
	if err := m.configureHostedLocked(context.Background(), "Workload"); err != nil {
		t.Fatal(err)
	}
	if m.state.ClientID != id || m.state.CACertificatePEM != ca || m.state.Password != password {
		t.Fatal("identity replaced")
	}
	if m.state.Generation != generation+1 || m.state.Configuration.Transport != "hosted" {
		t.Fatal("mode did not advance generation")
	}
	bundle, err := m.IssueInvitation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(bundle)
	if !strings.Contains(string(raw), `"transport":"hosted"`) {
		t.Fatalf("missing mode: %s", raw)
	}
	status, _ := json.Marshal(m.Status())
	if strings.Contains(string(status), "token") || m.Status().Connected {
		t.Fatal("unsafe or false phone status")
	}
}

func TestExistingConfigurationRemainsDirectAndFreshStatusDefaultsHosted(t *testing.T) {
	fresh := NewDirect(NewRepository(securestore.NewMemoryStore()), "windows", "amd64", "test")
	if err := fresh.ensureLocked(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fresh.Status().Transport != "hosted" {
		t.Fatal("fresh setup must default hosted")
	}
	m, _, _ := directTestConfigured(t)
	if m.Status().Transport != "direct" {
		t.Fatal("existing direct configuration changed")
	}
	raw, _ := json.Marshal(m.state.Invitation)
	if strings.Contains(string(raw), `"transport"`) {
		t.Fatal("direct compatibility field must be omitted")
	}
}

func TestTransportConfigurationRejectsNullUnknownAndDuplicateModes(t *testing.T) {
	for _, raw := range []string{`{"transport":null,"endpoint":"https://route.example","displayName":"Workload"}`, `{"transport":"automatic","endpoint":"https://route.example","displayName":"Workload"}`, `{"transport":"direct","transport":"hosted","endpoint":"https://route.example","displayName":"Workload"}`, `{"Transport":"hosted","endpoint":"https://route.example","displayName":"Workload"}`} {
		if _, err := DecodeDirectConfiguration(raw); err == nil {
			t.Fatalf("invalid mode accepted: %s", raw)
		}
	}
}

type updateTunnel struct {
	bundle string
	closed bool
}

type healthyUpdateTunnel struct{ updateTunnel }

func (t *healthyUpdateTunnel) Healthy() bool { return !t.closed }

func TestMigrationVerificationRequiresCurrentModeGenerationPhoneSession(t *testing.T) {
	m, i, csr := directTestConfigured(t)
	if r := directTestRequest(m, "/v2/direct/enroll", directEnrollBody(i, csr), nil); r.Code != 201 {
		t.Fatal(r.Body.String())
	}
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.state.Pairing.Acknowledged = true
	m.state.AcknowledgedGeneration = m.state.Generation
	m.state.AcknowledgedEndpoint = m.state.Configuration.Endpoint
	oldGeneration := m.state.Generation
	m.mu.Lock()
	m.status.Running = true
	m.sessionGeneration = oldGeneration
	m.sessionTransport = "direct"
	m.mu.Unlock()
	phone := &healthyUpdateTunnel{}
	m.opener.swap(phone)
	m.refreshLocked()
	if !m.Status().Connected {
		t.Fatal("valid existing direct phone not connected")
	}
	m.state.Hosted = &hostedState{DeviceID: "device", DeviceToken: "token", BrokerEndpoint: "https://broker.example", GatewayHostname: "route.example", GatewayPort: 443}
	if err := m.configureHostedLocked(context.Background(), "Workload"); err != nil {
		t.Fatal(err)
	}
	if m.Status().Connected || phone.closed {
		t.Fatal("old direct tunnel verified new hosted mode or was discarded")
	}
	m.state.AcknowledgedGeneration = m.state.Generation
	m.refreshLocked()
	if m.Status().Connected {
		t.Fatal("ACK alone verified the old direct session")
	}
	m.mu.Lock()
	m.sessionGeneration = m.state.Generation
	m.sessionTransport = "hosted"
	m.mu.Unlock()
	if !m.Status().Connected {
		t.Fatal("current hosted authenticated acknowledged session not connected")
	}
}

func TestHostedAuthorizationRejectionIsSafeAndDoesNotUnpairPhone(t *testing.T) {
	m, i, csr := directTestConfigured(t)
	if r := directTestRequest(m, "/v2/direct/enroll", directEnrollBody(i, csr), nil); r.Code != 201 {
		t.Fatal(r.Body.String())
	}
	m.opMu.Lock()
	m.state.Pairing.Acknowledged = true
	m.state.AcknowledgedGeneration = m.state.Generation
	m.state.Hosted = &hostedState{DeviceID: "device", DeviceToken: "token", BrokerEndpoint: "https://broker.example", GatewayHostname: "route.example", GatewayPort: 443}
	if err := m.configureHostedLocked(context.Background(), "Workload"); err != nil {
		m.opMu.Unlock()
		t.Fatal(err)
	}
	pairing := m.state.Pairing.ID
	generation := m.state.Generation
	m.gatewayEpoch = 1
	m.opMu.Unlock()
	m.recordGatewayStatus(generation, 1, hostedgateway.Status{State: "disconnected", Reason: "gateway activation rejected"})
	status := m.Status()
	if status.GatewayState != "authorization_rejected" || status.ActivationState != "access_rejected" || !status.Paired || m.state.Pairing.ID != pairing || m.state.Pairing.Revoked {
		t.Fatalf("rejection erased pairing or hid access state: %+v", status)
	}
	m.recordGatewayStatus(generation, 1, hostedgateway.Status{State: "disconnected", Reason: "secret-token-should-not-leak"})
	raw, _ := json.Marshal(m.Status())
	if strings.Contains(string(raw), "secret-token") {
		t.Fatal("gateway reason leaked")
	}
}

func (t *updateTunnel) Done() <-chan struct{} { return make(chan struct{}) }
func (t *updateTunnel) Healthy() bool         { return false }
func (t *updateTunnel) OpenStream(context.Context, string, uint16) (io.ReadWriteCloser, error) {
	return nil, errors.New("offline")
}
func (t *updateTunnel) Close() error                           { t.closed = true; return nil }
func (t *updateTunnel) SendEndpointUpdate(bundle string) error { t.bundle = bundle; return nil }

func TestHostedMigrationUsesVirtualListenerAndNotifiesExistingPhone(t *testing.T) {
	m, i, csr := directTestConfigured(t)
	if r := directTestRequest(m, "/v2/direct/enroll", directEnrollBody(i, csr), nil); r.Code != 201 {
		t.Fatal(r.Body.String())
	}
	tunnel := &updateTunnel{}
	m.opener.swap(tunnel)
	m.opMu.Lock()
	defer m.opMu.Unlock()
	blocker, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Close()
	m.state.Configuration.BindAddress = blocker.Addr().String()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m.runCtx = ctx
	m.state.Hosted = &hostedState{DeviceID: "12345678-1234-4234-8234-123456789012", DeviceToken: "med1." + base64.RawURLEncoding.EncodeToString(make([]byte, 32)), BrokerEndpoint: "https://broker.example", GatewayHostname: "route.example", GatewayPort: 443}
	if err := m.configureHostedLocked(context.Background(), "Workload"); err != nil {
		t.Fatal(err)
	}
	if m.listener == nil || m.listener.Addr().Network() != "hostedgateway" || m.state.Configuration.BindAddress != "" {
		t.Fatal("public workload port bound")
	}
	if tunnel.bundle == "" || tunnel.closed {
		t.Fatal("lost live signed update before phone migration")
	}
	if m.Status().Connected {
		t.Fatal("gateway listener claimed phone connection")
	}
	if m.server != nil {
		m.server.Close()
	}
	m.listener.Close()
}
