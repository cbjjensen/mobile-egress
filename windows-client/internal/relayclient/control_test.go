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
	requests := make(chan string, 2)
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.TLS == nil || len(request.TLS.PeerCertificates) == 0 {
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch request.URL.Path {
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
