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
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"mobile-egress/pairing"
)

func TestEnrollGeneratesP256CSRAndValidatesReturnedIdentity(t *testing.T) {
	t.Parallel()

	fixture := newEnrollmentFixture(t, false)
	defer fixture.Close()
	identity, err := Enroll(context.Background(), fixture.bundle("one-use-owner-capability", "owner"))
	if err != nil {
		t.Fatal(err)
	}
	if identity.Role != "owner" || identity.Serial == "" || identity.PrivateKeyPEM == "" || identity.CertificatePEM == "" || identity.CACertificatePEM == "" {
		t.Fatalf("identity is incomplete: %#v", identity)
	}
	keyBlock, _ := pem.Decode([]byte(identity.PrivateKeyPEM))
	parsed, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok || key.Curve != elliptic.P256() {
		t.Fatalf("private key = %T, want P-256 ECDSA", parsed)
	}
}

func TestEnrollRejectsCertificateChainThatDoesNotMatchReturnedCA(t *testing.T) {
	t.Parallel()

	fixture := newEnrollmentFixture(t, true)
	defer fixture.Close()
	if _, err := Enroll(context.Background(), fixture.bundle("one-use-client-capability", "client")); err == nil {
		t.Fatal("Enroll() accepted a certificate chain signed by a different CA")
	}
}

func TestEnrollPinsTLSBeforeSendingPairingCapability(t *testing.T) {
	t.Parallel()

	fixture := newEnrollmentFixture(t, false)
	defer fixture.Close()
	_, _, unrelatedCAPEM := newTestCA(t, "unrelated-bootstrap-ca")
	bundle := fixture.bundle("must-not-cross-untrusted-tls", "client")
	bundle.CACertificatePEM = string(unrelatedCAPEM)
	if _, err := Enroll(context.Background(), bundle); err == nil {
		t.Fatal("Enroll() trusted a relay not signed by the supplied CA")
	}
	if fixture.requestReceived.Load() {
		t.Fatal("pairing capability was sent before TLS trust was established")
	}
}

type enrollmentFixture struct {
	server          *httptest.Server
	ca              *x509.Certificate
	caKey           *ecdsa.PrivateKey
	caPEM           []byte
	badCA           []byte
	requestReceived atomic.Bool
}

func newEnrollmentFixture(t *testing.T, mismatchedCA bool) *enrollmentFixture {
	t.Helper()
	ca, caKey, caPEM := newTestCA(t, "relay-test-ca")
	_, _, badCAPEM := newTestCA(t, "unrelated-ca")
	serverCertificate := newSignedCertificate(t, ca, caKey, &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "127.0.0.1"},
		DNSNames: []string{"127.0.0.1"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	fixture := &enrollmentFixture{ca: ca, caKey: caKey, caPEM: caPEM, badCA: badCAPEM}
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/enroll" {
			http.NotFound(writer, request)
			return
		}
		fixture.requestReceived.Store(true)
		var input struct {
			Code   string `json:"code"`
			Role   string `json:"role"`
			CSRPEM string `json:"csrPem"`
		}
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
			t.Error(err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		if !strings.HasPrefix(input.Code, "one-use-") || (input.Role != "owner" && input.Role != "client") {
			t.Errorf("unexpected enrollment input: %#v", input)
		}
		block, _ := pem.Decode([]byte(input.CSRPEM))
		csr, err := x509.ParseCertificateRequest(block.Bytes)
		if err != nil || csr.CheckSignature() != nil {
			t.Errorf("invalid CSR: %v", err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		publicKey, ok := csr.PublicKey.(*ecdsa.PublicKey)
		if !ok || publicKey.Curve != elliptic.P256() {
			t.Errorf("CSR key = %T, want P-256", csr.PublicKey)
		}
		leafPEM := signPublicKey(t, fixture.ca, fixture.caKey, csr.PublicKey, input.Role)
		responseCA := fixture.caPEM
		if mismatchedCA {
			responseCA = fixture.badCA
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(writer).Encode(map[string]string{
			"certificatePem":   string(leafPEM) + string(fixture.caPEM),
			"caCertificatePem": string(responseCA), "serial": "3", "role": input.Role,
		})
	})
	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{Certificates: []tls.Certificate{serverCertificate}, MinVersion: tls.VersionTLS13}
	server.StartTLS()
	fixture.server = server
	return fixture
}

func (fixture *enrollmentFixture) bundle(capability, role string) pairing.Bundle {
	return pairing.Bundle{
		Version: pairing.Version, RelayURL: fixture.server.URL,
		CACertificatePEM: string(fixture.caPEM), Capability: capability, Role: role,
	}
}

func (fixture *enrollmentFixture) Close() { fixture.server.Close() }

func newTestCA(t *testing.T, commonName string) (*x509.Certificate, *ecdsa.PrivateKey, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: commonName},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return certificate, key, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func newSignedCertificate(t *testing.T, ca *x509.Certificate, caKey *ecdsa.PrivateKey, template *x509.Certificate) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	if err != nil {
		t.Fatal(err)
	}
	return certificate
}

func signPublicKey(t *testing.T, ca *x509.Certificate, caKey *ecdsa.PrivateKey, publicKey any, role string) []byte {
	t.Helper()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(3), Subject: pkix.Name{CommonName: "mobile-egress-" + role},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, publicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}
