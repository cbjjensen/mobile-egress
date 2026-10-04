package nodeservice

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"mobile-egress/internal/tunnelwire"
	"mobile-egress/windows-client/internal/securestore"
)

func directTestEnroll(t *testing.T, m *Direct, i directInvitation, csr string) directIdentityResponse {
	t.Helper()
	w := directTestRequest(m, "/v2/direct/enroll", directEnrollBody(i, csr), nil)
	if w.Code != 201 {
		t.Fatalf("enroll: %d %s", w.Code, w.Body.String())
	}
	var identity directIdentityResponse
	_ = json.Unmarshal(w.Body.Bytes(), &identity)
	return identity
}
func directTestAck(t *testing.T, m *Direct, identity directIdentityResponse) {
	t.Helper()
	w := directTestRequest(m, "/v2/direct/ack", map[string]any{"clientId": identity.ClientID, "pairingId": identity.PairingID, "generation": identity.Generation}, &identity)
	if w.Code != 200 {
		t.Fatalf("ack: %d %s", w.Code, w.Body.String())
	}
}

func TestDirectEndpointSignatureAndRenewalCannotAcknowledgeUnseenEndpoint(t *testing.T) {
	m, i, csr := directTestConfigured(t)
	identity := directTestEnroll(t, m, i, csr)
	directTestAck(t, m, identity)
	for _, host := range []string{"second.example", "third.example"} {
		if err := m.Configure(context.Background(), DirectConfiguration{Endpoint: "https://" + host + ":443", DisplayName: "Changed"}); err != nil {
			t.Fatal(err)
		}
	}
	bundle, err := m.ExportEndpointUpdate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(bundle)
	var wrapper struct {
		Version   int    `json:"version"`
		Type      string `json:"type"`
		Payload   string `json:"payload"`
		Signature string `json:"signature"`
	}
	if directStrictJSON(raw, &wrapper) != nil {
		t.Fatal("invalid wrapper")
	}
	payload, _ := base64.RawURLEncoding.DecodeString(wrapper.Payload)
	signature, _ := base64.RawURLEncoding.DecodeString(wrapper.Signature)
	digest := sha256.Sum256(append([]byte(directSignatureDomain), payload...))
	ca, _, _ := directCA(m.state)
	if wrapper.Version != 2 || wrapper.Type != "mobile-egress-direct-endpoint-update" || !ecdsa.VerifyASN1(ca.PublicKey.(*ecdsa.PublicKey), digest[:], signature) {
		t.Fatal("invalid endpoint signature")
	}
	var fields map[string]any
	_ = json.Unmarshal(payload, &fields)
	if fields["clientId"] != identity.ClientID || fields["pairingId"] != identity.PairingID || fields["generation"] != float64(3) || fields["endpoint"] != "https://third.example" {
		t.Fatal("incorrect endpoint payload")
	}
	renew := map[string]any{"clientId": identity.ClientID, "pairingId": identity.PairingID, "csrPem": csr}
	w := directTestRequest(m, "/v2/direct/renew", renew, &identity)
	if w.Code != 201 {
		t.Fatal("renew failed", w.Code)
	}
	var renewed directIdentityResponse
	_ = json.Unmarshal(w.Body.Bytes(), &renewed)
	if renewed.Generation != 1 || renewed.PairingID != identity.PairingID || renewed.Serial == identity.Serial {
		t.Fatal("renewal changed endpoint state or failed to rotate certificate")
	}
	again := directTestRequest(m, "/v2/direct/renew", renew, &identity)
	if again.Body.String() != w.Body.String() {
		t.Fatal("lost renewal response is not recoverable")
	}
	directTestAck(t, m, renewed)
	if !m.Status().UpdatePending {
		t.Fatal("certificate ACK cleared unseen endpoint change")
	}
	w = directTestRequest(m, "/v2/direct/ack", map[string]any{"clientId": identity.ClientID, "pairingId": identity.PairingID, "generation": 1}, &identity)
	if w.Code != 401 {
		t.Fatal("old certificate survived renewal ACK", w.Code)
	}
}

func TestDirectLostRenewalRetryUsesAcknowledgedEndpointGeneration(t *testing.T) {
	m, invitation, csr := directTestConfigured(t)
	old := directTestEnroll(t, m, invitation, csr)
	directTestAck(t, m, old)
	renew := map[string]any{"clientId": old.ClientID, "pairingId": old.PairingID, "csrPem": csr}
	first := directTestRequest(m, "/v2/direct/renew", renew, &old)
	var issued directIdentityResponse
	if first.Code != 201 || json.Unmarshal(first.Body.Bytes(), &issued) != nil || issued.Generation != 1 {
		t.Fatal("initial renewal failed", first.Code)
	}
	// The phone loses this response, keeps its old certificate, then imports
	// and acknowledges an endpoint change before retrying the renewal.
	if err := m.Configure(context.Background(), DirectConfiguration{Endpoint: "https://second.example:8443", DisplayName: "Client"}); err != nil {
		t.Fatal(err)
	}
	update, err := m.ExportEndpointUpdate(context.Background())
	if err != nil || update == "" {
		t.Fatal("endpoint update unavailable", err)
	}
	raw, _ := json.Marshal(map[string]any{"clientId": old.ClientID, "pairingId": old.PairingID, "generation": 2})
	request := httptest.NewRequest(http.MethodPost, "https://second.example:8443/v2/direct/ack", bytes.NewReader(raw))
	block, _ := pem.Decode([]byte(old.CertificatePEM))
	certificate, _ := x509.ParseCertificate(block.Bytes)
	request.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{certificate}, VerifiedChains: [][]*x509.Certificate{{certificate}}}
	ack := httptest.NewRecorder()
	m.handler().ServeHTTP(ack, request)
	if ack.Code != 200 || m.state.Pairing.PreviousSerial != old.Serial {
		t.Fatal("old certificate could not acknowledge the endpoint without losing renewal recovery", ack.Code)
	}
	if err := m.Configure(context.Background(), DirectConfiguration{Endpoint: "https://third.example:8443", DisplayName: "Client"}); err != nil {
		t.Fatal(err)
	}
	for _, manager := range []*Direct{m, NewDirect(m.repository, "windows", "amd64", "2.0.0")} {
		retry := directTestRequest(manager, "/v2/direct/renew", renew, &old)
		var recovered directIdentityResponse
		if retry.Code != 201 || json.Unmarshal(retry.Body.Bytes(), &recovered) != nil {
			t.Fatal("cached renewal was not recoverable", retry.Code)
		}
		if recovered.Generation != 2 {
			t.Fatalf("renewal retry generation = %d; want acknowledged 2, not issued 1 or desired 3", recovered.Generation)
		}
		// Only the response generation may change; never mint another serial or
		// replace the certificate whose delivery is still unacknowledged.
		recovered.Generation = issued.Generation
		if recovered != issued {
			t.Fatal("cached renewal changed the previously issued identity")
		}
	}
}

func TestDirectConcurrentRedemptionCommitsExactlyOneKey(t *testing.T) {
	m, i, csr := directTestConfigured(t)
	_, _, other := directTestConfigured(t)
	start := make(chan struct{})
	results := make(chan int, 2)
	for _, value := range []string{csr, other} {
		go func(value string) {
			<-start
			results <- directTestRequest(m, "/v2/direct/enroll", directEnrollBody(i, value), nil).Code
		}(value)
	}
	close(start)
	a, b := <-results, <-results
	if !(a == 201 && b == 409 || a == 409 && b == 201) {
		t.Fatalf("competing enrollments=%d,%d", a, b)
	}
}

type directFailingStore struct {
	securestore.Store
	mu   sync.Mutex
	fail bool
}

func (s *directFailingStore) Put(ctx context.Context, key string, value []byte) error {
	s.mu.Lock()
	fail := s.fail
	s.mu.Unlock()
	if fail && key == directStateKey {
		return errors.New("simulated protected write failure")
	}
	return s.Store.Put(ctx, key, value)
}
func (s *directFailingStore) setFail(value bool) { s.mu.Lock(); s.fail = value; s.mu.Unlock() }

func TestDirectFailedIssuanceDoesNotReservePairingAndRevokeFailsClosed(t *testing.T) {
	m, i, csr := directTestConfigured(t)
	store := &directFailingStore{Store: m.repository.store}
	m.repository.store = store
	store.setFail(true)
	if w := directTestRequest(m, "/v2/direct/enroll", directEnrollBody(i, csr), nil); w.Code != 503 {
		t.Fatal("failed persistence delivered credentials", w.Code)
	}
	if m.state.Pairing != nil {
		t.Fatal("failed issuance retained pairing")
	}
	store.setFail(false)
	identity := directTestEnroll(t, m, i, csr)
	directTestAck(t, m, identity)
	store.setFail(true)
	if m.Revoke(context.Background()) == nil {
		t.Fatal("failed revoke reported success")
	}
	if w := directTestRequest(m, "/v2/direct/ack", map[string]any{"clientId": identity.ClientID, "pairingId": identity.PairingID, "generation": 1}, &identity); w.Code != 503 || !strings.Contains(w.Body.String(), "storage_unavailable") {
		t.Fatal("failed durable revoke must block admission with a storage failure", w.Code)
	}
	store.setFail(false)
	if m.Revoke(context.Background()) != nil {
		t.Fatal("revoke recovery failed")
	}
}

func TestDirectRealTLSAdmissionSessionReplacementAndRevocation(t *testing.T) {
	m := NewDirect(NewRepository(securestore.NewMemoryStore()), "windows", "amd64", "2.0.0")
	server := httptest.NewUnstartedServer(m.handler())
	endpoint := "https://" + server.Listener.Addr().String()
	if err := m.Configure(context.Background(), DirectConfiguration{Endpoint: endpoint, DisplayName: "Loopback fixture"}); err != nil {
		t.Fatal(err)
	}
	bundle, _ := m.IssueInvitation(context.Background())
	raw, _ := base64.RawURLEncoding.DecodeString(bundle)
	var invitation directInvitation
	_ = json.Unmarshal(raw, &invitation)
	server.TLS = m.tlsConfig.Load().Clone()
	server.StartTLS()
	defer server.Close()
	m.opMu.Lock()
	m.runCtx = context.Background()
	m.opMu.Unlock()
	defer m.opener.swap(nil)
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM([]byte(invitation.CACertificatePEM))
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	csr, _ := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, key)
	csrPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csr}))
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots}}, Timeout: 3 * time.Second}
	defer client.CloseIdleConnections()
	request, _ := json.Marshal(directEnrollBody(invitation, csrPEM))
	response, err := client.Post(endpoint+"/v2/direct/enroll", "application/json", bytes.NewReader(request))
	if err != nil {
		t.Fatal(err)
	}
	var identity directIdentityResponse
	_ = json.NewDecoder(response.Body).Decode(&identity)
	response.Body.Close()
	if response.StatusCode != 201 {
		t.Fatal("TLS enrollment rejected", response.StatusCode)
	}
	private, _ := directKeyPEM(key)
	certificate, err := tls.X509KeyPair([]byte(identity.CertificatePEM), []byte(private))
	if err != nil {
		t.Fatal(err)
	}
	config := &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, Certificates: []tls.Certificate{certificate}}
	header := http.Header{"X-Mobile-Egress-Protocol": []string{"direct/1"}}
	dialer := websocket.Dialer{TLSClientConfig: config, HandshakeTimeout: 3 * time.Second}
	wsURL := "wss" + strings.TrimPrefix(endpoint, "https") + "/v2/direct/session?transport=2"
	if c, r, e := dialer.Dial(wsURL, header); e == nil {
		c.Close()
		t.Fatal("unacknowledged identity admitted session")
	} else if r == nil || r.StatusCode != 401 {
		t.Fatalf("unexpected pre-ACK rejection %v", e)
	}
	authorized := &http.Client{Transport: &http.Transport{TLSClientConfig: config}, Timeout: 3 * time.Second}
	defer authorized.CloseIdleConnections()
	ack, _ := json.Marshal(map[string]any{"clientId": identity.ClientID, "pairingId": identity.PairingID, "generation": identity.Generation})
	response, err = authorized.Post(endpoint+"/v2/direct/ack", "application/json", bytes.NewReader(ack))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("TLS ACK rejected", response.StatusCode)
	}
	first, _, err := dialer.Dial(wsURL, header)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	_, _, _ = first.ReadMessage()
	second, _, err := dialer.Dial(wsURL, header)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	_, _, _ = second.ReadMessage()
	first.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := first.ReadMessage(); err == nil {
		t.Fatal("replacement kept previous session")
	}
	tunnel := m.opener.current()
	streamResult := make(chan io.ReadWriteCloser, 1)
	errorsChannel := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		stream, e := tunnel.OpenStream(ctx, "1.1.1.1", 443)
		if e != nil {
			errorsChannel <- e
		} else {
			streamResult <- stream
		}
	}()
	_, openRaw, err := second.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	open, err := tunnelwire.ParseEnvelope(openRaw)
	if err != nil || open.Type != tunnelwire.TypeOpen {
		t.Fatal("missing open")
	}
	for _, frame := range []tunnelwire.Envelope{{Version: 1, Type: tunnelwire.TypeOpened, StreamID: open.StreamID}, {Version: 1, Type: tunnelwire.TypeData, StreamID: open.StreamID, Data: []byte("full TLS tail")}, {Version: 1, Type: tunnelwire.TypeClose, StreamID: open.StreamID, Payload: base64.RawURLEncoding.EncodeToString([]byte("target_closed"))}} {
		raw, _ := frame.MarshalForPeer(true)
		if err := second.WriteMessage(websocket.BinaryMessage, raw); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case stream := <-streamResult:
		raw, err := io.ReadAll(stream)
		stream.Close()
		if err != nil || string(raw) != "full TLS tail" {
			t.Fatal("TLS tail loss", err)
		}
	case err := <-errorsChannel:
		t.Fatal(err)
	case <-time.After(3 * time.Second):
		t.Fatal("open timed out")
	}
	if err := m.Revoke(context.Background()); err != nil {
		t.Fatal(err)
	}
	second.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := second.ReadMessage(); err == nil {
		t.Fatal("revocation kept active socket")
	}
	if c, r, e := dialer.Dial(wsURL, header); e == nil {
		c.Close()
		t.Fatal("revoked identity reconnected")
	} else if r == nil || r.StatusCode != 401 {
		t.Fatal("unexpected post-revoke result", e)
	}
}

func TestDirectMigrationPreservesProxyCredentials(t *testing.T) {
	store := securestore.NewMemoryStore()
	repo := NewRepository(store)
	legacy, err := newState()
	if err != nil {
		t.Fatal(err)
	}
	legacy.Configuration = &Configuration{SOCKSUsername: "existing-user", SOCKSPassword: "existing-password"}
	legacy.Pairing = &PairingState{NodeID: "2eaa8932-53d7-4d1e-9089-deaab4f936f4"}
	if err := repo.save(context.Background(), legacy); err != nil {
		t.Fatal(err)
	}
	m := NewDirect(repo, "windows", "amd64", "2.0.0")
	if err := m.Configure(context.Background(), DirectConfiguration{Endpoint: "https://client.example:8443", DisplayName: "Migrated"}); err != nil {
		t.Fatal(err)
	}
	if m.Status().Phase != "migration_required" {
		t.Fatal("old state silently considered paired")
	}
	if m.Status().ClientID != legacy.Pairing.NodeID {
		t.Fatal("usable stable Client ID changed during migration")
	}
	proxy, err := m.Proxy(context.Background(), "http")
	if err != nil || !strings.HasSuffix(proxy, ":existing-user:existing-password") {
		t.Fatal("proxy credentials changed")
	}
	if _, err := store.Get(context.Background(), stateKey); err != nil {
		t.Fatal("legacy recovery record destroyed")
	}
}

func TestDirectExpiredServerCertificateRepairsWithSameAuthorityAndGeneration(t *testing.T) {
	m, _, _ := directTestConfigured(t)
	ctx := context.Background()
	m.opMu.Lock()
	next := m.cloneLocked()
	ca, key, _ := directCA(next)
	serverKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	cert := &x509.Certificate{SerialNumber: big.NewInt(888), Subject: pkix.Name{CommonName: next.ClientID}, DNSNames: []string{"client.example"}, NotBefore: time.Now().Add(-48 * time.Hour), NotAfter: time.Now().Add(-24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, _ := x509.CreateCertificate(rand.Reader, cert, ca, &serverKey.PublicKey, key)
	next.ServerCertificatePEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})) + next.CACertificatePEM
	next.ServerPrivateKeyPEM, _ = directKeyPEM(serverKey)
	_ = m.saveLocked(ctx, next)
	m.opMu.Unlock()
	restarted := NewDirect(m.repository, "windows", "amd64", "2.0.0")
	if err := restarted.Configure(ctx, *next.Configuration); err != nil {
		t.Fatal(err)
	}
	got := restarted.tlsConfig.Load().Certificates[0].Leaf
	if got == nil || !got.NotAfter.After(time.Now().Add(7*24*time.Hour)) {
		t.Fatal("expired server certificate was not renewed before serving")
	}
	if restarted.state.CACertificatePEM != next.CACertificatePEM || restarted.state.Generation != next.Generation {
		t.Fatal("repair rotated trust or endpoint generation")
	}
}

func TestDirectAckAcceptsEquivalentDefaultHTTPSAuthority(t *testing.T) {
	m, i, csr := directTestConfigured(t)
	identity := directTestEnroll(t, m, i, csr)
	directTestAck(t, m, identity)
	if err := m.Configure(context.Background(), DirectConfiguration{Endpoint: "https://client.example:443", DisplayName: "Client"}); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"clientId": identity.ClientID, "pairingId": identity.PairingID, "generation": 2})
	r := httptest.NewRequest(http.MethodPost, "https://client.example/v2/direct/ack", bytes.NewReader(raw))
	block, _ := pem.Decode([]byte(identity.CertificatePEM))
	cert, _ := x509.ParseCertificate(block.Bytes)
	r.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}, VerifiedChains: [][]*x509.Certificate{{cert}}}
	w := httptest.NewRecorder()
	m.handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal("equivalent default-port authority rejected", w.Code)
	}
}

func TestDirectLiveConfigRemainsReachableAtAcknowledgedTLSHostnameAcrossChanges(t *testing.T) {
	m, i, _ := directTestConfigured(t)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, key)
	csr := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}))
	identity := directTestEnroll(t, m, i, csr)
	directTestAck(t, m, identity)
	server := httptest.NewUnstartedServer(m.handler())
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13, GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) { return m.tlsConfig.Load(), nil }}
	server.StartTLS()
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM([]byte(i.CACertificatePEM))
	private, _ := directKeyPEM(key)
	certificate, _ := tls.X509KeyPair([]byte(identity.CertificatePEM), []byte(private))
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, ServerName: "client.example", Certificates: []tls.Certificate{certificate}}}, Timeout: 3 * time.Second}
	defer client.CloseIdleConnections()
	for _, name := range []string{"second.example", "third.example", "fourth.example"} {
		if err := m.Configure(context.Background(), DirectConfiguration{Endpoint: "https://" + name + ":8443", DisplayName: "Client"}); err != nil {
			t.Fatal(err)
		}
	}
	response, err := client.Get(server.URL + "/v2/direct/config")
	if err != nil {
		t.Fatal("original acknowledged endpoint cannot poll live configuration after repeated changes:", err)
	}
	defer response.Body.Close()
	var result struct {
		Update string `json:"update"`
	}
	if json.NewDecoder(response.Body).Decode(&result) != nil || response.StatusCode != 200 || result.Update == "" {
		t.Fatal("live configuration was not delivered")
	}
	// A restart must retain the original hostname until the phone ACKs the new one.
	restarted := NewDirect(m.repository, "windows", "amd64", "2.0.0")
	if _, err := restarted.ExportEndpointUpdate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := restarted.tlsConfig.Load().Certificates[0].Leaf.VerifyHostname("client.example"); err != nil {
		t.Fatal("acknowledged origin lost after restart", err)
	}
	ackRaw, _ := json.Marshal(map[string]any{"clientId": identity.ClientID, "pairingId": identity.PairingID, "generation": 4})
	ackRequest := httptest.NewRequest(http.MethodPost, "https://fourth.example:8443/v2/direct/ack", bytes.NewReader(ackRaw))
	block, _ := pem.Decode([]byte(identity.CertificatePEM))
	cert, _ := x509.ParseCertificate(block.Bytes)
	ackRequest.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}, VerifiedChains: [][]*x509.Certificate{{cert}}}
	ackResponse := httptest.NewRecorder()
	m.handler().ServeHTTP(ackResponse, ackRequest)
	if ackResponse.Code != 200 || m.Status().UpdatePending {
		t.Fatal("latest endpoint ACK failed")
	}
	leaf := m.tlsConfig.Load().Certificates[0].Leaf
	if leaf.VerifyHostname("fourth.example") != nil || leaf.VerifyHostname("client.example") == nil {
		t.Fatal("ACK did not prune obsolete SANs")
	}
}
