//go:build windows

package desktop

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"image/png"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"mobile-egress/pairing"
	"mobile-egress/windows-client/internal/client"
	"mobile-egress/windows-client/internal/cloud"
	"mobile-egress/windows-client/internal/localbridge"
	"mobile-egress/windows-client/internal/relayclient"
	"mobile-egress/windows-client/internal/securestore"
	"mobile-egress/windows-client/internal/tailscale"
)

func TestEncodeQrPNGUsesFourPixelModulesForDensePairingPayload(t *testing.T) {
	t.Parallel()

	encoded := strings.Repeat("a", 1000)
	encodedPNG, err := encodeQrPNG(encoded)
	if err != nil {
		t.Fatal(err)
	}
	configuration, err := png.DecodeConfig(bytes.NewReader(encodedPNG))
	if err != nil {
		t.Fatal(err)
	}
	if configuration.Width != 516 || configuration.Height != 516 {
		t.Fatalf("dense QR dimensions = %dx%d, want 516x516 for four-pixel modules", configuration.Width, configuration.Height)
	}
}

func TestControllerUsesASingleInstanceLock(t *testing.T) {
	t.Parallel()

	lock := controllerSingleInstanceLock(&DesktopApp{})
	if lock == nil || lock.UniqueId == "" || lock.OnSecondInstanceLaunch == nil {
		t.Fatalf("controller single-instance lock = %#v", lock)
	}
}

func TestFunnelApprovalUsesTheDesktopBrowserWhenRuntimeIsReady(t *testing.T) {
	t.Parallel()

	var openedURL string
	app := &DesktopApp{
		ctx: context.Background(),
		browserOpenURL: func(_ context.Context, approvalURL string) {
			openedURL = approvalURL
		},
	}
	app.openFunnelApproval("https://login.tailscale.com/f/funnel?node=test-node")
	if got, want := openedURL, "https://login.tailscale.com/f/funnel?node=test-node"; got != want {
		t.Fatalf("opened URL = %q, want %q", got, want)
	}

	app.ctx = nil
	app.openFunnelApproval("https://login.tailscale.com/f/funnel?node=other-node")
	if got, want := openedURL, "https://login.tailscale.com/f/funnel?node=test-node"; got != want {
		t.Fatalf("URL changed without a runtime context: got %q, want %q", got, want)
	}
}

func TestOpenAWSIdentityCenterConsoleUsesUsEastDashboard(t *testing.T) {
	t.Parallel()

	var openedURL string
	app := &DesktopApp{
		ctx: context.Background(),
		browserOpenURL: func(_ context.Context, consoleURL string) {
			openedURL = consoleURL
		},
	}
	if err := app.OpenAWSIdentityCenterConsole(); err != nil {
		t.Fatal(err)
	}
	if got, want := openedURL, "https://console.aws.amazon.com/singlesignon/home?region=us-east-1#/dashboard"; got != want {
		t.Fatalf("opened URL = %q, want %q", got, want)
	}
}

func TestOpenAWSIAMUserCreateConsoleUsesUsEastCreateUser(t *testing.T) {
	t.Parallel()

	var openedURL string
	app := &DesktopApp{
		ctx: context.Background(),
		browserOpenURL: func(_ context.Context, consoleURL string) {
			openedURL = consoleURL
		},
	}
	if err := app.OpenAWSIAMUserCreateConsole(); err != nil {
		t.Fatal(err)
	}
	if got, want := openedURL, "https://console.aws.amazon.com/iam/home?region=us-east-1#/users/create"; got != want {
		t.Fatalf("opened URL = %q, want %q", got, want)
	}
}

func TestSSMPreparationErrorKeepsTheSafeFailingStage(t *testing.T) {
	err := formatSSMPreparationError(errors.New("create dedicated SSM instance profile: create dedicated SSM IAM role"))
	if err == nil || !strings.Contains(err.Error(), "create dedicated SSM IAM role") {
		t.Fatalf("formatSSMPreparationError() = %v, want safe failing stage", err)
	}
}

func TestNodeInstallErrorShowsOnlyTheApprovedFailureStage(t *testing.T) {
	staged := fmt.Errorf("orchestration wrapper: %w", cloud.NewSSMCommandFailure("pretrust-signature"))
	err := formatNodeInstallError(staged)
	if got, want := err.Error(), "Unable to install the Client node through Systems Manager during pre-trust signature verification. No EC2 networking was changed."; got != want {
		t.Fatalf("formatNodeInstallError() = %q, want %q", got, want)
	}

	err = formatNodeInstallError(errors.New("private-output-marker"))
	if strings.Contains(err.Error(), "private-output-marker") || !strings.Contains(err.Error(), "Unable to install the Client node") {
		t.Fatalf("formatNodeInstallError() exposed an unapproved error: %v", err)
	}
}

func TestNodeUpdateErrorShowsOnlyTheApprovedFailureStage(t *testing.T) {
	staged := fmt.Errorf("orchestration wrapper: %w", cloud.NewSSMCommandFailure("service-start"))
	err := formatNodeUpdateError(staged)
	if got, want := err.Error(), "Unable to update the signed Client service through Systems Manager during Client service startup."; got != want {
		t.Fatalf("formatNodeUpdateError() = %q, want %q", got, want)
	}

	err = formatNodeUpdateError(errors.New("private-output-marker"))
	if strings.Contains(err.Error(), "private-output-marker") || !strings.Contains(err.Error(), "Unable to update the signed Client service") {
		t.Fatalf("formatNodeUpdateError() exposed an unapproved error: %v", err)
	}
}

func TestWithInstanceSSMStatusUpdatesOnlyTheSelectedInstance(t *testing.T) {
	t.Parallel()

	inventory := []cloud.Instance{{ID: "i-0123456789abcdef0"}, {ID: "i-fedcba98765432100"}}
	updated := withInstanceSSMStatus(inventory, "i-0123456789abcdef0", true)
	if !updated[0].SSMOnline || updated[1].SSMOnline {
		t.Fatalf("updated inventory = %#v", updated)
	}
	if inventory[0].SSMOnline {
		t.Fatal("withInstanceSSMStatus mutated the source inventory")
	}
}

func TestInstallTailscaleReportsTheSafeFailingStage(t *testing.T) {
	app := &DesktopApp{tailscaleInstall: tailscale.Installer{}}
	err := app.InstallTailscale()
	if err == nil || !strings.Contains(err.Error(), "verification and elevation are required") {
		t.Fatalf("InstallTailscale() error = %v, want safe installer stage", err)
	}
}

func TestGetBridgeStatusDistinguishesInstalledTailscaleFromOnlineTailscale(t *testing.T) {
	t.Parallel()

	executable := filepath.Join(t.TempDir(), "tailscale.exe")
	if err := os.WriteFile(executable, []byte("test executable"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := securestore.NewMemoryStore()
	core, err := client.NewCore(context.Background(), store, desktopGateway{})
	if err != nil {
		t.Fatal(err)
	}
	app := &DesktopApp{
		core:            core,
		ownerRepository: client.NewRepository(store),
		tailscale:       tailscale.NewController(executable, offlineTailscaleRunner{}),
	}

	status := app.GetBridgeStatus()
	if !status.TailscaleInstalled || status.TailscaleOnline {
		t.Fatalf("GetBridgeStatus() = %#v, want installed and offline", status)
	}
}

func TestInstallTailscaleRejectsDuplicateInstallationBeforeCallingInstaller(t *testing.T) {
	t.Parallel()

	executable := filepath.Join(t.TempDir(), "tailscale.exe")
	if err := os.WriteFile(executable, []byte("test executable"), 0o600); err != nil {
		t.Fatal(err)
	}
	installer := &recordingTailscaleInstaller{}
	app := &DesktopApp{
		tailscale:        tailscale.NewController(executable, offlineTailscaleRunner{}),
		tailscaleInstall: installer,
	}

	err := app.InstallTailscale()
	if err == nil || !strings.Contains(err.Error(), "already installed") {
		t.Fatalf("InstallTailscale() error = %v, want already-installed guidance", err)
	}
	if installer.calls != 0 {
		t.Fatalf("installer calls = %d, want 0", installer.calls)
	}
}

func TestSetupLocalBridgeIncludesTheFailingStage(t *testing.T) {
	t.Parallel()

	app := &DesktopApp{bridge: &localbridge.Manager{}}
	_, err := app.SetupLocalBridge()
	if err == nil {
		t.Fatal("SetupLocalBridge() succeeded without local bridge dependencies")
	}
	if !strings.Contains(err.Error(), "local bridge setup dependencies are required") {
		t.Fatalf("SetupLocalBridge() error = %q, want underlying setup stage", err.Error())
	}
}

func TestReservationCancellationRequiresExplicitConfirmation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repository := cloud.NewRepository(securestore.NewMemoryStore())
	instanceID := "i-0123456789abcdef0"
	if err := repository.ReserveNode(ctx, instanceID); err != nil {
		t.Fatal(err)
	}
	app := &DesktopApp{cloudRepository: repository}
	if err := app.CancelEC2NodeReservation(instanceID, false); err == nil {
		t.Fatal("CancelEC2NodeReservation() accepted missing confirmation")
	}
	pending, err := app.PendingEC2NodeReservations()
	if err != nil || len(pending) != 1 || pending[0] != instanceID {
		t.Fatalf("pending reservations after rejected cancellation = %#v/%v", pending, err)
	}
	if err := app.CancelEC2NodeReservation(instanceID, true); err != nil {
		t.Fatal(err)
	}
	pending, err = app.PendingEC2NodeReservations()
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending reservations after confirmed cancellation = %#v/%v", pending, err)
	}
}

func TestManagedNodeBindingsExposeHTTPLineAndSOCKSFallback(t *testing.T) {
	t.Parallel()

	repository := cloud.NewRepository(securestore.NewMemoryStore())
	node := cloud.ManagedNode{
		InstanceID: "i-0123456789abcdef0", ClientSerial: "A1", ConfigurationPublicKey: "public", ConfigurationGeneration: 1,
		ServiceVersion: "1.0.22", Health: "installed", SOCKSUsername: "node-user", SOCKSPassword: "node-password", SOCKSPort: 1080,
		RelayURL: "https://bridge.tail123.ts.net:8443", CertificatePEM: "certificate", CACertificatePEM: "ca",
	}
	if err := repository.SaveNode(context.Background(), node); err != nil {
		t.Fatal(err)
	}
	app := &DesktopApp{cloudRepository: repository}
	if line, err := app.NodeProxyLine(node.InstanceID); err != nil || line != "127.0.0.1:1081:node-user:node-password" {
		t.Fatalf("NodeProxyLine() = %q/%v", line, err)
	}
	if socksURL, err := app.NodeSOCKSProxyURL(node.InstanceID); err != nil || socksURL != "socks5://node-user:node-password@127.0.0.1:1080" {
		t.Fatalf("NodeSOCKSProxyURL() = %q/%v", socksURL, err)
	}
}

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

	raw, expected := testNodeReleaseManifest(t, 2)
	release, err := decodeNodeReleaseManifest(raw)
	if err != nil || release != expected {
		t.Fatalf("decodeNodeReleaseManifest() = %#v/%v", release, err)
	}
	v1, _ := testNodeReleaseManifest(t, 1)
	offDomain := expected
	offDomain.URL = "https://example.com/client.exe"
	offDomainRaw := marshalNodeReleaseManifest(t, 2, offDomain)
	var unknownField map[string]any
	if err := json.Unmarshal(raw, &unknownField); err != nil {
		t.Fatal(err)
	}
	unknownField["publisher"] = "Attacker Selects This"
	unknownFieldRaw, err := json.Marshal(unknownField)
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range [][]byte{
		append(append([]byte(nil), raw...), []byte(` {}`)...),
		v1,
		offDomainRaw,
		unknownFieldRaw,
	} {
		if _, err := decodeNodeReleaseManifest(invalid); err == nil {
			t.Fatalf("decodeNodeReleaseManifest() accepted %s", invalid)
		}
	}
}

func TestNodeReleaseTrustLoadsOnlyFromTheSignedControllerEmbedding(t *testing.T) {
	raw, expected := testNodeReleaseManifest(t, 2)
	previous := embeddedReleaseManifestBase64
	embeddedReleaseManifestBase64 = base64.StdEncoding.EncodeToString(raw)
	t.Cleanup(func() { embeddedReleaseManifestBase64 = previous })

	release, err := loadNodeRelease()
	if err != nil || release != expected {
		t.Fatalf("loadNodeRelease() = %#v/%v", release, err)
	}
}

func testNodeReleaseManifest(t *testing.T, manifestVersion int) ([]byte, cloud.NodeRelease) {
	t.Helper()
	certificateDER, err := os.ReadFile(filepath.Join("..", "..", "..", "windows-signing", "mobile-egress-code-signing.cer"))
	if err != nil {
		t.Fatal(err)
	}
	sha1Digest := sha1.Sum(certificateDER)
	sha256Digest := sha256.Sum256(certificateDER)
	release := cloud.NodeRelease{
		Version:                 "1.2.3",
		URL:                     "https://github.com/cbjjensen/mobile-egress/releases/download/v1.2.3/mobile-egress-client.exe",
		SHA256:                  "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		SignerThumbprint:        hex.EncodeToString(sha1Digest[:]),
		SignerCertificateSHA256: hex.EncodeToString(sha256Digest[:]),
		SignerCertificateBase64: base64.StdEncoding.EncodeToString(certificateDER),
	}
	return marshalNodeReleaseManifest(t, manifestVersion, release), release
}

func marshalNodeReleaseManifest(t *testing.T, manifestVersion int, release cloud.NodeRelease) []byte {
	t.Helper()
	raw, err := json.Marshal(struct {
		Version int               `json:"version"`
		Client  cloud.NodeRelease `json:"client"`
	}{Version: manifestVersion, Client: release})
	if err != nil {
		t.Fatal(err)
	}
	return raw
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

type offlineTailscaleRunner struct{}

func (offlineTailscaleRunner) Run(context.Context, string, ...string) ([]byte, error) {
	return nil, errors.New("offline")
}

type recordingTailscaleInstaller struct{ calls int }

func (installer *recordingTailscaleInstaller) Install(context.Context) (tailscale.Release, error) {
	installer.calls++
	return tailscale.Release{}, nil
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
