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
	"errors"
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

func TestCoreOwnerSinkAdoptsSetupIdentityThenUpdatesOnlyItsEndpoint(t *testing.T) {
	t.Parallel()

	store := securestore.NewMemoryStore()
	core, err := client.NewCore(context.Background(), store, desktopGateway{})
	if err != nil {
		t.Fatal(err)
	}
	sink := coreOwnerSink{core: core}
	identity := desktopIdentity("owner", "A11CE")
	identity.RelayURL = "https://old.tail123.ts.net:8443"
	identity.DialAddress = "127.0.0.1:8443"
	if err := sink.SaveOwnerIdentity(context.Background(), identity); err != nil {
		t.Fatalf("SaveOwnerIdentity() = %v", err)
	}
	identity.RelayURL = "https://new.tail123.ts.net:8443"
	if err := sink.UpdateOwnerIdentity(context.Background(), identity); err != nil {
		t.Fatalf("UpdateOwnerIdentity() = %v", err)
	}
	stored, _, err := client.NewRepository(store).LoadOwnerIdentity(context.Background())
	if err != nil || stored != identity {
		t.Fatalf("stored Owner after endpoint update = %#v/%v", stored, err)
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

func TestSignedNodeReleaseManifestIsStrictAndGitHubBound(t *testing.T) {
	t.Parallel()

	raw := []byte(`{"version":1,"client":{"version":"1.2.3","url":"https://github.com/cbjjensen/mobile-egress/releases/download/v1.2.3/mobile-egress-client.exe","sha256":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","signerThumbprint":"0123456789abcdef0123456789abcdef01234567"}}`)
	release, err := decodeNodeReleaseManifest(raw)
	if err != nil || release.Version != "1.2.3" || release.SignerThumbprint != "0123456789abcdef0123456789abcdef01234567" {
		t.Fatalf("decodeNodeReleaseManifest() = %#v/%v", release, err)
	}
	for _, invalid := range [][]byte{
		append(append([]byte(nil), raw...), []byte(` {}`)...),
		[]byte(`{"version":1,"client":{"version":"1.2.3","url":"https://example.com/client.exe","sha256":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}}`),
		[]byte(`{"version":1,"client":{"version":"1.2.3","url":"https://github.com/cbjjensen/mobile-egress/releases/download/v1.2.3/mobile-egress-client.exe","sha256":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","publisher":"Attacker Selects This"}}`),
	} {
		if _, err := decodeNodeReleaseManifest(invalid); err == nil {
			t.Fatalf("decodeNodeReleaseManifest() accepted %s", invalid)
		}
	}
}

func TestNodeReleaseTrustLoadsOnlyFromTheSignedControllerEmbedding(t *testing.T) {
	raw := []byte(`{"version":1,"client":{"version":"1.2.3","url":"https://github.com/cbjjensen/mobile-egress/releases/download/v1.2.3/mobile-egress-client.exe","sha256":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","signerThumbprint":"0123456789abcdef0123456789abcdef01234567"}}`)
	previous := embeddedReleaseManifestBase64
	embeddedReleaseManifestBase64 = base64.StdEncoding.EncodeToString(raw)
	t.Cleanup(func() { embeddedReleaseManifestBase64 = previous })

	release, err := loadNodeRelease()
	if err != nil || release.Version != "1.2.3" || release.SignerThumbprint == "" {
		t.Fatalf("loadNodeRelease() = %#v/%v", release, err)
	}
}

func TestReplaceClientReturnsOnlyAGenericRecoveryError(t *testing.T) {
	t.Parallel()

	owner := desktopIdentity("owner", "0A11CE")
	clientIdentity := desktopIdentity("client", "C11E17")
	store := securestore.NewMemoryStore()
	repository := client.NewRepository(store)
	if err := repository.SaveOwnerIdentity(context.Background(), owner); err != nil {
		t.Fatal(err)
	}
	if err := repository.SaveClientIdentity(context.Background(), clientIdentity); err != nil {
		t.Fatal(err)
	}
	core, err := client.NewCore(context.Background(), store, desktopGateway{enrollErr: errors.New("sensitive relay detail")})
	if err != nil {
		t.Fatal(err)
	}
	replacer, ok := any(&DesktopApp{core: core}).(interface{ ReplaceClient() error })
	if !ok {
		t.Fatal("DesktopApp does not expose ReplaceClient")
	}

	err = replacer.ReplaceClient()
	if err == nil {
		t.Fatal("ReplaceClient() succeeded when enrollment failed")
	}
	if got, want := err.Error(), "Unable to replace the local Windows Client. Please try again."; got != want {
		t.Fatalf("ReplaceClient() error = %q, want generic error %q", got, want)
	}
}

func TestRevokeReturnsOnlyAGenericOwnerControlError(t *testing.T) {
	t.Parallel()

	owner := desktopIdentity("owner", "0A11CE")
	store := securestore.NewMemoryStore()
	if err := client.NewRepository(store).SaveOwnerIdentity(context.Background(), owner); err != nil {
		t.Fatal(err)
	}
	core, err := client.NewCore(context.Background(), store, desktopGateway{revokeErr: errors.New("sensitive relay detail")})
	if err != nil {
		t.Fatal(err)
	}

	err = (&DesktopApp{core: core}).Revoke("C11E17")
	if err == nil {
		t.Fatal("Revoke() succeeded when the relay rejected it")
	}
	if got, want := err.Error(), "Unable to revoke that certificate. Verify the serial and try again."; got != want {
		t.Fatalf("Revoke() error = %q, want generic error %q", got, want)
	}
}

type desktopGateway struct {
	enrollErr error
	revokeErr error
}

func (gateway desktopGateway) Enroll(context.Context, pairing.Bundle) (relayclient.Identity, error) {
	return relayclient.Identity{}, gateway.enrollErr
}

func (desktopGateway) DialSession(context.Context, relayclient.Identity) (client.Tunnel, error) {
	return nil, nil
}

func (desktopGateway) IssuePairing(_ context.Context, _ relayclient.Identity, role string) (relayclient.PairingCode, error) {
	return relayclient.PairingCode{Code: "agent-invitation", Role: role, ExpiresAt: time.Now().Add(time.Minute)}, nil
}

func (gateway desktopGateway) Revoke(context.Context, relayclient.Identity, string) error {
	return gateway.revokeErr
}

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
