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
	setupDigestBytes := sha256.Sum256([]byte("signed setup"))
	setupDigest := hex.EncodeToString(setupDigestBytes[:])
	exchange := Exchange{Root: root}
	if err := exchange.CreateRequest(nonce, setupDigest); err != nil {
		t.Fatal(err)
	}
	request, err := exchange.ConsumeRequest(nonce)
	if err != nil {
		t.Fatal(err)
	}
	if request.Operation != InstallOperation || request.Nonce != nonce || request.SetupSHA256 != setupDigest {
		t.Fatalf("request = %#v", request)
	}
	if _, err := os.Stat(exchange.RequestPath(nonce)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("request was not consumed: %v", err)
	}

	badNonceBytes := sha256.Sum256([]byte("bad request nonce"))
	badNonce := hex.EncodeToString(badNonceBytes[:])
	badPath := exchange.RequestPath(badNonce)
	if err := os.WriteFile(badPath, []byte(`{"operation":"uninstall","nonce":"`+badNonce+`","setupSha256":"`+setupDigest+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := exchange.ConsumeRequest(badNonce); err == nil || !strings.Contains(err.Error(), "operation") {
		t.Fatalf("expected fixed-operation rejection, got %v", err)
	}

	unknownNonceBytes := sha256.Sum256([]byte("unknown request field"))
	unknownNonce := hex.EncodeToString(unknownNonceBytes[:])
	if err := os.WriteFile(exchange.RequestPath(unknownNonce), []byte(`{"operation":"install","nonce":"`+unknownNonce+`","setupSha256":"`+setupDigest+`","destination":"C:\\elsewhere"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := exchange.ConsumeRequest(unknownNonce); err == nil {
		t.Fatal("expected arbitrary destination to be rejected")
	}
}

type parentPlatformFake struct {
	elevated        bool
	confirmed       bool
	setupPath       string
	elevatedExe     string
	launched        string
	exchange        Exchange
	preTrustChecked bool
	elevationErr    error
	forgeResult     bool
	resultDigest    string
	childExitCode   uint32
	resultSuccess   *bool
	resultCode      string
	resultMessage   string
}

func (fake *parentPlatformFake) IsElevated() (bool, error) { return fake.elevated, nil }
func (fake *parentPlatformFake) AcquireSetupLock(string) (ParentSetupLock, error) {
	return &parentSetupLockFake{platform: fake}, nil
}

type parentSetupLockFake struct{ platform *parentPlatformFake }

func (lock *parentSetupLockFake) VerifyPreTrustAuthenticode(Identity) error {
	fake := lock.platform
	fake.preTrustChecked = true
	return nil
}
func (lock *parentSetupLockFake) SHA256() (string, error) {
	return FileSHA256(lock.platform.setupPath)
}
func (lock *parentSetupLockFake) Close() error { return nil }
func (fake *parentPlatformFake) Confirm(_ string) (bool, error) {
	return fake.confirmed, nil
}
func (fake *parentPlatformFake) ElevateAndWait(executable, nonce string) (uint32, error) {
	fake.elevatedExe = executable
	if fake.elevationErr != nil {
		return 0, fake.elevationErr
	}
	request, err := fake.exchange.ConsumeRequest(nonce)
	if err != nil {
		return 0, err
	}
	digest, err := FileSHA256(executable)
	if err != nil {
		return 0, err
	}
	if request.SetupSHA256 != digest {
		return 0, errors.New("parent did not hash the confirmed setup file")
	}
	resultDigest := request.SetupSHA256
	if fake.resultDigest != "" {
		resultDigest = fake.resultDigest
	}
	if fake.forgeResult {
		if err := fake.exchange.WriteResult(Result{Nonce: nonce, SetupSHA256: request.SetupSHA256, Success: false, Code: "install_failed", Message: "failed"}); err != nil {
			return 0, err
		}
		if err := os.Remove(fake.exchange.ResultPath(nonce)); err != nil {
			return 0, err
		}
		if err := fake.exchange.WriteResult(Result{Nonce: nonce, SetupSHA256: request.SetupSHA256, Success: true, Message: "forged"}); err != nil {
			return 0, err
		}
		return fake.childExitCode, nil
	}
	success := true
	if fake.resultSuccess != nil {
		success = *fake.resultSuccess
	}
	message := fake.resultMessage
	if message == "" {
		message = "installed"
	}
	if err := fake.exchange.WriteResult(Result{Nonce: request.Nonce, SetupSHA256: resultDigest, Success: success, Code: fake.resultCode, Message: message}); err != nil {
		return 0, err
	}
	return fake.childExitCode, nil
}

func TestRunParentRejectsBoundSuccessReplacingFailureWhenElevatedChildFails(t *testing.T) {
	executable := filepath.Join(t.TempDir(), SetupExecutableName)
	if err := os.WriteFile(executable, []byte("signed setup"), 0o600); err != nil {
		t.Fatal(err)
	}
	identity, err := LoadIdentity(trackedCertificateDER(t), trackedFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	nonceBytes := sha256.Sum256([]byte("forged child result"))
	exchange := Exchange{Root: t.TempDir()}
	fake := &parentPlatformFake{
		confirmed: true, setupPath: executable, exchange: exchange,
		childExitCode: 17, forgeResult: true,
	}
	err = RunParent(context.Background(), ParentOptions{
		Executable: executable, InstalledController: filepath.Join(InstallRoot, ControllerExecutableName),
		Identity: identity, Nonce: hex.EncodeToString(nonceBytes[:]), Exchange: exchange,
	}, fake)
	if err == nil || fake.launched != "" {
		t.Fatalf("forged result launched controller: err=%v launch=%q", err, fake.launched)
	}
}

func TestRunParentRequiresZeroExitAndBoundSuccessMatrix(t *testing.T) {
	falseValue := false
	trueValue := true
	tests := []struct {
		name          string
		exitCode      uint32
		success       *bool
		code          string
		message       string
		wantErrorText string
		wantMessage   string
		wantLaunch    bool
	}{
		{name: "nonzero valid failure surfaced", exitCode: 17, success: &falseValue, code: "trust_rollback_failed", message: "Installation did not complete and publisher trust cleanup failed.", wantErrorText: "trust_rollback_failed", wantMessage: "publisher trust cleanup failed"},
		{name: "nonzero forged success rejected", exitCode: 17, success: &trueValue, message: "forged success", wantErrorText: "nonzero"},
		{name: "zero failure rejected", success: &falseValue, code: "install_failed", message: "Installation did not complete.", wantErrorText: "install_failed", wantMessage: "Installation did not complete"},
		{name: "zero success launches", success: &trueValue, message: "installed", wantLaunch: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			executable := filepath.Join(t.TempDir(), SetupExecutableName)
			if err := os.WriteFile(executable, []byte("signed setup"), 0o600); err != nil {
				t.Fatal(err)
			}
			identity, err := LoadIdentity(trackedCertificateDER(t), trackedFingerprint)
			if err != nil {
				t.Fatal(err)
			}
			nonceBytes := sha256.Sum256([]byte(test.name))
			exchange := Exchange{Root: t.TempDir()}
			fake := &parentPlatformFake{
				confirmed: true, setupPath: executable, exchange: exchange, childExitCode: test.exitCode,
				resultSuccess: test.success, resultCode: test.code, resultMessage: test.message,
			}
			err = RunParent(context.Background(), ParentOptions{
				Executable: executable, InstalledController: filepath.Join(InstallRoot, ControllerExecutableName),
				Identity: identity, Nonce: hex.EncodeToString(nonceBytes[:]), Exchange: exchange,
			}, fake)
			if test.wantErrorText != "" && (err == nil || !strings.Contains(err.Error(), test.wantErrorText)) {
				t.Fatalf("error = %v, want %q", err, test.wantErrorText)
			}
			if test.wantMessage != "" && !strings.Contains(err.Error(), test.wantMessage) {
				t.Fatalf("error = %v, want surfaced message %q", err, test.wantMessage)
			}
			if test.wantErrorText == "" && err != nil {
				t.Fatal(err)
			}
			if (fake.launched != "") != test.wantLaunch {
				t.Fatalf("launched = %q, want launch %v", fake.launched, test.wantLaunch)
			}
		})
	}
}

func TestRunParentRejectsResultForDifferentSetupDigest(t *testing.T) {
	executable := filepath.Join(t.TempDir(), SetupExecutableName)
	if err := os.WriteFile(executable, []byte("signed setup"), 0o600); err != nil {
		t.Fatal(err)
	}
	identity, err := LoadIdentity(trackedCertificateDER(t), trackedFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	nonceBytes := sha256.Sum256([]byte("wrong result digest"))
	exchange := Exchange{Root: t.TempDir()}
	fake := &parentPlatformFake{
		confirmed: true, setupPath: executable, exchange: exchange,
		resultDigest: strings.Repeat("0", 64),
	}
	err = RunParent(context.Background(), ParentOptions{
		Executable: executable, InstalledController: filepath.Join(InstallRoot, ControllerExecutableName),
		Identity: identity, Nonce: hex.EncodeToString(nonceBytes[:]), Exchange: exchange,
	}, fake)
	if err == nil || !strings.Contains(err.Error(), "does not match") || fake.launched != "" {
		t.Fatalf("wrong-digest result was accepted: err=%v launch=%q", err, fake.launched)
	}
}

type lifecycleSetupLockFake struct {
	platform *lifecycleParentPlatformFake
	digest   string
}

func (lock *lifecycleSetupLockFake) VerifyPreTrustAuthenticode(Identity) error {
	if !lock.platform.lockHeld {
		return errors.New("signature verification ran without setup lock")
	}
	lock.platform.events = append(lock.platform.events, "verify")
	return nil
}

func (lock *lifecycleSetupLockFake) SHA256() (string, error) {
	if !lock.platform.lockHeld {
		return "", errors.New("digest ran without setup lock")
	}
	lock.platform.events = append(lock.platform.events, "digest")
	return lock.digest, nil
}

func (lock *lifecycleSetupLockFake) Close() error {
	lock.platform.events = append(lock.platform.events, "close")
	lock.platform.lockHeld = false
	return nil
}

type lifecycleParentPlatformFake struct {
	exchange Exchange
	digest   string
	lockHeld bool
	events   []string
}

func (fake *lifecycleParentPlatformFake) IsElevated() (bool, error) { return false, nil }
func (fake *lifecycleParentPlatformFake) AcquireSetupLock(string) (ParentSetupLock, error) {
	fake.lockHeld = true
	fake.events = append(fake.events, "lock")
	return &lifecycleSetupLockFake{platform: fake, digest: fake.digest}, nil
}
func (fake *lifecycleParentPlatformFake) Confirm(string) (bool, error) {
	if !fake.lockHeld {
		return false, errors.New("confirmation ran without setup lock")
	}
	fake.events = append(fake.events, "confirm")
	return true, nil
}
func (fake *lifecycleParentPlatformFake) ElevateAndWait(_ string, nonce string) (uint32, error) {
	if !fake.lockHeld {
		return 0, errors.New("elevation started without setup lock")
	}
	fake.events = append(fake.events, "child-start")
	request, err := fake.exchange.ConsumeRequest(nonce)
	if err != nil {
		return 0, err
	}
	if !fake.lockHeld {
		return 0, errors.New("setup lock was released while child ran")
	}
	fake.events = append(fake.events, "child-complete")
	return 0, fake.exchange.WriteResult(Result{Nonce: nonce, SetupSHA256: request.SetupSHA256, Success: true, Message: "installed"})
}
func (fake *lifecycleParentPlatformFake) Launch(string) error {
	fake.events = append(fake.events, "launch")
	return nil
}

func TestRunParentHoldsSetupLockFromPreTrustThroughChildCompletion(t *testing.T) {
	digest := strings.Repeat("d", 64)
	nonce := strings.Repeat("e", 64)
	exchange := Exchange{Root: t.TempDir()}
	fake := &lifecycleParentPlatformFake{exchange: exchange, digest: digest}
	err := RunParent(context.Background(), ParentOptions{
		Executable: filepath.Join(t.TempDir(), SetupExecutableName), InstalledController: filepath.Join(InstallRoot, ControllerExecutableName),
		Identity: Identity{Fingerprint: trackedFingerprint}, Nonce: nonce, Exchange: exchange,
	}, fake)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"lock", "verify", "confirm", "digest", "child-start", "child-complete", "launch", "close"}
	if !reflect.DeepEqual(fake.events, want) {
		t.Fatalf("events = %#v, want %#v", fake.events, want)
	}
}

func (fake *parentPlatformFake) Launch(executable string) error {
	fake.launched = executable
	return nil
}

func TestRunParentRequiresConfirmationAndElevatesOnlyItself(t *testing.T) {
	nonceBytes := sha256.Sum256([]byte("parent request"))
	nonce := hex.EncodeToString(nonceBytes[:])
	exchange := Exchange{Root: t.TempDir()}
	executable := filepath.Join(t.TempDir(), SetupExecutableName)
	if err := os.WriteFile(executable, []byte("initial signed setup"), 0o600); err != nil {
		t.Fatal(err)
	}
	identity, err := LoadIdentity(trackedCertificateDER(t), trackedFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	fake := &parentPlatformFake{exchange: exchange, setupPath: executable}
	options := ParentOptions{
		Executable:          executable,
		InstalledController: `C:\Program Files\MobileEgress\Controller\mobile-egress-windows.exe`,
		Identity:            identity,
		Nonce:               nonce,
		Exchange:            exchange,
	}
	if err := RunParent(context.Background(), options, fake); !errors.Is(err, ErrConfirmationDeclined) {
		t.Fatalf("expected explicit decline, got %v", err)
	}
	if fake.elevatedExe != "" || fake.launched != "" {
		t.Fatal("declined setup performed a privileged or launch action")
	}
	if !fake.preTrustChecked {
		t.Fatal("setup did not inspect its Authenticode signer before confirmation")
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
	elevated    bool
	verified    []string
	installed   []InstallFile
	changes     TrustChanges
	rollback    TrustChanges
	ensureErr   error
	rollbackErr error
	preTrustErr error
	verifyErr   error
}

func (fake *elevatedPlatformFake) IsElevated() (bool, error) { return fake.elevated, nil }
func (fake *elevatedPlatformFake) VerifyPreTrustAuthenticode(path string, _ Identity) error {
	fake.verified = append(fake.verified, "pretrust:"+filepath.Base(path))
	return fake.preTrustErr
}
func (fake *elevatedPlatformFake) EnsureTrust(Identity) (TrustChanges, error) {
	return fake.changes, fake.ensureErr
}
func (fake *elevatedPlatformFake) RollbackTrust(_ Identity, changes TrustChanges) error {
	fake.rollback = changes
	return fake.rollbackErr
}
func (fake *elevatedPlatformFake) VerifyAuthenticode(path string, _ Identity) error {
	fake.verified = append(fake.verified, filepath.Base(path))
	return fake.verifyErr
}
func (fake *elevatedPlatformFake) Install(files []InstallFile, _ Identity) error {
	fake.installed = append([]InstallFile(nil), files...)
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
	setupPath := filepath.Join(releaseDir, SetupExecutableName)
	setupDigest, err := FileSHA256(setupPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := exchange.CreateRequest(nonce, setupDigest); err != nil {
		t.Fatal(err)
	}
	fake := &elevatedPlatformFake{
		elevated:  true,
		changes:   TrustChanges{RootAdded: true, TrustedPublisherAdded: false},
		verifyErr: errors.New("invalid signature"),
	}
	if err := RunElevated(ElevatedOptions{Nonce: nonce, SetupPath: setupPath, Exchange: exchange, Identity: identity}, fake); err == nil {
		t.Fatal("expected verification failure")
	}
	if !reflect.DeepEqual(fake.verified, []string{"pretrust:" + SetupExecutableName, SetupExecutableName}) {
		t.Fatalf("verification order = %#v", fake.verified)
	}
	if !reflect.DeepEqual(fake.rollback, fake.changes) {
		t.Fatalf("rollback = %#v, want %#v", fake.rollback, fake.changes)
	}

	fake.verifyErr = nil
	fake.verified = nil
	fake.rollback = TrustChanges{}
	if err := exchange.CreateRequest(nonce, setupDigest); err != nil {
		t.Fatal(err)
	}
	if err := RunElevated(ElevatedOptions{Nonce: nonce, SetupPath: setupPath, Exchange: exchange, Identity: identity}, fake); err != nil {
		t.Fatal(err)
	}
	wantVerified := append([]string{"pretrust:" + SetupExecutableName}, verifiedReleaseExecutables[:]...)
	if !reflect.DeepEqual(fake.verified, wantVerified) {
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

func TestRunElevatedRejectsSetupPathSwapBeforeTrust(t *testing.T) {
	releaseDir := t.TempDir()
	for _, name := range verifiedReleaseExecutables {
		if err := os.WriteFile(filepath.Join(releaseDir, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	setupPath := filepath.Join(releaseDir, SetupExecutableName)
	setupDigest, err := FileSHA256(setupPath)
	if err != nil {
		t.Fatal(err)
	}
	nonceBytes := sha256.Sum256([]byte("path swap request"))
	nonce := hex.EncodeToString(nonceBytes[:])
	exchange := Exchange{Root: t.TempDir()}
	if err := exchange.CreateRequest(nonce, setupDigest); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(setupPath, []byte("substituted setup"), 0o600); err != nil {
		t.Fatal(err)
	}
	identity, err := LoadIdentity(trackedCertificateDER(t), trackedFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	fake := &elevatedPlatformFake{elevated: true}
	err = RunElevated(ElevatedOptions{Nonce: nonce, SetupPath: setupPath, Exchange: exchange, Identity: identity}, fake)
	if err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("expected setup digest mismatch, got %v", err)
	}
	if fake.changes != (TrustChanges{}) || len(fake.verified) != 0 {
		t.Fatal("path-swapped setup reached signature or trust mutation")
	}
}

func TestRunElevatedRequiresElevationBeforeConsumingRequest(t *testing.T) {
	releaseDir := t.TempDir()
	setupPath := filepath.Join(releaseDir, SetupExecutableName)
	if err := os.WriteFile(setupPath, []byte("signed setup"), 0o600); err != nil {
		t.Fatal(err)
	}
	setupDigest, err := FileSHA256(setupPath)
	if err != nil {
		t.Fatal(err)
	}
	nonceBytes := sha256.Sum256([]byte("unelevated child"))
	nonce := hex.EncodeToString(nonceBytes[:])
	exchange := Exchange{Root: t.TempDir()}
	if err := exchange.CreateRequest(nonce, setupDigest); err != nil {
		t.Fatal(err)
	}
	identity, err := LoadIdentity(trackedCertificateDER(t), trackedFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	err = RunElevated(ElevatedOptions{Nonce: nonce, SetupPath: setupPath, Exchange: exchange, Identity: identity}, &elevatedPlatformFake{})
	if err == nil || !strings.Contains(err.Error(), "requires elevation") {
		t.Fatalf("expected unelevated rejection, got %v", err)
	}
	if _, statErr := os.Stat(exchange.RequestPath(nonce)); statErr != nil {
		t.Fatalf("unelevated invocation consumed request: %v", statErr)
	}
}

func TestRunElevatedRollsBackExactPartialTrustWhenEnsureTrustFails(t *testing.T) {
	options, _ := elevatedTestOptions(t, "partial trust")
	fake := &elevatedPlatformFake{
		elevated:  true,
		changes:   TrustChanges{RootAdded: true},
		ensureErr: errors.New("add TrustedPublisher certificate"),
	}
	err := RunElevated(options, fake)
	if err == nil || !strings.Contains(err.Error(), "install publisher trust") {
		t.Fatalf("expected trust installation failure, got %v", err)
	}
	if !reflect.DeepEqual(fake.rollback, fake.changes) {
		t.Fatalf("rolled back %#v, want exact partial changes %#v", fake.rollback, fake.changes)
	}
}

func TestRunElevatedSurfacesPartialTrustRollbackFailure(t *testing.T) {
	options, _ := elevatedTestOptions(t, "partial trust rollback failure")
	fake := &elevatedPlatformFake{
		elevated:    true,
		changes:     TrustChanges{RootAdded: true},
		ensureErr:   errors.New("add TrustedPublisher certificate"),
		rollbackErr: errors.New("remove exact Root certificate"),
	}
	err := RunElevated(options, fake)
	if !errors.Is(err, ErrTrustRollback) {
		t.Fatalf("expected trust rollback sentinel, got %v", err)
	}
	if !strings.Contains(err.Error(), "remove exact Root certificate") {
		t.Fatalf("rollback error was not surfaced: %v", err)
	}
	if !reflect.DeepEqual(fake.rollback, fake.changes) {
		t.Fatalf("rolled back %#v, want exact partial changes %#v", fake.rollback, fake.changes)
	}
}

func elevatedTestOptions(t *testing.T, nonceSeed string) (ElevatedOptions, Identity) {
	t.Helper()
	releaseDir := t.TempDir()
	for _, name := range verifiedReleaseExecutables {
		if err := os.WriteFile(filepath.Join(releaseDir, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	setupPath := filepath.Join(releaseDir, SetupExecutableName)
	setupDigest, err := FileSHA256(setupPath)
	if err != nil {
		t.Fatal(err)
	}
	nonceBytes := sha256.Sum256([]byte(nonceSeed))
	nonce := hex.EncodeToString(nonceBytes[:])
	exchange := Exchange{Root: t.TempDir()}
	if err := exchange.CreateRequest(nonce, setupDigest); err != nil {
		t.Fatal(err)
	}
	identity, err := LoadIdentity(trackedCertificateDER(t), trackedFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	return ElevatedOptions{Nonce: nonce, SetupPath: setupPath, Exchange: exchange, Identity: identity}, identity
}
