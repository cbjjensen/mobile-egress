//go:build windows

package desktop

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"reflect"
	"strings"
	"testing"
	"time"

	"mobile-egress/pairing"
	"mobile-egress/windows-client/internal/client"
	"mobile-egress/windows-client/internal/relayclient"
	"mobile-egress/windows-client/internal/securestore"
)

func TestGetStatusReportsOwnerAndClientReadinessWithoutIdentitySecrets(t *testing.T) {
	t.Parallel()

	owner := desktopIdentity("owner", "OWNER")
	store := securestore.NewMemoryStore()
	if err := client.NewRepository(store).SaveOwnerIdentity(context.Background(), owner); err != nil {
		t.Fatal(err)
	}
	core, err := client.NewCore(context.Background(), store, desktopGateway{})
	if err != nil {
		t.Fatal(err)
	}

	status := (&DesktopApp{core: core}).GetStatus()
	if !status.OwnerReady || status.ClientReady || status.Proxy != "" {
		t.Fatalf("GetStatus() = %#v, want owner ready, client not ready, and no proxy endpoint", status)
	}
}

func TestBootstrapOwnerRedactsMalformedInvitationErrors(t *testing.T) {
	t.Parallel()

	core, err := client.NewCore(context.Background(), securestore.NewMemoryStore(), desktopGateway{})
	if err != nil {
		t.Fatal(err)
	}

	err = (&DesktopApp{core: core}).BootstrapOwner("owner-invitation-secret")
	if err == nil {
		t.Fatal("BootstrapOwner() accepted a malformed invitation")
	}
	if got, want := err.Error(), "Unable to complete secure setup. Verify the owner invitation and try again."; got != want {
		t.Fatalf("BootstrapOwner() error = %q, want redacted error %q", got, want)
	}
}

func TestIssueAgentQrReturnsOnlyExpiringPNGDataURL(t *testing.T) {
	t.Parallel()

	owner := desktopIdentity("owner", "OWNER")
	owner.CACertificatePEM = string(desktopTestCA(t))
	store := securestore.NewMemoryStore()
	if err := client.NewRepository(store).SaveOwnerIdentity(context.Background(), owner); err != nil {
		t.Fatal(err)
	}
	core, err := client.NewCore(context.Background(), store, desktopGateway{})
	if err != nil {
		t.Fatal(err)
	}

	view, err := (&DesktopApp{core: core}).IssueAgentQr()
	if err != nil {
		t.Fatal(err)
	}
	if view.ExpiresAt == "" {
		t.Fatal("IssueAgentQr() returned no expiry")
	}
	expiresAt, err := time.Parse(time.RFC3339, view.ExpiresAt)
	if err != nil {
		t.Fatalf("IssueAgentQr() expiry = %q: %v", view.ExpiresAt, err)
	}
	if !expiresAt.After(time.Now()) {
		t.Fatalf("IssueAgentQr() expiry = %s, want a future expiry", expiresAt)
	}
	if !strings.HasPrefix(view.ImageDataURL, "data:image/png;base64,") {
		t.Fatalf("IssueAgentQr() image = %q, want a PNG data URL", view.ImageDataURL)
	}
	png, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(view.ImageDataURL, "data:image/png;base64,"))
	if err != nil {
		t.Fatalf("IssueAgentQr() image is not base64: %v", err)
	}
	if len(png) < 8 || string(png[:8]) != "\x89PNG\r\n\x1a\n" {
		t.Fatalf("IssueAgentQr() image is not a PNG: %x", png)
	}
}

func TestAgentQrViewDoesNotExposeInvitationText(t *testing.T) {
	t.Parallel()

	viewType := reflect.TypeFor[AgentQrView]()
	if viewType.NumField() != 2 {
		t.Fatalf("AgentQrView exposes %d fields, want only image data and expiry", viewType.NumField())
	}
	if field, ok := viewType.FieldByName("ImageDataURL"); !ok || field.Tag.Get("json") != "imageDataUrl" {
		t.Fatalf("AgentQrView image field = %#v, want imageDataUrl", field)
	}
	if field, ok := viewType.FieldByName("ExpiresAt"); !ok || field.Tag.Get("json") != "expiresAt" {
		t.Fatalf("AgentQrView expiry field = %#v, want expiresAt", field)
	}
}

type desktopGateway struct{}

func (desktopGateway) Enroll(context.Context, pairing.Bundle) (relayclient.Identity, error) {
	return relayclient.Identity{}, nil
}

func (desktopGateway) DialSession(context.Context, relayclient.Identity) (client.Tunnel, error) {
	return nil, nil
}

func (desktopGateway) IssuePairing(context.Context, relayclient.Identity, string) (relayclient.PairingCode, error) {
	return relayclient.PairingCode{Code: "agent-invitation", Role: "agent", ExpiresAt: time.Now().Add(time.Minute)}, nil
}

func (desktopGateway) Revoke(context.Context, relayclient.Identity, string) error { return nil }

func desktopIdentity(role, serial string) relayclient.Identity {
	return relayclient.Identity{
		RelayURL: "https://relay.example", Role: role, Serial: serial,
		PrivateKeyPEM: role + "-key", CertificatePEM: role + "-chain", CACertificatePEM: "ca",
	}
}

func desktopTestCA(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "desktop-test-ca"},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}
