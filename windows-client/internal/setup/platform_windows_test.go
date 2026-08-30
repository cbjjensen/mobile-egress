//go:build windows

package setup

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
			ops := installTransactionOps{
				rename: func(oldPath, newPath string) error {
					if strings.Contains(oldPath, ".mobile-egress-staging-") && filepath.Base(newPath) == failedName {
						return errors.New("injected promotion failure")
					}
					return os.Rename(oldPath, newPath)
				},
				remove: os.Remove,
			}
			err := installVerifiedFiles(files, func(string) error { return nil }, ops)
			if err == nil {
				t.Fatal("expected promotion failure")
			}
			assertTransactionTestFiles(t, files, "old-")
		})
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
