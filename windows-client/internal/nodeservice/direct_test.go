package nodeservice

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"mobile-egress/windows-client/internal/securestore"
)

func TestDirectListeningAndAwaitingPhoneDoNotClaimExternalReachability(t *testing.T) {
	m, _, _ := directTestConfigured(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	m.opMu.Lock()
	m.listener = listener
	m.mu.Lock()
	m.status.Running = true
	m.mu.Unlock()
	m.refreshLocked()
	m.opMu.Unlock()
	if s := m.Status(); s.Phase != "awaiting_phone" || s.Connected {
		t.Fatalf("invitation status = %#v", s)
	}
	if err := m.CancelInvitation(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s := m.Status(); s.Phase != "listening" || s.Connected {
		t.Fatalf("listener status = %#v", s)
	}
}

func directTestConfigured(t *testing.T) (*Direct, directInvitation, string) {
	t.Helper()
	m := NewDirect(NewRepository(securestore.NewMemoryStore()), "windows", "amd64", "2.0.0")
	if err := m.Configure(context.Background(), DirectConfiguration{Endpoint: "https://client.example:8443", DisplayName: "Client"}); err != nil {
		t.Fatal(err)
	}
	bundle, err := m.IssueInvitation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(bundle)
	var invitation directInvitation
	_ = json.Unmarshal(raw, &invitation)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, key)
	return m, invitation, string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}))
}
func directTestRequest(m *Direct, path string, body any, identity *directIdentityResponse) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest(http.MethodPost, "https://client.example:8443"+path, bytes.NewReader(raw))
	if identity != nil {
		block, _ := pem.Decode([]byte(identity.CertificatePEM))
		cert, _ := x509.ParseCertificate(block.Bytes)
		r.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}, VerifiedChains: [][]*x509.Certificate{{cert}}}
	}
	w := httptest.NewRecorder()
	m.handler().ServeHTTP(w, r)
	return w
}
func directEnrollBody(i directInvitation, csr string) any {
	return map[string]any{"clientId": i.ClientID, "invitationId": i.InvitationID, "code": i.Capability, "role": "agent", "csrPem": csr}
}
func TestDirectEnrollmentRecoveryExpiryAndRevocation(t *testing.T) {
	m, i, csr := directTestConfigured(t)
	w := directTestRequest(m, "/v2/direct/enroll", directEnrollBody(i, csr), nil)
	if w.Code != 201 {
		t.Fatalf("enroll=%d %s", w.Code, w.Body.String())
	}
	var identity directIdentityResponse
	if err := json.Unmarshal(w.Body.Bytes(), &identity); err != nil {
		t.Fatal(err)
	}
	m.opMu.Lock()
	next := m.cloneLocked()
	next.Invitation.ExpiresAt = time.Now().Add(-time.Hour)
	if err := m.saveLocked(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	m.opMu.Unlock()
	w = directTestRequest(m, "/v2/direct/enroll", directEnrollBody(i, csr), nil)
	if w.Code != 201 {
		t.Fatal("same CSR recovery failed after expiry", w.Code)
	}
	_, _, otherCSR := directTestConfigured(t)
	w = directTestRequest(m, "/v2/direct/enroll", directEnrollBody(i, otherCSR), nil)
	if w.Code == 201 {
		t.Fatal("different key reused invitation")
	}
	ack := map[string]any{"clientId": i.ClientID, "pairingId": identity.PairingID, "generation": identity.Generation}
	w = directTestRequest(m, "/v2/direct/ack", ack, &identity)
	if w.Code != 200 || !m.Status().Paired {
		t.Fatal("ack failed", w.Code)
	}
	if err := m.Revoke(context.Background()); err != nil {
		t.Fatal(err)
	}
	w = directTestRequest(m, "/v2/direct/ack", ack, &identity)
	if w.Code != 401 {
		t.Fatal("revoked identity admitted", w.Code)
	}
	restored := NewDirect(m.repository, "windows", "amd64", "2.0.0")
	w = directTestRequest(restored, "/v2/direct/ack", ack, &identity)
	if w.Code != 401 {
		t.Fatal("revocation lost after restart", w.Code)
	}
}
func TestDirectExpiredCanceledAndMalformedInvitationAdmission(t *testing.T) {
	m, i, csr := directTestConfigured(t)
	m.opMu.Lock()
	next := m.cloneLocked()
	next.Invitation.ExpiresAt = time.Now().Add(-time.Second)
	_ = m.saveLocked(context.Background(), next)
	m.opMu.Unlock()
	if w := directTestRequest(m, "/v2/direct/enroll", directEnrollBody(i, csr), nil); w.Code == 201 {
		t.Fatal("expired invitation admitted")
	}
	if err := m.CancelInvitation(context.Background()); err != nil {
		t.Fatal(err)
	}
	if w := directTestRequest(m, "/v2/direct/enroll", directEnrollBody(i, csr), nil); w.Code == 201 {
		t.Fatal("canceled invitation admitted")
	}
	for _, raw := range []string{`{"a":1,"a":2}`, `{"a":1} {}`, `{"unknown":1}`, `{"A":1}`, "\xff"} {
		var value struct {
			A int `json:"a"`
		}
		if directStrictJSON([]byte(raw), &value) == nil {
			t.Fatal("strict decoder accepted malformed JSON")
		}
	}
}

func TestDirectInvitationPersistsAndStatusDoesNotExposeSecrets(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(securestore.NewMemoryStore())
	manager := NewDirect(repo, "windows", "amd64", "2.0.0")
	if err := manager.Configure(ctx, DirectConfiguration{Endpoint: "https://client.example:8443", DisplayName: "Workload"}); err != nil {
		t.Fatal(err)
	}
	invitation, err := manager.IssueInvitation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(invitation)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if fields["version"] != float64(2) || fields["type"] != "mobile-egress-direct-invitation" || fields["role"] != "agent" {
		t.Fatal("invalid invitation contract")
	}
	status := manager.Status()
	encoded, _ := json.Marshal(status)
	if status.ClientID == "" || status.BindAddress != ":8443" || status.Paired || strings.Contains(string(encoded), fields["capability"].(string)) {
		t.Fatal("unsafe or incorrect status")
	}
	restored := NewDirect(repo, "windows", "amd64", "2.0.0")
	again, err := restored.IssueInvitation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if again != invitation {
		t.Fatal("unexpired invitation changed after reload")
	}
	if err := restored.CancelInvitation(ctx); err != nil {
		t.Fatal(err)
	}
	next, err := restored.IssueInvitation(ctx)
	if err != nil || next == invitation {
		t.Fatal("canceled invitation reused", err)
	}
}
