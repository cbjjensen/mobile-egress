package service

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mobile-egress/relay/internal/enrollment"
)

type enrollmentResult struct {
	CertificatePEM   string `json:"certificatePem"`
	CACertificatePEM string `json:"caCertificatePem"`
	Serial           string `json:"serial"`
	Role             string `json:"role"`
}

type pairingResult struct {
	Code      string `json:"code"`
	Role      string `json:"role"`
	ExpiresAt string `json:"expiresAt"`
}

type relayFixture struct {
	service   *Service
	server    *httptest.Server
	stateDir  string
	ownerCode string
}

func TestEnrollRejectsInvalidReusedExpiredAndRoleMismatchedCapabilities(t *testing.T) {
	t.Parallel()

	fixture := newRelayFixture(t)
	defer fixture.Close()
	_, ownerCSR := newDeviceCSR(t)

	if status, _ := postEnrollment(t, fixture.server.Client(), fixture.server.URL, "not-a-capability", "owner", ownerCSR); status != http.StatusUnauthorized {
		t.Fatalf("invalid capability status = %d, want 401", status)
	}
	if status, _ := postEnrollment(t, fixture.server.Client(), fixture.server.URL, fixture.ownerCode, "client", ownerCSR); status != http.StatusForbidden {
		t.Fatalf("role-mismatched capability status = %d, want 403", status)
	}
	if status, _ := postEnrollment(t, fixture.server.Client(), fixture.server.URL, fixture.ownerCode, "owner", ownerCSR); status != http.StatusCreated {
		t.Fatalf("valid owner enrollment status = %d, want 201", status)
	}
	if status, _ := postEnrollment(t, fixture.server.Client(), fixture.server.URL, fixture.ownerCode, "owner", ownerCSR); status != http.StatusUnauthorized {
		t.Fatalf("reused capability status = %d, want 401", status)
	}

	expiredCode, expiredHash, err := newCapability()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := fixture.service.store.insertCapability(context.Background(), expiredHash, enrollment.RoleAgent, now.Add(-20*time.Minute), now.Add(-10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	_, expiredCSR := newDeviceCSR(t)
	if status, _ := postEnrollment(t, fixture.server.Client(), fixture.server.URL, expiredCode, "agent", expiredCSR); status != http.StatusUnauthorized {
		t.Fatalf("expired capability status = %d, want 401", status)
	}
}

func TestEnrollAcceptsPublicKeyPEMAndPersistsActiveIdentity(t *testing.T) {
	t.Parallel()

	fixture := newRelayFixture(t)
	defer fixture.Close()
	privateKey, _ := newDeviceCSR(t)
	publicKeyDER, err := x509.MarshalPKIXPublicKey(privateKey.Public())
	if err != nil {
		t.Fatal(err)
	}
	publicKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicKeyDER})

	status, result := postEnrollmentBody(t, fixture.server.Client(), fixture.server.URL, map[string]any{
		"code": fixture.ownerCode, "role": "owner", "publicKeyPem": string(publicKeyPEM),
	})
	if status != http.StatusCreated {
		t.Fatalf("public-key enrollment status = %d, want 201", status)
	}
	if result.Role != "owner" || result.Serial == "" || result.CertificatePEM == "" || result.CACertificatePEM == "" {
		t.Fatalf("public-key enrollment response is incomplete: %#v", result)
	}

	identityRole, revoked, err := fixture.service.store.identityStatus(context.Background(), result.Serial)
	if err != nil {
		t.Fatalf("identityStatus() returned an error: %v", err)
	}
	if identityRole != enrollment.RoleOwner || revoked {
		t.Fatalf("persisted identity role/revocation = %q/%t, want owner/false", identityRole, revoked)
	}
}

func TestOnlyActiveOwnerCanIssuePairingCodesAndRevokeIdentity(t *testing.T) {
	t.Parallel()

	fixture := newRelayFixture(t)
	defer fixture.Close()
	ownerKey, ownerCSR := newDeviceCSR(t)
	status, owner := postEnrollment(t, fixture.server.Client(), fixture.server.URL, fixture.ownerCode, "owner", ownerCSR)
	if status != http.StatusCreated {
		t.Fatalf("owner enrollment status = %d, want 201", status)
	}
	ownerClient := fixture.authenticatedClient(t, ownerKey, owner.CertificatePEM)

	status, pairing := postPairing(t, ownerClient, fixture.server.URL, "client")
	if status != http.StatusCreated || len(pairing.Code) < 32 || pairing.Role != "client" {
		t.Fatalf("owner pairing response = status %d, %#v", status, pairing)
	}
	if _, err := time.Parse(time.RFC3339, pairing.ExpiresAt); err != nil {
		t.Fatalf("owner pairing expiry is not RFC3339: %q", pairing.ExpiresAt)
	}

	clientKey, clientCSR := newDeviceCSR(t)
	status, clientIdentity := postEnrollment(t, fixture.server.Client(), fixture.server.URL, pairing.Code, "client", clientCSR)
	if status != http.StatusCreated {
		t.Fatalf("client enrollment status = %d, want 201", status)
	}
	client := fixture.authenticatedClient(t, clientKey, clientIdentity.CertificatePEM)
	if status, _ := postPairing(t, client, fixture.server.URL, "agent"); status != http.StatusForbidden {
		t.Fatalf("client pairing status = %d, want 403", status)
	}
	if status, _ := postPairing(t, ownerClient, fixture.server.URL, "owner"); status != http.StatusBadRequest {
		t.Fatalf("additional owner pairing status = %d, want 400", status)
	}

	if status := postRevocation(t, ownerClient, fixture.server.URL, clientIdentity.Serial); status != http.StatusNoContent {
		t.Fatalf("owner revocation status = %d, want 204", status)
	}
	if status, _ := postPairing(t, client, fixture.server.URL, "agent"); status != http.StatusUnauthorized {
		t.Fatalf("revoked client authentication status = %d, want 401", status)
	}
	role, revoked, err := fixture.service.store.identityStatus(context.Background(), clientIdentity.Serial)
	if err != nil {
		t.Fatal(err)
	}
	if role != enrollment.RoleClient || !revoked {
		t.Fatalf("revoked identity state = %q/%t, want client/true", role, revoked)
	}
}

func newRelayFixture(t *testing.T) *relayFixture {
	t.Helper()
	stateDir := filepath.Join(t.TempDir(), "state")
	ownerCode, err := Initialize(context.Background(), InitOptions{StateDir: stateDir, PublicName: "127.0.0.1"})
	if err != nil {
		t.Fatalf("Initialize() returned an error: %v", err)
	}
	relay, err := Open(stateDir)
	if err != nil {
		t.Fatalf("Open() returned an error: %v", err)
	}
	server := httptest.NewUnstartedServer(relay.Handler())
	server.TLS = relay.TLSConfig()
	server.StartTLS()

	caPEM, err := os.ReadFile(filepath.Join(stateDir, caCertFilename))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		t.Fatal("failed to trust initialized CA")
	}
	transport := server.Client().Transport.(*http.Transport)
	transport.TLSClientConfig.RootCAs = roots
	transport.TLSClientConfig.InsecureSkipVerify = false
	return &relayFixture{service: relay, server: server, stateDir: stateDir, ownerCode: ownerCode}
}

func (fixture *relayFixture) Close() {
	fixture.server.Close()
	_ = fixture.service.Close()
}

func (fixture *relayFixture) authenticatedClient(t *testing.T, key crypto.Signer, certificatePEM string) *http.Client {
	t.Helper()
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := tls.X509KeyPair([]byte(certificatePEM), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	if err != nil {
		t.Fatalf("parse enrolled device certificate: %v", err)
	}
	baseTransport := fixture.server.Client().Transport.(*http.Transport)
	transport := baseTransport.Clone()
	transport.TLSClientConfig = baseTransport.TLSClientConfig.Clone()
	transport.TLSClientConfig.Certificates = []tls.Certificate{certificate}
	return &http.Client{Transport: transport}
}

func newDeviceCSR(t *testing.T) (crypto.Signer, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: "device-supplied-name-is-ignored"},
	}, key)
	if err != nil {
		t.Fatal(err)
	}
	return key, string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER}))
}

func postEnrollment(t *testing.T, client *http.Client, baseURL, code, role, csrPEM string) (int, enrollmentResult) {
	t.Helper()
	return postEnrollmentBody(t, client, baseURL, map[string]any{"code": code, "role": role, "csrPem": csrPEM})
}

func postEnrollmentBody(t *testing.T, client *http.Client, baseURL string, requestBody map[string]any) (int, enrollmentResult) {
	t.Helper()
	body, err := json.Marshal(requestBody)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Post(baseURL+"/v1/enroll", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /v1/enroll returned an error: %v", err)
	}
	defer response.Body.Close()
	var result enrollmentResult
	if response.StatusCode == http.StatusCreated {
		if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
			t.Fatalf("decode enrollment response: %v", err)
		}
	} else {
		_, _ = io.Copy(io.Discard, response.Body)
	}
	return response.StatusCode, result
}

func postPairing(t *testing.T, client *http.Client, baseURL, role string) (int, pairingResult) {
	t.Helper()
	body := bytes.NewBufferString(fmt.Sprintf(`{"role":%q}`, role))
	response, err := client.Post(baseURL+"/v1/pairing-codes", "application/json", body)
	if err != nil {
		t.Fatalf("POST /v1/pairing-codes returned an error: %v", err)
	}
	defer response.Body.Close()
	var result pairingResult
	if response.StatusCode == http.StatusCreated {
		if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
	}
	return response.StatusCode, result
}

func postRevocation(t *testing.T, client *http.Client, baseURL, serial string) int {
	t.Helper()
	body := bytes.NewBufferString(fmt.Sprintf(`{"serial":%q}`, serial))
	response, err := client.Post(baseURL+"/v1/revoke", "application/json", body)
	if err != nil {
		t.Fatalf("POST /v1/revoke returned an error: %v", err)
	}
	defer response.Body.Close()
	return response.StatusCode
}
