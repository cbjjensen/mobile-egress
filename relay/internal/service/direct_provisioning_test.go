package service

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"mobile-egress/relay/internal/enrollment"
)

func TestBootstrapOwnerCreatesIdentityWithoutPairingCapability(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "state")
	ownerKey, ownerCSR := newDeviceCSR(t)
	result, err := BootstrapOwner(context.Background(), BootstrapOwnerOptions{
		StateDir:   stateDir,
		PublicName: "relay.example.ts.net",
		PublicURL:  "https://relay.example.ts.net:8443",
		CSRPEM:     ownerCSR,
	})
	if err != nil {
		t.Fatalf("BootstrapOwner() returned an error: %v", err)
	}
	if result.Role != enrollment.RoleOwner || result.Serial == "" || result.CertificatePEM == "" || result.CACertificatePEM == "" {
		t.Fatalf("BootstrapOwner() result is incomplete: %#v", result)
	}
	assertCertificateMatchesKey(t, result.CertificatePEM, ownerKey.Public())

	state, err := openStore(filepath.Join(stateDir, databaseFilename))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	count, err := state.capabilityCount(context.Background(), string(enrollment.RoleOwner))
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("owner capability count = %d, want 0", count)
	}
	role, revoked, err := state.identityStatus(context.Background(), result.Serial)
	if err != nil || role != enrollment.RoleOwner || revoked {
		t.Fatalf("bootstrapped identity = %q/%t/%v, want active owner", role, revoked, err)
	}
	if endpoint, err := state.relayURL(context.Background()); err != nil || endpoint != "https://relay.example.ts.net:8443" {
		t.Fatalf("persisted relay URL = %q/%v", endpoint, err)
	}
}

func TestBootstrapOwnerCanRecoverInitializedStateBeforeOwnerExists(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "state")
	if _, err := Initialize(context.Background(), InitOptions{
		StateDir: stateDir, PublicName: "bridge.example.ts.net", PublicURL: "https://bridge.example.ts.net:8443",
	}); err != nil {
		t.Fatal(err)
	}
	ownerKey, ownerCSR := newDeviceCSR(t)
	result, err := BootstrapOwner(context.Background(), BootstrapOwnerOptions{
		StateDir: stateDir, PublicName: "bridge.example.ts.net", PublicURL: "https://bridge.example.ts.net:8443", CSRPEM: ownerCSR,
	})
	if err != nil {
		t.Fatalf("BootstrapOwner() returned an error: %v", err)
	}
	assertCertificateMatchesKey(t, result.CertificatePEM, ownerKey.Public())

	store, err := openStore(filepath.Join(stateDir, databaseFilename))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	role, revoked, err := store.identityStatus(context.Background(), result.Serial)
	if err != nil || role != "owner" || revoked {
		t.Fatalf("recovered owner identity = %q/%t/%v, want active owner", role, revoked, err)
	}
}

func TestBootstrapOwnerRejectsRecoveryWhenOwnerAlreadyExists(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "state")
	_, ownerCSR := newDeviceCSR(t)
	if _, err := BootstrapOwner(context.Background(), BootstrapOwnerOptions{
		StateDir: stateDir, PublicName: "bridge.example.ts.net", PublicURL: "https://bridge.example.ts.net:8443", CSRPEM: ownerCSR,
	}); err != nil {
		t.Fatal(err)
	}
	_, secondCSR := newDeviceCSR(t)
	if _, err := BootstrapOwner(context.Background(), BootstrapOwnerOptions{
		StateDir: stateDir, PublicName: "bridge.example.ts.net", PublicURL: "https://bridge.example.ts.net:8443", CSRPEM: secondCSR,
	}); err == nil {
		t.Fatal("BootstrapOwner() replaced an existing Owner")
	}
}

func TestOwnerCanProvisionClientCSRDirectly(t *testing.T) {
	t.Parallel()

	fixture := newRelayFixture(t)
	defer fixture.Close()
	ownerKey, ownerCSR := newDeviceCSR(t)
	status, owner := postEnrollment(t, fixture.server.Client(), fixture.server.URL, fixture.ownerCode, "owner", ownerCSR)
	if status != http.StatusCreated {
		t.Fatalf("owner enrollment status = %d", status)
	}
	ownerClient := fixture.authenticatedClient(t, ownerKey, owner.CertificatePEM)

	clientKey, clientCSR := newDeviceCSR(t)
	status, result := postClientCSR(t, ownerClient, fixture.server.URL, clientCSR)
	if status != http.StatusCreated {
		t.Fatalf("direct client provisioning status = %d, want 201", status)
	}
	if result.Role != string(enrollment.RoleClient) || result.Serial == "" {
		t.Fatalf("direct client provisioning result = %#v", result)
	}
	assertCertificateMatchesKey(t, result.CertificatePEM, clientKey.Public())

	client := fixture.authenticatedClient(t, clientKey, result.CertificatePEM)
	_, anotherCSR := newDeviceCSR(t)
	if status, _ := postClientCSR(t, client, fixture.server.URL, anotherCSR); status != http.StatusForbidden {
		t.Fatalf("Client direct provisioning status = %d, want 403", status)
	}
	if status, _ := postClientCSR(t, ownerClient, fixture.server.URL, "not a CSR"); status != http.StatusBadRequest {
		t.Fatalf("invalid CSR status = %d, want 400", status)
	}
	for index := 1; index < 10; index++ {
		_, csr := newDeviceCSR(t)
		if status, _ := postClientCSR(t, ownerClient, fixture.server.URL, csr); status != http.StatusCreated {
			t.Fatalf("direct Client %d status = %d, want 201", index+1, status)
		}
	}
	_, eleventhCSR := newDeviceCSR(t)
	if status, _ := postClientCSR(t, ownerClient, fixture.server.URL, eleventhCSR); status != http.StatusConflict {
		t.Fatalf("eleventh direct Client status = %d, want 409", status)
	}
}

func TestRelayProductionStreamLimitsMatchCapacityContract(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "state")
	if _, err := Initialize(context.Background(), InitOptions{StateDir: stateDir, PublicName: "127.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	relay, err := Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()
	if relay.maxClientStreams != 256 || relay.maxAgentStreams != 256 {
		t.Fatalf("stream limits = %d per Client/%d aggregate, want 256/256", relay.maxClientStreams, relay.maxAgentStreams)
	}
}

func postClientCSR(t *testing.T, client *http.Client, baseURL, csrPEM string) (int, enrollmentResult) {
	t.Helper()
	body, err := json.Marshal(map[string]string{"csrPem": csrPEM})
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Post(baseURL+"/v1/clients", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result enrollmentResult
	if response.StatusCode == http.StatusCreated {
		if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
	}
	return response.StatusCode, result
}

func assertCertificateMatchesKey(t *testing.T, certificatePEM string, publicKey any) {
	t.Helper()
	block, _ := pem.Decode([]byte(certificatePEM))
	if block == nil {
		t.Fatal("certificate response does not contain a PEM certificate")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	encodedCertificateKey, err := x509.MarshalPKIXPublicKey(certificate.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	encodedExpectedKey, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encodedCertificateKey, encodedExpectedKey) {
		t.Fatal("issued certificate does not match the CSR key")
	}
}

func TestBootstrapOwnerDoesNotPersistCSR(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "state")
	_, csr := newDeviceCSR(t)
	if _, err := BootstrapOwner(context.Background(), BootstrapOwnerOptions{
		StateDir: stateDir, PublicName: "relay.example.ts.net", PublicURL: "https://relay.example.ts.net:8443", CSRPEM: csr,
	}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{caCertFilename, caKeyFilename, relayCertFilename, relayKeyFilename, databaseFilename} {
		contents, err := os.ReadFile(filepath.Join(stateDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(contents, []byte(csr)) {
			t.Fatalf("%s persisted the Owner CSR", name)
		}
	}
}
