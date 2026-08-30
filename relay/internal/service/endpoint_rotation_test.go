package service

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRotateEndpointReissuesRelayLeafUnderExistingCA(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "state")
	_, ownerCSR := newDeviceCSR(t)
	if _, err := BootstrapOwner(context.Background(), BootstrapOwnerOptions{
		StateDir: stateDir, PublicName: "old-name.example.ts.net", PublicURL: "https://old-name.example.ts.net:8443", CSRPEM: ownerCSR,
	}); err != nil {
		t.Fatal(err)
	}
	oldCA, err := os.ReadFile(filepath.Join(stateDir, caCertFilename))
	if err != nil {
		t.Fatal(err)
	}
	oldLeaf := readLeafCertificate(t, filepath.Join(stateDir, relayCertFilename))

	result, err := RotateEndpoint(context.Background(), RotateEndpointOptions{
		StateDir: stateDir, PublicName: "new-name.example.ts.net", PublicURL: "https://new-name.example.ts.net:8443",
	})
	if err != nil {
		t.Fatalf("RotateEndpoint() returned an error: %v", err)
	}
	if result.PublicURL != "https://new-name.example.ts.net:8443" || result.Serial == "" {
		t.Fatalf("RotateEndpoint() result = %#v", result)
	}
	newCA, err := os.ReadFile(filepath.Join(stateDir, caCertFilename))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(oldCA, newCA) {
		t.Fatal("RotateEndpoint() changed the relay CA")
	}
	newLeaf := readLeafCertificate(t, filepath.Join(stateDir, relayCertFilename))
	if oldLeaf.SerialNumber.Cmp(newLeaf.SerialNumber) == 0 {
		t.Fatal("RotateEndpoint() reused the old relay leaf certificate")
	}
	if err := newLeaf.VerifyHostname("new-name.example.ts.net"); err != nil {
		t.Fatalf("rotated relay certificate does not cover the new endpoint: %v", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(newCA) {
		t.Fatal("parse relay CA")
	}
	if _, err := newLeaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: "new-name.example.ts.net", KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
		t.Fatalf("rotated relay certificate does not verify under existing CA: %v", err)
	}

	state, err := openStore(filepath.Join(stateDir, databaseFilename))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if relayURL, err := state.relayURL(context.Background()); err != nil || relayURL != result.PublicURL {
		t.Fatalf("persisted relay URL = %q/%v", relayURL, err)
	}
}

func TestOwnerIssuesAndAgentConsumesOneUseEndpointMigration(t *testing.T) {
	t.Parallel()

	fixture := newRelayFixture(t)
	defer fixture.Close()
	owner, devices := enrollDevices(t, fixture, "agent", "client")
	ownerClient := fixture.authenticatedClient(t, owner.key, owner.certificate)
	agentClient := fixture.authenticatedClient(t, devices[0].key, devices[0].certificate)
	clientClient := fixture.authenticatedClient(t, devices[1].key, devices[1].certificate)

	status, migration := postMigrationIssue(t, ownerClient, fixture.server.URL)
	if status != http.StatusCreated {
		t.Fatalf("Owner migration issue status = %d, want 201", status)
	}
	if migration.Version != 1 || migration.Type != "agent-endpoint-migration" || migration.Capability == "" || migration.RelayURL == "" || migration.CACertificatePEM == "" {
		t.Fatalf("migration response is incomplete: %#v", migration)
	}
	if _, err := time.Parse(time.RFC3339, migration.ExpiresAt); err != nil {
		t.Fatalf("migration expiry is invalid: %v", err)
	}
	if status, _ := postMigrationConsume(t, clientClient, fixture.server.URL, migration.Capability); status != http.StatusForbidden {
		t.Fatalf("Client migration consume status = %d, want 403", status)
	}
	status, consumed := postMigrationConsume(t, agentClient, fixture.server.URL, migration.Capability)
	if status != http.StatusOK || consumed.RelayURL != migration.RelayURL {
		t.Fatalf("Agent migration consume = %d/%#v", status, consumed)
	}
	if status, _ := postMigrationConsume(t, agentClient, fixture.server.URL, migration.Capability); status != http.StatusUnauthorized {
		t.Fatalf("reused migration consume status = %d, want 401", status)
	}
}

type migrationIssueResult struct {
	Version          int    `json:"version"`
	Type             string `json:"type"`
	RelayURL         string `json:"relayUrl"`
	CACertificatePEM string `json:"caCertificatePem"`
	Capability       string `json:"capability"`
	ExpiresAt        string `json:"expiresAt"`
}

type migrationConsumeResult struct {
	RelayURL string `json:"relayUrl"`
}

func postMigrationIssue(t *testing.T, client *http.Client, baseURL string) (int, migrationIssueResult) {
	t.Helper()
	response, err := client.Post(baseURL+"/v1/endpoint-migrations", "application/json", bytes.NewBufferString("{}"))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result migrationIssueResult
	if response.StatusCode == http.StatusCreated {
		if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
	}
	return response.StatusCode, result
}

func postMigrationConsume(t *testing.T, client *http.Client, baseURL, capability string) (int, migrationConsumeResult) {
	t.Helper()
	body, err := json.Marshal(map[string]string{"capability": capability})
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Post(baseURL+"/v1/endpoint-migrations/consume", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result migrationConsumeResult
	if response.StatusCode == http.StatusOK {
		if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
	}
	return response.StatusCode, result
}

func readLeafCertificate(t *testing.T, path string) *x509.Certificate {
	t.Helper()
	certificatePEM, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(certificatePEM)
	if block == nil {
		t.Fatal("relay certificate is not PEM")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return certificate
}

func TestRotatedRelayKeyStillLoadsAsTLSIdentity(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "state")
	_, csr := newDeviceCSR(t)
	if _, err := BootstrapOwner(context.Background(), BootstrapOwnerOptions{
		StateDir: stateDir, PublicName: "old.example.ts.net", PublicURL: "https://old.example.ts.net:8443", CSRPEM: csr,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := RotateEndpoint(context.Background(), RotateEndpointOptions{
		StateDir: stateDir, PublicName: "new.example.ts.net", PublicURL: "https://new.example.ts.net:8443",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tls.LoadX509KeyPair(filepath.Join(stateDir, relayCertFilename), filepath.Join(stateDir, relayKeyFilename)); err != nil {
		t.Fatalf("rotated TLS identity does not load: %v", err)
	}
}
