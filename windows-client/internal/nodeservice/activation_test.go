package nodeservice

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"mobile-egress/windows-client/internal/securestore"
)

func TestHostedActivationPKCEProtectedPersistenceAndRestart(t *testing.T) {
	store := securestore.NewMemoryStore()
	m := NewDirect(NewRepository(store), "windows", "amd64", "test")
	var challenge string
	var mu sync.Mutex
	polls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if r.URL.Path == "/api/mobile-egress/device-links" {
			challenge = body["codeChallenge"]
			if body["codeChallengeMethod"] != "S256" || body["clientId"] == "" {
				t.Error("missing PKCE")
			}
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"requestId": "12345678-1234-4234-8234-123456789012", "pollSecret": base64.RawURLEncoding.EncodeToString(make([]byte, 32)), "verificationUri": "https://inevitableproxies.com/mobile-egress/approve?requestId=12345678-1234-4234-8234-123456789012", "expiresAt": time.Now().Add(time.Hour), "pollIntervalSeconds": 5}})
			return
		}
		digest := sha256.Sum256([]byte(body["codeVerifier"]))
		if base64.RawURLEncoding.EncodeToString(digest[:]) != challenge || body["pollSecret"] != base64.RawURLEncoding.EncodeToString(make([]byte, 32)) {
			t.Error("wrong proof")
		}
		mu.Lock()
		polls++
		mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]string{"status": "pending"}})
	}))
	defer server.Close()
	m.activationOrigin = server.URL
	m.activationClient = server.Client()
	view, err := m.StartHostedActivation(context.Background(), "Workload")
	if err != nil {
		t.Fatal(err)
	}
	if view.VerificationURI == "" || view.State != "pending" {
		t.Fatalf("view=%+v", view)
	}
	m.StopHostedActivation()
	raw, err := store.Get(context.Background(), directStateKey)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "codeVerifier") {
		t.Fatal("proof not persisted")
	}
	safe, _ := json.Marshal(m.Status())
	if strings.Contains(string(safe), "pollSecret") || strings.Contains(string(safe), m.state.Activation.CodeVerifier) {
		t.Fatal("secret exposed")
	}
	restored := NewDirect(NewRepository(store), "windows", "amd64", "test")
	restored.activationOrigin = server.URL
	restored.activationClient = server.Client()
	resumed, err := restored.ResumeHostedActivation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer restored.StopHostedActivation()
	if resumed.VerificationURI != view.VerificationURI {
		t.Fatal("lost activation")
	}
	if err := restored.pollActivation(context.Background(), restored.state.Activation.RequestID); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if polls == 0 {
		t.Fatal("did not resume polling")
	}
}

func TestHostedActivationPollingAppliesApprovalWithoutResume(t *testing.T) {
	m := NewDirect(NewRepository(securestore.NewMemoryStore()), "windows", "amd64", "test")
	defer m.StopHostedActivation()
	const requestID = "12345678-1234-4234-8234-123456789012"
	const route = "r-23456789123442348234123456789012.gateway.example"
	proof := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	var clientID string
	var clientMu sync.Mutex
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/mobile-egress/device-links" {
			var body struct {
				ClientID string `json:"clientId"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
				return
			}
			clientMu.Lock()
			clientID = body.ClientID
			clientMu.Unlock()
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"requestId": requestID, "pollSecret": proof, "verificationUri": "https://inevitableproxies.com/mobile-egress/approve?requestId=" + requestID, "expiresAt": time.Now().Add(10 * time.Minute), "pollIntervalSeconds": 5}})
			return
		}
		if r.URL.Path != "/api/mobile-egress/device-links/poll" {
			t.Errorf("unexpected activation request: %s", r.URL.Path)
			return
		}
		clientMu.Lock()
		approvedClientID := clientID
		clientMu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"status": "authorized", "deviceId": "34567890-1234-4234-8234-123456789012", "clientId": approvedClientID, "deviceToken": "med1." + proof, "routeId": "23456789-1234-4234-8234-123456789012", "brokerEndpoint": "https://broker.example:443", "gatewayHostname": route, "gatewayPort": 443}})
	}))
	defer server.Close()
	m.activationOrigin = server.URL
	m.activationClient = server.Client()
	view, err := m.StartHostedActivation(context.Background(), "Workload")
	if err != nil || view.State != "pending" {
		t.Fatalf("start: state=%s error=%v", view.State, err)
	}
	// Exercise the real poller's child context. A direct call with a background
	// context misses cancellation of that context during the approval handoff.
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		status := m.Status()
		if status.ActivationState == "authorized" && status.Endpoint == "https://"+route {
			restored := NewDirect(m.repository, "windows", "amd64", "test")
			if err := restored.ensureLocked(context.Background()); err != nil {
				t.Fatal(err)
			}
			if restored.Status().Endpoint != status.Endpoint || restored.state.Activation != nil {
				t.Fatal("approval was not fully persisted without a second resume")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	status := m.Status()
	t.Fatalf("automatic approval did not configure the Client: state=%s endpointConfigured=%t", status.ActivationState, status.Endpoint != "")
}

func TestActivationRequestsRejectRedirectsAndSanitizeErrors(t *testing.T) {
	leaked := false
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked = true }))
	defer target.Close()
	redirect := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	m := NewDirect(NewRepository(securestore.NewMemoryStore()), "windows", "amd64", "test")
	m.activationClient = redirect.Client()
	m.activationOrigin = redirect.URL
	_, err := m.StartHostedActivation(context.Background(), "Workload")
	if err == nil || strings.Contains(err.Error(), target.URL) || leaked {
		t.Fatalf("unsafe redirect: %v leak %v", err, leaked)
	}
}

func TestAuthorizedActivationSeparatesBrokerAndRouteAndRetainsPhone(t *testing.T) {
	m, i, csr := directTestConfigured(t)
	response := directTestRequest(m, "/v2/direct/enroll", directEnrollBody(i, csr), nil)
	if response.Code != 201 {
		t.Fatal(response.Body.String())
	}
	id, pairing, ca, password := m.state.ClientID, m.state.Pairing.ID, m.state.CACertificatePEM, m.state.Password
	verifier, _ := directRandom()
	proof := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	m.state.Activation = &activationState{RequestID: "12345678-1234-4234-8234-123456789012", PollSecret: proof, CodeVerifier: verifier, DisplayName: "Workload", Status: "pending", ExpiresAt: time.Now().Add(time.Hour), PollIntervalSeconds: 5}
	token := "med1." + proof
	routeID := "23456789-1234-4234-8234-123456789012"
	route := "r-" + strings.ReplaceAll(routeID, "-", "") + ".gateway.example"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"status": "authorized", "deviceId": "34567890-1234-4234-8234-123456789012", "clientId": id, "deviceToken": token, "routeId": routeID, "brokerEndpoint": "https://broker.example:443", "gatewayHostname": route, "gatewayPort": 443}})
	}))
	defer server.Close()
	m.activationOrigin = server.URL
	m.activationClient = server.Client()
	if err := m.pollActivation(context.Background(), m.state.Activation.RequestID); err != nil {
		t.Fatal(err)
	}
	if m.state.Configuration.Endpoint != "https://"+route || m.state.Hosted.BrokerEndpoint != "https://broker.example:443" {
		t.Fatal("broker conflated with phone endpoint")
	}
	if m.state.ClientID != id || m.state.Pairing.ID != pairing || m.state.CACertificatePEM != ca || m.state.Password != password || m.state.Invitation != nil {
		t.Fatal("migration replaced identity or stale invitation")
	}
	bundle, err := m.ExportEndpointUpdate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(bundle)
	var wrapper struct{ Payload string }
	json.Unmarshal(raw, &wrapper)
	payload, _ := base64.RawURLEncoding.DecodeString(wrapper.Payload)
	if !strings.Contains(string(payload), `"transport":"hosted"`) || strings.Contains(string(raw), token) {
		t.Fatal("signed migration mode missing or token leaked")
	}
	restored := NewDirect(m.repository, "windows", "amd64", "test")
	if err := restored.ensureLocked(context.Background()); err != nil {
		t.Fatal(err)
	}
	if restored.state.Hosted.DeviceToken != token || restored.Status().Transport != "hosted" {
		t.Fatal("lost protected activation")
	}
}

func TestHostedReactivationRecoversOfflinePhoneWithSignedUpdate(t *testing.T) {
	ctx := context.Background()
	m, _, csr := directTestConfigured(t)
	proof := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	m.state.Hosted = &hostedState{DeviceID: "12345678-1234-4234-8234-123456789012", DeviceToken: "med1." + proof, BrokerEndpoint: "https://broker.example", GatewayHostname: "old-route.example", GatewayPort: 443}
	if err := m.configureHostedLocked(ctx, "Workload"); err != nil {
		t.Fatal(err)
	}
	bundle, err := m.IssueInvitation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(bundle)
	var invitation directInvitation
	if err != nil || json.Unmarshal(raw, &invitation) != nil {
		t.Fatal("invalid hosted invitation")
	}
	identity := directTestEnroll(t, m, invitation, csr)
	block, _ := pem.Decode([]byte(identity.CertificatePEM))
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	ackAt := func(manager *Direct, endpoint string, generation uint64) *httptest.ResponseRecorder {
		body, err := json.Marshal(map[string]any{"clientId": identity.ClientID, "pairingId": identity.PairingID, "generation": generation})
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPost, endpoint+"/v2/direct/ack", bytes.NewReader(body))
		request.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{certificate}, VerifiedChains: [][]*x509.Certificate{{certificate}}}
		response := httptest.NewRecorder()
		manager.handler().ServeHTTP(response, request)
		return response
	}
	if response := ackAt(m, "https://old-route.example", identity.Generation); response.Code != http.StatusOK {
		t.Fatal("initial hosted pairing acknowledgement failed", response.Code)
	}
	before := m.cloneLocked()
	if !m.Status().Paired || m.Status().UpdatePending || m.opener.current() != nil {
		t.Fatal("fixture must have an acknowledged but offline hosted phone")
	}

	// Removing the device in Inevitable retires its route. Browser approval
	// returns a replacement route for the same locally paired Client identity.
	verifier, err := directRandom()
	if err != nil {
		t.Fatal(err)
	}
	m.state.Activation = &activationState{RequestID: "23456789-1234-4234-8234-123456789012", PollSecret: proof, CodeVerifier: verifier, DisplayName: "Workload", Status: "pending", ExpiresAt: time.Now().Add(time.Hour), PollIntervalSeconds: 5}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"status": "authorized", "deviceId": "34567890-1234-4234-8234-123456789012", "clientId": before.ClientID, "deviceToken": "med1." + proof, "routeId": "45678901-1234-4234-8234-123456789012", "brokerEndpoint": "https://broker.example", "gatewayHostname": "new-route.example", "gatewayPort": 443}})
	}))
	defer server.Close()
	m.activationOrigin, m.activationClient = server.URL, server.Client()
	if err := m.pollActivation(ctx, m.state.Activation.RequestID); err != nil {
		t.Fatal(err)
	}
	if m.state.Generation != before.Generation+1 || m.state.Configuration.Endpoint != "https://new-route.example" {
		t.Fatal("reactivation did not advance to the replacement hosted route")
	}
	if m.state.ClientID != before.ClientID || m.state.CACertificatePEM != before.CACertificatePEM || m.state.CAPrivateKeyPEM != before.CAPrivateKeyPEM ||
		m.state.Pairing.ID != before.Pairing.ID || !bytes.Equal(m.state.Pairing.PublicKey, before.Pairing.PublicKey) || m.state.Pairing.Identity != before.Pairing.Identity ||
		m.state.Username != before.Username || m.state.Password != before.Password || m.state.Invitation != nil {
		t.Fatal("reactivation replaced local trust, pairing, proxy credentials, or created an invitation")
	}

	// Protected persistence must retain both the desired route and the phone's
	// older acknowledgement so restarting cannot hide the pending update.
	restored := NewDirect(m.repository, "windows", "amd64", "test")
	if err := restored.ensureLocked(ctx); err != nil {
		t.Fatal(err)
	}
	if status := restored.Status(); !status.Paired || !status.UpdatePending || status.Connected || status.Generation != m.state.Generation {
		t.Fatal("restart lost pending recovery or reported an offline phone connected")
	}
	if restored.state.AcknowledgedGeneration != before.Generation || restored.state.AcknowledgedEndpoint != "https://old-route.example" {
		t.Fatal("reactivation silently acknowledged an address the phone has not received")
	}
	bundle, err = restored.ExportEndpointUpdate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = base64.RawURLEncoding.DecodeString(bundle)
	var wrapper struct {
		Version   int    `json:"version"`
		Type      string `json:"type"`
		Payload   string `json:"payload"`
		Signature string `json:"signature"`
	}
	if err != nil || directStrictJSON(raw, &wrapper) != nil || wrapper.Version != 2 || wrapper.Type != "mobile-egress-direct-endpoint-update" {
		t.Fatal("invalid recovery update wrapper")
	}
	payload, err := base64.RawURLEncoding.DecodeString(wrapper.Payload)
	if err != nil {
		t.Fatal(err)
	}
	signature, err := base64.RawURLEncoding.DecodeString(wrapper.Signature)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(append([]byte(directSignatureDomain), payload...))
	ca, _, err := directCA(before)
	if err != nil || !ecdsa.VerifyASN1(ca.PublicKey.(*ecdsa.PublicKey), digest[:], signature) {
		t.Fatal("recovery update was not signed by the phone's existing pinned authority")
	}
	var fields struct {
		ClientID   string `json:"clientId"`
		PairingID  string `json:"pairingId"`
		Generation uint64 `json:"generation"`
		Endpoint   string `json:"endpoint"`
		Transport  string `json:"transport"`
	}
	if directStrictJSON(payload, &fields) != nil || fields.ClientID != before.ClientID || fields.PairingID != before.Pairing.ID || fields.Generation != before.Generation+1 || fields.Endpoint != "https://new-route.example" || fields.Transport != "hosted" {
		t.Fatal("recovery update did not bind the new route to the existing phone identity")
	}
	tc, err := directTLS(restored.state)
	if err != nil {
		t.Fatal(err)
	}
	restored.tlsConfig.Store(tc)
	if response := ackAt(restored, "https://old-route.example", fields.Generation); response.Code != http.StatusConflict || !restored.Status().UpdatePending {
		t.Fatal("old route incorrectly confirmed delivery of the replacement endpoint", response.Code)
	}
	if response := ackAt(restored, fields.Endpoint, fields.Generation); response.Code != http.StatusOK || restored.Status().UpdatePending {
		t.Fatal("existing phone certificate could not acknowledge the recovery update", response.Code)
	}
	if restored.Status().Connected {
		t.Fatal("an acknowledgement without a live session reported connected")
	}
	restored.mu.Lock()
	restored.status.Running = true
	restored.sessionGeneration, restored.sessionTransport = before.Generation, "hosted"
	restored.mu.Unlock()
	restored.opener.swap(&healthyUpdateTunnel{})
	defer restored.opener.swap(nil)
	if restored.Status().Connected {
		t.Fatal("a stale hosted session verified the replacement route")
	}
	restored.mu.Lock()
	restored.sessionGeneration = fields.Generation
	restored.mu.Unlock()
	if !restored.Status().Connected {
		t.Fatal("current-generation authenticated session did not finish recovery")
	}
}

func TestActivationTimeoutAndBrowserURLValidation(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(100 * time.Millisecond):
		}
	}))
	defer server.Close()
	m := NewDirect(NewRepository(securestore.NewMemoryStore()), "windows", "amd64", "test")
	m.activationOrigin = server.URL
	m.activationClient = server.Client()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := m.StartHostedActivation(ctx, "Workload")
	if err == nil || time.Since(start) > time.Second {
		t.Fatal("activation did not honor timeout")
	}
	for _, raw := range []string{"http://inevitableproxies.com/mobile-egress/approve?requestId=12345678-1234-4234-8234-123456789012", "https://inevitableproxies.com.evil.test/mobile-egress/approve?requestId=12345678-1234-4234-8234-123456789012", "https://user:password@inevitableproxies.com/mobile-egress/approve?requestId=12345678-1234-4234-8234-123456789012", "https://inevitableproxies.com/mobile-egress/approve?requestId=12345678-1234-4234-8234-123456789012&token=secret", "https://inevitableproxies.com/mobile-egress/approve?requestId=12345678-1234-4234-8234-123456789012#fragment"} {
		if validActivationURL(raw) {
			t.Fatalf("unsafe URI %s", raw)
		}
	}
}

func TestActivationCancellationExpiryAndExplicitDirectPreventLatePolling(t *testing.T) {
	for _, operation := range []string{"cancel", "expire", "direct"} {
		t.Run(operation, func(t *testing.T) {
			m, _, _ := directTestConfigured(t)
			proof := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
			id := "12345678-1234-4234-8234-123456789012"
			m.state.Activation = &activationState{RequestID: id, PollSecret: proof, CodeVerifier: proof, Status: "pending", ExpiresAt: time.Now().Add(time.Hour), PollIntervalSeconds: 5, VerificationURI: "https://inevitableproxies.com/mobile-egress/approve?requestId=" + id, DisplayName: "Workload"}
			requests := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.URL.Path != "/api/mobile-egress/device-links/cancel" {
					t.Error("late poll after cancellation")
				}
				w.WriteHeader(http.StatusServiceUnavailable)
			}))
			defer server.Close()
			m.activationOrigin = server.URL
			m.activationClient = server.Client()
			switch operation {
			case "cancel":
				if err := m.CancelHostedActivation(context.Background()); err != nil {
					t.Fatal(err)
				}
			case "expire":
				m.state.Activation.ExpiresAt = time.Now().Add(-time.Second)
				if err := m.pollActivation(context.Background(), id); err != nil {
					t.Fatal(err)
				}
			case "direct":
				if err := m.Configure(context.Background(), *m.state.Configuration); err != nil {
					t.Fatal(err)
				}
			}
			if err := m.pollActivation(context.Background(), id); err != nil {
				t.Fatal(err)
			}
			if operation == "cancel" && requests != 1 {
				t.Fatal("cancel not attempted")
			}
			if operation != "cancel" && requests != 0 {
				t.Fatal("terminal request polled")
			}
			raw, _ := json.Marshal(m.state)
			if strings.Contains(string(raw), `"pollSecret":"`+proof) || strings.Contains(string(raw), `"codeVerifier":"`+proof) {
				t.Fatal("terminal proof retained")
			}
		})
	}
}

func TestActivationControlDeadlineIncludesWaitingForProtectedState(t *testing.T) {
	m := NewDirect(NewRepository(securestore.NewMemoryStore()), "windows", "amd64", "test")
	m.opMu.Lock()
	defer m.opMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := m.ResumeHostedActivation(ctx); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expired operation accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("activation lock ignored deadline")
	}
}
