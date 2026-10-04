//go:build windows

package setup

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestClientServiceRepairOnlyAcceptsKnownClientInstallations(t *testing.T) {
	if err := validateExistingClientCommand(`"C:\Program Files\Mobile Egress Client\mobile-egress-client.exe" serve --standalone --state-dir C:\ProgramData\MobileEgressClient`); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{
		`C:\ProgramData\MobileEgress\Client\mobile-egress-client.exe serve`,
		`"C:\Program Files\MobileEgress\Controller\mobile-egress-windows.exe"`,
		`"C:\Program Files\Mobile Egress Client\mobile-egress-client.exe" serve`,
		standaloneClientCommand() + ` --state-dir C:\replacement`,
	} {
		if err := validateExistingClientCommand(command); err == nil {
			t.Fatalf("repair adopted a different existing installation: %q", command)
		}
	}
}

func TestClientServiceMigratesKnownEC2StateInPlace(t *testing.T) {
	for _, command := range []string{
		`"C:\Program Files\MobileEgress\mobile-egress-client.exe" serve --state-dir "C:\ProgramData\MobileEgress\Client"`,
		`"C:\Program Files\Mobile Egress Client\mobile-egress-client.exe" serve --standalone --state-dir "C:\ProgramData\MobileEgress\Client"`,
	} {
		if err := validateExistingClientCommand(command); err != nil {
			t.Fatalf("known local migration rejected: %v", err)
		}
	}
	for _, command := range []string{
		`"C:\Program Files\MobileEgress\mobile-egress-client.exe" serve --state-dir "C:\other"`,
		`"C:\untrusted\mobile-egress-client.exe" serve --state-dir "C:\ProgramData\MobileEgress\Client"`,
		`"C:\Program Files\MobileEgress\mobile-egress-client.exe" serve --state-dir "C:\ProgramData\MobileEgress\Client" --extra`,
	} {
		if err := validateExistingClientCommand(command); err == nil {
			t.Fatal("unrecognized migration accepted")
		}
	}
}

func TestServiceFinalizationFailureRestoresPriorExecutable(t *testing.T) {
	root := t.TempDir()
	sourceDir, targetDir := filepath.Join(root, "source"), filepath.Join(root, "installed")
	for _, dir := range []string{sourceDir, targetDir} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	source, target := filepath.Join(sourceDir, "client.exe"), filepath.Join(targetDir, "client.exe")
	if err := os.WriteFile(source, []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("service registration failed")
	err := installVerifiedFiles([]InstallFile{{Source: source, Destination: target}}, func(string) error { return nil }, installTransactionOps{rename: os.Rename, remove: os.Remove, finalize: func() error { return failure }})
	if !errors.Is(err, failure) {
		t.Fatalf("error = %v", err)
	}
	content, err := os.ReadFile(target)
	if err != nil || string(content) != "old" {
		t.Fatalf("previous executable was not restored: %q, %v", content, err)
	}
}
