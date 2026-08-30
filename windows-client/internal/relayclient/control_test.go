package relayclient

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOwnerControlCallsIssuePairingAndRevokeOverMTLS(t *testing.T) {
	t.Parallel()

	identity, server, requests := newControlFixture(t, "owner")
	defer server.Close()
	pairing, err := IssuePairing(context.Background(), identity, "agent")
	if err != nil {
		t.Fatal(err)
	}
	if pairing.Code != "long-one-use-capability" || pairing.Role != "agent" || pairing.ExpiresAt.IsZero() {
		t.Fatalf("pairing result = %#v", pairing)
	}
	if err := Revoke(context.Background(), identity, "ABC123"); err != nil {
		t.Fatal(err)
	}
	if got := <-requests; got != "pairing:agent" {
		t.Fatalf("first request = %q", got)
	}
	if got := <-requests; got != "revoke:ABC123" {
		t.Fatalf("second request = %q", got)
	}
}

func TestClientIdentityCannotUseOwnerControls(t *testing.T) {
	t.Parallel()

	identity := Identity{Role: "client"}
	if _, err := IssuePairing(context.Background(), identity, "agent"); err == nil {
		t.Fatal("IssuePairing accepted a client identity")
	}
	if err := Revoke(context.Background(), identity, "ABC123"); err == nil {
		t.Fatal("Revoke accepted a client identity")
	}
	_, csr := testControlCSR(t)
	if _, err := ProvisionClient(context.Background(), identity, csr); err == nil {
		t.Fatal("ProvisionClient accepted a client identity")
	}
}

func TestOwnerProvisionsClientCSRWithoutReceivingPrivateKey(t *testing.T) {
	t.Parallel()

	identity, server, requests := newControlFixture(t, "owner")
	defer server.Close()
	_, csr := testControlCSR(t)
	issued, err := ProvisionClient(context.Background(), identity, csr)
	if err != nil {
		t.Fatal(err)
	}
	if issued.Role != "client" || issued.Serial != "3" || issued.CertificatePEM == "" || issued.CACertificatePEM == "" {
		t.Fatalf("ProvisionClient() result = %#v", issued)
	}
	if got := <-requests; got != "client-csr" {
		t.Fatalf("request = %q", got)
	}
}

func TestOwnerIssuesDistinctOneUseAgentEndpointMigration(t *testing.T) {
	t.Parallel()

	identity, server, requests := newControlFixture(t, "owner")
	defer server.Close()
	migration, err := IssueEndpointMigration(context.Background(), identity)
	if err != nil {
		t.Fatal(err)
	}
	if migration.Version != 1 || migration.Type != "agent-endpoint-migration" || migration.Capability == "" || migration.RelayURL != identity.RelayURL {
		t.Fatalf("IssueEndpointMigration() = %#v", migration)
	}
	if got := <-requests; got != "endpoint-migration" {
		t.Fatalf("request = %q", got)
	}
}

func TestRelayHealthReadsOnlyAggregateReadiness(t *testing.T) {
	t.Parallel()

	identity, server, requests := newControlFixture(t, "owner")
	defer server.Close()
	health, err := Health(context.Background(), identity)
	if err != nil || !health.Readiness || health.ActiveStreams != 2 || health.ConnectedClients != 1 {
		t.Fatalf("Health() = %#v/%v", health, err)
	}
	if got := <-requests; got != "health" {
		t.Fatalf("request = %q", got)
	}
}

func TestIdentityHTTPClientUsesLoopbackDialOverrideWithPublicTLSServerName(t *testing.T) {
	t.Parallel()

	identity, server, _ := newControlFixture(t, "owner")
	defer server.Close()
	identity.RelayURL = "https://bridge.tail123.ts.net:8443"
	identity.DialAddress = "127.0.0.1:8443"
	_, transport, err := identityHTTPClient(identity)
	if err != nil {
		t.Fatal(err)
	}
	if transport.DialContext == nil || transport.TLSClientConfig.ServerName != "bridge.tail123.ts.net" {
		t.Fatalf("loopback transport = DialContext %v, ServerName %q", transport.DialContext != nil, transport.TLSClientConfig.ServerName)
	}
}

func newControlFixture(t *testing.T, role string) (Identity, *httptest.Server, chan string) {
	t.Helper()
	ca, caKey, caPEM := newTestCA(t, "control-ca")
	serverCertificate := newSignedCertificate(t, ca, caKey, &x509.Certificate{
		SerialNumber: big.NewInt(20), Subject: pkix.Name{CommonName: "127.0.0.1"},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:   time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	deviceKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	deviceCertificate := signPublicKey(t, ca, caKey, &deviceKey.PublicKey, role)
	deviceKeyDER, err := x509.MarshalPKCS8PrivateKey(deviceKey)
	if err != nil {
		t.Fatal(err)
	}
	requests := make(chan string, 4)
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.TLS == nil || len(request.TLS.PeerCertificates) == 0 {
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch request.URL.Path {
		case "/healthz":
			requests <- "health"
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"readiness": true, "agentConnected": true, "connectedClients": 1, "activeStreams": 2,
				"totalStreams": 3, "byteCount": 4, "errorCounts": map[string]int64{},
			})
		case "/v1/pairing-codes":
			var body struct {
				Role string `json:"role"`
			}
			_ = json.NewDecoder(request.Body).Decode(&body)
			requests <- "pairing:" + body.Role
			writer.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(writer).Encode(map[string]string{
				"code": "long-one-use-capability", "role": body.Role,
				"expiresAt": time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339),
			})
		case "/v1/revoke":
			var body struct {
				Serial string `json:"serial"`
			}
			_ = json.NewDecoder(request.Body).Decode(&body)
			requests <- "revoke:" + body.Serial
			writer.WriteHeader(http.StatusNoContent)
		case "/v1/clients":
			var body struct {
				CSRPEM string `json:"csrPem"`
			}
			_ = json.NewDecoder(request.Body).Decode(&body)
			block, _ := pem.Decode([]byte(body.CSRPEM))
			if block == nil {
				http.Error(writer, "invalid", http.StatusBadRequest)
				return
			}
			csr, err := x509.ParseCertificateRequest(block.Bytes)
			if err != nil || csr.CheckSignature() != nil {
				http.Error(writer, "invalid", http.StatusBadRequest)
				return
			}
			certificate := signPublicKey(t, ca, caKey, csr.PublicKey, "client")
			requests <- "client-csr"
			writer.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(writer).Encode(map[string]string{
				"certificatePem": string(certificate) + string(caPEM), "caCertificatePem": string(caPEM),
				"serial": "3", "role": "client",
			})
		case "/v1/endpoint-migrations":
			requests <- "endpoint-migration"
			writer.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"version": 1, "type": "agent-endpoint-migration", "relayUrl": identityURL(request),
				"caCertificatePem": string(caPEM), "capability": "one-use-migration-capability",
				"expiresAt": time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339),
			})
		default:
			http.NotFound(writer, request)
		}
	})
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{
		Certificates: []tls.Certificate{serverCertificate}, MinVersion: tls.VersionTLS13,
		ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: roots,
	}
	server.StartTLS()
	identity := Identity{
		RelayURL: server.URL, Role: role, Serial: "15",
		PrivateKeyPEM:  string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: deviceKeyDER})),
		CertificatePEM: string(deviceCertificate) + string(caPEM), CACertificatePEM: string(caPEM),
	}
	return identity, server, requests
}

func identityURL(request *http.Request) string { return "https://" + request.Host }

func testControlCSR(t *testing.T) (*ecdsa.PrivateKey, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	request, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: "node"},
	}, key)
	if err != nil {
		t.Fatal(err)
	}
	return key, string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: request}))
}
