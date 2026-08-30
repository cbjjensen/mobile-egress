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
