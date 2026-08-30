//go:build windows

package setup

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func testSetupTransactionMutexName(t *testing.T) string {
	t.Helper()
	random := make([]byte, 12)
	if _, err := rand.Read(random); err != nil {
		t.Fatal(err)
	}
	return `Local\MobileEgressSetupTransaction-Test-` + hex.EncodeToString(random)
}

func TestWindowsSetupTransactionRecoversAbandonedOwnership(t *testing.T) {
	name := testSetupTransactionMutexName(t)
	namePointer, err := windows.UTF16PtrFromString(name)
	if err != nil {
		t.Fatal(err)
	}
	anchor, err := windows.CreateMutex(nil, false, namePointer)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(anchor)

	command := exec.Command("powershell", "-NoProfile", "-Command", `$mutex = [Threading.Mutex]::new($false, $env:MOBILE_EGRESS_TEST_MUTEX_NAME)
$null = $mutex.WaitOne()
exit 0`)
	command.Env = append(os.Environ(), "MOBILE_EGRESS_TEST_MUTEX_NAME="+name)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("abandon setup transaction mutex: %v\n%s", err, output)
	}

	transaction, err := acquireWindowsSetupTransaction(name, 500*time.Millisecond)
	if err != nil {
		t.Fatalf("abandoned setup transaction was not recovered: %v", err)
	}
	if err := transaction.Close(); err != nil {
		t.Fatalf("release abandoned setup transaction: %v", err)
	}
}

func TestWindowsSetupTransactionPreventsOverlapUntilRelease(t *testing.T) {
	name := testSetupTransactionMutexName(t)
	ready := make(chan error, 1)
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		transaction, err := acquireWindowsSetupTransaction(name, time.Second)
		ready <- err
		if err != nil {
			return
		}
		<-release
		ready <- transaction.Close()
	}()
	if err := <-ready; err != nil {
		t.Fatal(err)
	}

	if transaction, err := acquireWindowsSetupTransaction(name, 50*time.Millisecond); !errors.Is(err, ErrSetupTransactionTimeout) {
		if err == nil {
			_ = transaction.Close()
		}
		t.Fatalf("overlapping transaction result = %v, want timeout", err)
	}
	close(release)
	if err := <-ready; err != nil {
		t.Fatal(err)
	}
	<-done

	transaction, err := acquireWindowsSetupTransaction(name, time.Second)
	if err != nil {
		t.Fatalf("transaction remained locked after release: %v", err)
	}
	if err := transaction.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestAwaitElevatedProcessRetainsHandleUntilActualCompletion(t *testing.T) {
	completion := make(chan struct{})
	returned := make(chan struct{})
	events := make(chan string, 4)
	var exitCode uint32
	var waitErr error
	go func() {
		exitCode, waitErr = awaitElevatedProcess(windows.Handle(123), elevatedProcessOperations{
			wait: func(handle windows.Handle, milliseconds uint32) (uint32, error) {
				if handle != windows.Handle(123) {
					t.Errorf("wait handle = %v", handle)
				}
				if milliseconds != windows.INFINITE {
					t.Errorf("wait duration = %d, want INFINITE", milliseconds)
				}
				events <- "wait-started"
				<-completion
				events <- "wait-completed"
				return waitObject0, nil
			},
			getExitCode: func(handle windows.Handle, code *uint32) error {
				events <- "exit-code"
				*code = 37
				return nil
			},
			close: func(handle windows.Handle) error {
				events <- "close"
				return nil
			},
		})
		close(returned)
	}()

	if event := <-events; event != "wait-started" {
		t.Fatalf("first event = %q", event)
	}
	select {
	case <-returned:
		t.Fatal("wait returned and released the handle before child completion")
	default:
	}
	close(completion)
	<-returned
	if waitErr != nil || exitCode != 37 {
		t.Fatalf("wait result = %d, %v", exitCode, waitErr)
	}
	for _, want := range []string{"wait-completed", "exit-code", "close"} {
		if event := <-events; event != want {
			t.Fatalf("event = %q, want %q", event, want)
		}
	}
}

func TestValidateAuthenticodeResultRequiresExactTimestampedCertificate(t *testing.T) {
	identity, err := LoadIdentity(trackedCertificateDER(t), trackedFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	certificateHash := sha256.Sum256(identity.DER)
	valid := `{"status":"Valid","thumbprint":"` + identity.Thumbprint + `","certificateSha256":"` + strings.ToUpper(hex.EncodeToString(certificateHash[:])) + `","certificateBase64":"` + base64.StdEncoding.EncodeToString(identity.DER) + `","timestamped":true}`
	if err := validateAuthenticodeResult([]byte(valid), identity); err != nil {
		t.Fatal(err)
	}

	cases := map[string]string{
		"status":      strings.Replace(valid, `"Valid"`, `"NotTrusted"`, 1),
		"thumbprint":  strings.Replace(valid, identity.Thumbprint, strings.Repeat("0", 40), 1),
		"certificate": strings.Replace(valid, base64.StdEncoding.EncodeToString(identity.DER), base64.StdEncoding.EncodeToString([]byte("other")), 1),
		"timestamp":   strings.Replace(valid, `"timestamped":true`, `"timestamped":false`, 1),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if err := validateAuthenticodeResult([]byte(raw), identity); err == nil {
				t.Fatal("expected exact Authenticode identity requirement to fail")
			}
		})
	}
}

func TestPreTrustAuthenticodeAcceptsOnlyValidOrExactUntrustedRoot(t *testing.T) {
	identity, err := LoadIdentity(trackedCertificateDER(t), trackedFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	certificateHash := sha256.Sum256(identity.DER)
	signer := `{"status":"NotTrusted","thumbprint":"` + identity.Thumbprint + `","certificateSha256":"` + strings.ToUpper(hex.EncodeToString(certificateHash[:])) + `","certificateBase64":"` + base64.StdEncoding.EncodeToString(identity.DER) + `","timestamped":true}`
	if err := validatePreTrustAuthenticodeResult([]byte(signer), certEUntrustedRoot, identity); err != nil {
		t.Fatal(err)
	}
	valid := strings.Replace(signer, `"NotTrusted"`, `"Valid"`, 1)
	if err := validatePreTrustAuthenticodeResult([]byte(valid), 0, identity); err != nil {
		t.Fatal(err)
	}
	if err := validatePreTrustAuthenticodeResult([]byte(strings.Replace(signer, `"NotTrusted"`, `"HashMismatch"`, 1)), trustEBadDigest, identity); err == nil {
		t.Fatal("accepted an Authenticode hash mismatch before elevation")
	}
	if err := validatePreTrustAuthenticodeResult([]byte(strings.Replace(signer, `"NotTrusted"`, `"NotSigned"`, 1)), trustENoSignature, identity); err == nil {
		t.Fatal("accepted an unsigned setup before elevation")
	}
	if err := validatePreTrustAuthenticodeResult([]byte(signer), trustESubjectNotTrusted, identity); err == nil {
		t.Fatal("accepted a non-root trust failure before elevation")
	}
	unknown := strings.Replace(signer, `"NotTrusted"`, `"UnknownError"`, 1)
	if err := validatePreTrustAuthenticodeResult([]byte(unknown), certEUntrustedRoot, identity); err == nil {
		t.Fatal("accepted UnknownError before elevation")
	}
}

func TestWindowsSetupLockDeniesWriteDeleteAndPathReplacementUntilClosed(t *testing.T) {
	root := t.TempDir()
	setupPath := filepath.Join(root, SetupExecutableName)
	replacementPath := filepath.Join(root, "replacement.exe")
	if err := os.WriteFile(setupPath, []byte("original signed setup bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(replacementPath, []byte("replacement bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	lock, err := acquireWindowsSetupLock(setupPath)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if _, err := lock.SHA256(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(setupPath, []byte("write while locked"), 0o600); err == nil {
		t.Fatal("setup contents were writable while elevation lock was held")
	}
	if err := os.Remove(setupPath); err == nil {
		t.Fatal("setup was deletable while elevation lock was held")
	}
	if err := os.Rename(setupPath, filepath.Join(root, "displaced.exe")); err == nil {
		t.Fatal("setup path was replaceable while elevation lock was held")
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(setupPath); err != nil {
		t.Fatalf("setup remained locked after child completion: %v", err)
	}
	if err := os.Rename(replacementPath, setupPath); err != nil {
		t.Fatalf("setup replacement should succeed after lock close: %v", err)
	}
}

func TestWindowsSetupLockVerifiesSignedArtifactThroughHeldHandle(t *testing.T) {
	setupPath := os.Getenv("MOBILE_EGRESS_SIGNED_SETUP_TEST_PATH")
	if setupPath == "" {
		t.Skip("set MOBILE_EGRESS_SIGNED_SETUP_TEST_PATH after a signed release build")
	}
	identity, err := LoadIdentity(trackedCertificateDER(t), trackedFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := acquireWindowsSetupLock(setupPath)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := lock.VerifyPreTrustAuthenticode(identity); err != nil {
		t.Fatalf("locked signed artifact verification failed: %v", err)
	}
}

func TestCopyInstallFilesIsIdempotent(t *testing.T) {
	sourceRoot := t.TempDir()
	destinationRoot := t.TempDir()
	files := []InstallFile{
		{Source: filepath.Join(sourceRoot, ControllerExecutableName), Destination: filepath.Join(destinationRoot, ControllerExecutableName)},
		{Source: filepath.Join(sourceRoot, AdminExecutableName), Destination: filepath.Join(destinationRoot, AdminExecutableName)},
		{Source: filepath.Join(sourceRoot, RelayExecutableName), Destination: filepath.Join(destinationRoot, RelayExecutableName)},
	}
	for _, file := range files {
		if err := os.WriteFile(file.Source, []byte(filepath.Base(file.Source)+"-v1"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	verify := func(path string) error {
		relative, err := filepath.Rel(destinationRoot, path)
		if err != nil || relative == "." || strings.HasPrefix(relative, "..") {
			t.Fatalf("verified outside protected destination: %s", path)
		}
		return nil
	}
	if err := installVerifiedFiles(files, verify); err != nil {
		t.Fatal(err)
	}
	if err := installVerifiedFiles(files, verify); err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		got, err := os.ReadFile(file.Destination)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != filepath.Base(file.Source)+"-v1" {
			t.Fatalf("%s = %q", file.Destination, got)
		}
	}
}

func TestInstallVerifiedFilesLeavesExistingFilesWhenStagedVerificationFails(t *testing.T) {
	sourceRoot := t.TempDir()
	destinationRoot := t.TempDir()
	files := []InstallFile{
		{Source: filepath.Join(sourceRoot, ControllerExecutableName), Destination: filepath.Join(destinationRoot, ControllerExecutableName)},
		{Source: filepath.Join(sourceRoot, AdminExecutableName), Destination: filepath.Join(destinationRoot, AdminExecutableName)},
	}
	for _, file := range files {
		if err := os.WriteFile(file.Source, []byte("new signed bytes"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file.Destination, []byte("existing signed bytes"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	verificationCalls := 0
	err := installVerifiedFiles(files, func(path string) error {
		verificationCalls++
		if filepath.Base(path) != filepath.Base(files[verificationCalls-1].Destination) {
			t.Fatalf("staged name = %s", filepath.Base(path))
		}
		if verificationCalls == 2 {
			return errors.New("staged signature invalid")
		}
		return nil
	})
	if err == nil {
		t.Fatal("expected staged verification failure")
	}
	for _, file := range files {
		got, readErr := os.ReadFile(file.Destination)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if string(got) != "existing signed bytes" {
			t.Fatalf("existing destination was replaced after failed verification: %s", file.Destination)
		}
	}
}

func TestInstallVerifiedFilesRollsBackEveryFileWhenPromotionFails(t *testing.T) {
	for _, failedName := range []string{AdminExecutableName, RelayExecutableName} {
		t.Run(failedName, func(t *testing.T) {
			sourceRoot := t.TempDir()
			destinationRoot := t.TempDir()
			files := transactionTestFiles(t, sourceRoot, destinationRoot)
			backupRoot := ""
			ops := installTransactionOps{
				rename: func(oldPath, newPath string) error {
					if strings.Contains(oldPath, ".mobile-egress-staging-") && filepath.Base(newPath) == failedName {
						return errors.New("injected promotion failure")
					}
					return os.Rename(oldPath, newPath)
				},
				remove: os.Remove,
				protectRecovery: func(path string) error {
					backupRoot = path
					return nil
				},
			}
			err := installVerifiedFiles(files, func(string) error { return nil }, ops)
			if err == nil {
				t.Fatal("expected promotion failure")
			}
			assertTransactionTestFiles(t, files, "old-")
			if _, statErr := os.Stat(backupRoot); !os.IsNotExist(statErr) {
				t.Fatalf("successful rollback left backup state at %q: %v", backupRoot, statErr)
			}
		})
	}
}

func TestInstallRollbackFailurePreservesRestrictedRecoveryBackup(t *testing.T) {
	sourceRoot := t.TempDir()
	destinationRoot := t.TempDir()
	files := transactionTestFiles(t, sourceRoot, destinationRoot)
	backupRoot := ""
	protected := false
	ops := installTransactionOps{
		rename: func(oldPath, newPath string) error {
			if strings.Contains(oldPath, ".mobile-egress-staging-") && filepath.Base(newPath) == AdminExecutableName {
				return errors.New("injected promotion failure")
			}
			if backupRoot != "" && oldPath == filepath.Join(backupRoot, ControllerExecutableName) && newPath == files[0].Destination {
				return errors.New("injected restore failure")
			}
			if strings.Contains(newPath, ".mobile-egress-backup-") && !protected {
				t.Fatal("existing file was moved before the recovery backup was restricted")
			}
			return os.Rename(oldPath, newPath)
		},
		remove: os.Remove,
		protectRecovery: func(path string) error {
			backupRoot = path
			protected = true
			return nil
		},
	}
	err := installVerifiedFiles(files, func(string) error { return nil }, ops)
	if !errors.Is(err, ErrInstallRollback) {
		t.Fatalf("install rollback error = %v", err)
	}
	if backupRoot == "" || !protected {
		t.Fatal("recovery backup was not restricted")
	}
	recovered, readErr := os.ReadFile(filepath.Join(backupRoot, ControllerExecutableName))
	if readErr != nil {
		t.Fatalf("preserved recovery backup is unavailable: %v", readErr)
	}
	if string(recovered) != "old-"+ControllerExecutableName {
		t.Fatalf("preserved recovery backup = %q", recovered)
	}
}

func TestInstallVerifiedFilesKeepsPromotedReleaseWhenCommittedBackupCleanupPartiallyFails(t *testing.T) {
	sourceRoot := t.TempDir()
	destinationRoot := t.TempDir()
	files := transactionTestFiles(t, sourceRoot, destinationRoot)
	backupRoot := ""
	cleanupCalls := 0
	ops := installTransactionOps{
		rename: os.Rename,
		remove: os.Remove,
		removeAll: func(path string) error {
			cleanupCalls++
			if path != backupRoot {
				t.Fatalf("cleanup path = %q, want %q", path, backupRoot)
			}
			if err := os.Remove(filepath.Join(path, ControllerExecutableName)); err != nil {
				t.Fatal(err)
			}
			return errors.New("injected partial committed-backup cleanup failure")
		},
		protectRecovery: func(path string) error {
			backupRoot = path
			return nil
		},
	}
	if err := installVerifiedFiles(files, func(string) error { return nil }, ops); err != nil {
		t.Fatalf("committed install failed because backup cleanup was partial: %v", err)
	}
	if cleanupCalls != 1 {
		t.Fatalf("backup cleanup calls = %d, want 1", cleanupCalls)
	}
	assertTransactionTestFiles(t, files, "new-")
	if _, err := os.Stat(filepath.Join(backupRoot, AdminExecutableName)); err != nil {
		t.Fatalf("test did not leave an incomplete rollback set: %v", err)
	}
}

func TestRestrictRecoveryDirectoryUsesOnlySystemAndAdministratorsACL(t *testing.T) {
	icaclsPath := `C:\Windows\System32\icacls.exe`
	recoveryPath := `C:\Program Files\MobileEgress\Controller\.mobile-egress-backup-test`
	called := false
	err := restrictRecoveryDirectoryWith(icaclsPath, recoveryPath, func(executable string, arguments ...string) error {
		called = true
		if executable != icaclsPath {
			t.Fatalf("executable = %q", executable)
		}
		want := []string{
			recoveryPath,
			"/inheritance:r",
			"/grant:r",
			"*S-1-5-18:(OI)(CI)F",
			"*S-1-5-32-544:(OI)(CI)F",
		}
		if !reflect.DeepEqual(arguments, want) {
			t.Fatalf("ACL arguments = %#v, want %#v", arguments, want)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("ACL command was not invoked")
	}
}

func TestInstallVerifiedFilesRollsBackFilesAndShortcutWhenShortcutFails(t *testing.T) {
	sourceRoot := t.TempDir()
	destinationRoot := t.TempDir()
	files := transactionTestFiles(t, sourceRoot, destinationRoot)
	shortcutPath := filepath.Join(t.TempDir(), "Mobile Egress.lnk")
	if err := os.WriteFile(shortcutPath, []byte("old-shortcut"), 0o600); err != nil {
		t.Fatal(err)
	}
	ops := installTransactionOps{
		rename:         os.Rename,
		remove:         os.Remove,
		shortcutPath:   shortcutPath,
		controllerPath: files[0].Destination,
		createShortcut: func(string) error {
			if err := os.WriteFile(shortcutPath, []byte("partial-new-shortcut"), 0o600); err != nil {
				t.Fatal(err)
			}
			return errors.New("injected shortcut failure")
		},
	}
	if err := installVerifiedFiles(files, func(string) error { return nil }, ops); err == nil {
		t.Fatal("expected shortcut failure")
	}
	assertTransactionTestFiles(t, files, "old-")
	got, err := os.ReadFile(shortcutPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "old-shortcut" {
		t.Fatalf("shortcut = %q, want old-shortcut", got)
	}
}

func transactionTestFiles(t *testing.T, sourceRoot, destinationRoot string) []InstallFile {
	t.Helper()
	files := []InstallFile{
		{Source: filepath.Join(sourceRoot, ControllerExecutableName), Destination: filepath.Join(destinationRoot, ControllerExecutableName)},
		{Source: filepath.Join(sourceRoot, AdminExecutableName), Destination: filepath.Join(destinationRoot, AdminExecutableName)},
		{Source: filepath.Join(sourceRoot, RelayExecutableName), Destination: filepath.Join(destinationRoot, RelayExecutableName)},
	}
	for _, file := range files {
		name := filepath.Base(file.Source)
		if err := os.WriteFile(file.Source, []byte("new-"+name), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file.Destination, []byte("old-"+name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return files
}

func assertTransactionTestFiles(t *testing.T, files []InstallFile, prefix string) {
	t.Helper()
	for _, file := range files {
		got, err := os.ReadFile(file.Destination)
		if err != nil {
			t.Fatal(err)
		}
		want := prefix + filepath.Base(file.Destination)
		if string(got) != want {
			t.Fatalf("%s = %q, want %q", file.Destination, got, want)
		}
	}
}
