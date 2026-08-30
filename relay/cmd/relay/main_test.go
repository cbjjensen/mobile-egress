package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mobile-egress/relay/internal/service"
)

func TestRunInitIsNotAPublicCommand(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if status := run([]string{"init"}, &stdout, &stderr); status != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "bootstrap-owner") {
		t.Fatalf("relay init = status %d, stdout %q, stderr %q", status, stdout.String(), stderr.String())
	}
}

func TestRunServeRefusesMissingState(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	status := run([]string{"serve", "--state-dir", filepath.Join(t.TempDir(), "missing"), "--listen", "127.0.0.1:0"}, &stdout, &stderr)
	if status == 0 {
		t.Fatal("relay serve accepted missing initialized state")
	}
	if stdout.Len() != 0 {
		t.Fatalf("relay serve wrote unexpected stdout: %q", stdout.String())
	}
}

func TestRunBootstrapOwnerPrintsPublicIdentityWithoutInvitation(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "state")
	csrPath := filepath.Join(t.TempDir(), "owner.csr")
	if err := os.WriteFile(csrPath, []byte(testCSR(t)), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	status := run([]string{
		"bootstrap-owner", "--state-dir", stateDir, "--public-name", "relay.example.ts.net",
		"--public-url", "https://relay.example.ts.net:8443", "--owner-csr-file", csrPath,
	}, &stdout, &stderr)
	if status != 0 {
		t.Fatalf("bootstrap-owner status = %d, stderr = %s", status, stderr.String())
	}
	if strings.Contains(strings.ToLower(stdout.String()), "invitation") || strings.Contains(stdout.String(), "Owner pairing bundle") {
		t.Fatalf("bootstrap-owner exposed an invitation: %s", stdout.String())
	}
	var result service.EnrollmentResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("bootstrap-owner output is not JSON: %v", err)
	}
	if result.Role != "owner" || result.CertificatePEM == "" || result.CACertificatePEM == "" || result.Serial == "" {
		t.Fatalf("bootstrap-owner output is incomplete: %#v", result)
	}
}

func TestRunRotateEndpointAndVersion(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "state")
	csrPath := filepath.Join(t.TempDir(), "owner.csr")
	if err := os.WriteFile(csrPath, []byte(testCSR(t)), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if status := run([]string{
		"bootstrap-owner", "--state-dir", stateDir, "--public-name", "old.example.ts.net",
		"--public-url", "https://old.example.ts.net:8443", "--owner-csr-file", csrPath,
	}, &stdout, &stderr); status != 0 {
		t.Fatalf("bootstrap-owner failed: %s", stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if status := run([]string{
		"rotate-endpoint", "--state-dir", stateDir, "--public-name", "new.example.ts.net",
		"--public-url", "https://new.example.ts.net:8443",
	}, &stdout, &stderr); status != 0 {
		t.Fatalf("rotate-endpoint status = %d, stderr = %s", status, stderr.String())
	}
	var rotated service.RotateEndpointResult
	if err := json.Unmarshal(stdout.Bytes(), &rotated); err != nil || rotated.PublicURL != "https://new.example.ts.net:8443" || rotated.Serial == "" {
		t.Fatalf("rotate-endpoint output = %q/%#v/%v", stdout.String(), rotated, err)
	}
	stdout.Reset()
	stderr.Reset()
	if status := run([]string{"--version"}, &stdout, &stderr); status != 0 || strings.TrimSpace(stdout.String()) == "" || stderr.Len() != 0 {
		t.Fatalf("--version = status %d, stdout %q, stderr %q", status, stdout.String(), stderr.String())
	}
}

func testCSR(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	request, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: "test owner"},
	}, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: request}))
}
