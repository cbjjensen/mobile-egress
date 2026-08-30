package setup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const trackedFingerprint = "9F:E2:14:C3:50:D7:CE:04:C8:EE:7F:71:E1:69:28:1B:50:FF:0B:2A:7C:56:69:A3:48:AC:10:61:6F:B7:06:1F"

func trackedCertificateDER(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "windows-signing", PublicCertificateName))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestLoadIdentityRequiresEmbeddedCertificateFingerprintMatch(t *testing.T) {
	identity, err := LoadIdentity(trackedCertificateDER(t), trackedFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if identity.Fingerprint != trackedFingerprint {
		t.Fatalf("fingerprint = %q", identity.Fingerprint)
	}
	if identity.Thumbprint != "85F220C1BF05A5D3A86B5DD408787EC1B122ECB7" {
		t.Fatalf("thumbprint = %q", identity.Thumbprint)
	}

	_, err = LoadIdentity(trackedCertificateDER(t), strings.Replace(trackedFingerprint, "9F", "8F", 1))
	if err == nil || !strings.Contains(err.Error(), "fingerprint") {
		t.Fatalf("expected fingerprint mismatch, got %v", err)
	}
}

func TestExchangeConsumesOnlyNonceBoundFixedInstallRequest(t *testing.T) {
	root := t.TempDir()
	nonceBytes := sha256.Sum256([]byte("request nonce"))
	nonce := hex.EncodeToString(nonceBytes[:])
	exchange := Exchange{Root: root}
	if err := exchange.CreateRequest(nonce); err != nil {
		t.Fatal(err)
	}
	request, err := exchange.ConsumeRequest(nonce)
	if err != nil {
		t.Fatal(err)
	}
	if request.Operation != InstallOperation || request.Nonce != nonce {
		t.Fatalf("request = %#v", request)
	}
	if _, err := os.Stat(exchange.RequestPath(nonce)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("request was not consumed: %v", err)
	}

	badNonceBytes := sha256.Sum256([]byte("bad request nonce"))
	badNonce := hex.EncodeToString(badNonceBytes[:])
	badPath := exchange.RequestPath(badNonce)
	if err := os.WriteFile(badPath, []byte(`{"operation":"uninstall","nonce":"`+badNonce+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := exchange.ConsumeRequest(badNonce); err == nil || !strings.Contains(err.Error(), "operation") {
		t.Fatalf("expected fixed-operation rejection, got %v", err)
	}

	unknownNonceBytes := sha256.Sum256([]byte("unknown request field"))
	unknownNonce := hex.EncodeToString(unknownNonceBytes[:])
	if err := os.WriteFile(exchange.RequestPath(unknownNonce), []byte(`{"operation":"install","nonce":"`+unknownNonce+`","destination":"C:\\elsewhere"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := exchange.ConsumeRequest(unknownNonce); err == nil {
		t.Fatal("expected arbitrary destination to be rejected")
	}
}

type parentPlatformFake struct {
	elevated    bool
	confirmed   bool
	elevatedExe string
	launched    string
	exchange    Exchange
}

func (fake *parentPlatformFake) IsElevated() (bool, error)    { return fake.elevated, nil }
func (fake *parentPlatformFake) Confirm(string) (bool, error) { return fake.confirmed, nil }
func (fake *parentPlatformFake) ElevateAndWait(executable, nonce string) error {
	fake.elevatedExe = executable
	request, err := fake.exchange.ConsumeRequest(nonce)
	if err != nil {
		return err
	}
	return fake.exchange.WriteResult(Result{Nonce: request.Nonce, Success: true, Message: "installed"})
}
func (fake *parentPlatformFake) Launch(executable string) error {
	fake.launched = executable
	return nil
}

func TestRunParentRequiresConfirmationAndElevatesOnlyItself(t *testing.T) {
	nonceBytes := sha256.Sum256([]byte("parent request"))
	nonce := hex.EncodeToString(nonceBytes[:])
	exchange := Exchange{Root: t.TempDir()}
	fake := &parentPlatformFake{exchange: exchange}
	options := ParentOptions{
		Executable:          `C:\release\MobileEgressSetup.exe`,
		InstalledController: `C:\Program Files\MobileEgress\Controller\mobile-egress-windows.exe`,
		Fingerprint:         trackedFingerprint,
		Nonce:               nonce,
		Exchange:            exchange,
	}
	if err := RunParent(context.Background(), options, fake); !errors.Is(err, ErrConfirmationDeclined) {
		t.Fatalf("expected explicit decline, got %v", err)
	}
	if fake.elevatedExe != "" || fake.launched != "" {
		t.Fatal("declined setup performed a privileged or launch action")
	}

	fake.confirmed = true
	if err := RunParent(context.Background(), options, fake); err != nil {
		t.Fatal(err)
	}
	if fake.elevatedExe != options.Executable {
		t.Fatalf("elevated %q instead of setup %q", fake.elevatedExe, options.Executable)
	}
	if fake.launched != options.InstalledController {
		t.Fatalf("launched %q", fake.launched)
	}
}

type elevatedPlatformFake struct {
	elevated  bool
	verified  []string
	installed []InstallFile
	shortcut  string
	changes   TrustChanges
	rollback  TrustChanges
	verifyErr error
}

func (fake *elevatedPlatformFake) IsElevated() (bool, error) { return fake.elevated, nil }
func (fake *elevatedPlatformFake) EnsureTrust(Identity) (TrustChanges, error) {
	return fake.changes, nil
}
func (fake *elevatedPlatformFake) RollbackTrust(_ Identity, changes TrustChanges) error {
	fake.rollback = changes
	return nil
}
func (fake *elevatedPlatformFake) VerifyAuthenticode(path string, _ Identity) error {
	fake.verified = append(fake.verified, filepath.Base(path))
	return fake.verifyErr
}
func (fake *elevatedPlatformFake) Install(files []InstallFile, _ Identity) error {
	fake.installed = append([]InstallFile(nil), files...)
	return nil
}
func (fake *elevatedPlatformFake) CreateShortcut(controllerPath string) error {
	fake.shortcut = controllerPath
	return nil
}

func TestRunElevatedUsesFixedSiblingsAndRollsBackOnlyNewTrust(t *testing.T) {
	releaseDir := t.TempDir()
	for _, name := range verifiedReleaseExecutables {
		if err := os.WriteFile(filepath.Join(releaseDir, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	identity, err := LoadIdentity(trackedCertificateDER(t), trackedFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	nonceBytes := sha256.Sum256([]byte("elevated request"))
	nonce := hex.EncodeToString(nonceBytes[:])
	exchange := Exchange{Root: root}
	if err := exchange.CreateRequest(nonce); err != nil {
		t.Fatal(err)
	}
	fake := &elevatedPlatformFake{
		elevated:  true,
		changes:   TrustChanges{RootAdded: true, TrustedPublisherAdded: false},
		verifyErr: errors.New("invalid signature"),
	}
	if err := RunElevated(ElevatedOptions{Nonce: nonce, ReleaseDir: releaseDir, Exchange: exchange, Identity: identity}, fake); err == nil {
		t.Fatal("expected verification failure")
	}
	if !reflect.DeepEqual(fake.verified, []string{SetupExecutableName}) {
		t.Fatalf("verification order = %#v", fake.verified)
	}
	if !reflect.DeepEqual(fake.rollback, fake.changes) {
		t.Fatalf("rollback = %#v, want %#v", fake.rollback, fake.changes)
	}

	fake.verifyErr = nil
	fake.verified = nil
	fake.rollback = TrustChanges{}
	if err := exchange.CreateRequest(nonce); err != nil {
		t.Fatal(err)
	}
	if err := RunElevated(ElevatedOptions{Nonce: nonce, ReleaseDir: releaseDir, Exchange: exchange, Identity: identity}, fake); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fake.verified, verifiedReleaseExecutables[:]) {
		t.Fatalf("verified = %#v", fake.verified)
	}
	wantInstalled := []string{ControllerExecutableName, AdminExecutableName, RelayExecutableName}
	var gotInstalled []string
	for _, file := range fake.installed {
		gotInstalled = append(gotInstalled, filepath.Base(file.Source))
	}
	if !reflect.DeepEqual(gotInstalled, wantInstalled) {
		t.Fatalf("installed = %#v", gotInstalled)
	}
	if fake.rollback != (TrustChanges{}) {
		t.Fatalf("successful install rolled back trust: %#v", fake.rollback)
	}
}
