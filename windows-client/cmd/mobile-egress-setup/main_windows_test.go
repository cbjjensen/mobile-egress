//go:build windows

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mobile-egress/windows-client/internal/setup"
)

func TestParseModeAcceptsOnlyFixedInternalOperationAndNonce(t *testing.T) {
	if _, _, err := parseMode(nil); err == nil || !strings.Contains(err.Error(), "trusted Windows PowerShell") {
		t.Fatalf("direct launch was not rejected with safe verifier instructions: %v", err)
	}
	verifiedDigest := strings.Repeat("1", 64)
	mode, value, err := parseMode([]string{"--verified-setup-sha256", verifiedDigest})
	if err != nil || mode != parentMode || value != verifiedDigest {
		t.Fatalf("verified parent parse = %q, %q, %v", mode, value, err)
	}
	validNonce := strings.Repeat("a", 64)
	mode, value, err = parseMode([]string{"--internal-elevated-install", validNonce})
	if err != nil || mode != elevatedInstallMode || value != validNonce {
		t.Fatalf("child parse = %q, %q, %v", mode, value, err)
	}

	for _, arguments := range [][]string{
		{"--internal-elevated-install", validNonce, "--destination", `C:\elsewhere`},
		{"--internal-elevated-uninstall", validNonce},
		{"--internal-elevated-install", "short"},
		{"--verified-setup-sha256", strings.Repeat("A", 64)},
		{"--verified-setup-sha256", "short"},
	} {
		if _, _, err := parseMode(arguments); err == nil {
			t.Fatalf("accepted arguments %#v", arguments)
		}
	}
}

func TestFailureResultIsRedacted(t *testing.T) {
	result := failureResult(strings.Repeat("b", 64), strings.Repeat("a", 64), errors.New(`copy C:\Users\Friend\secret: access denied`))
	if result.Success || result.Code != "install_failed" || result.Message != "Installation did not complete. Verify the Mobile Egress publisher trust before retrying." {
		t.Fatalf("result = %#v", result)
	}
	if strings.Contains(result.Message, "Friend") || strings.Contains(result.Message, "access denied") || strings.Contains(result.Message, "left behind") {
		t.Fatal("result exposed internal error details")
	}
	if result.Nonce != strings.Repeat("b", 64) {
		t.Fatal("result nonce changed")
	}
}

func TestFailureResultDistinguishesTrustRollbackFailureWithoutLeakingDetails(t *testing.T) {
	result := failureResult(strings.Repeat("c", 64), strings.Repeat("a", 64), errors.Join(setup.ErrTrustRollback, errors.New(`remove C:\secret\publisher.cer`)))
	if result.Code != "trust_rollback_failed" {
		t.Fatalf("result code = %q", result.Code)
	}
	if strings.Contains(result.Message, "secret") || strings.Contains(result.Message, "publisher.cer") {
		t.Fatalf("result leaked internal detail: %q", result.Message)
	}
}

func TestCompleteElevatedRunWritesBoundFailureAndReturnsNonzero(t *testing.T) {
	executable := filepath.Join(t.TempDir(), setup.SetupExecutableName)
	if err := os.WriteFile(executable, []byte("signed setup bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	nonce := strings.Repeat("d", 64)
	exchange := setup.Exchange{Root: t.TempDir()}
	installErr := errors.New("injected install failure")
	err := completeElevatedRun(nonce, executable, exchange, func() error { return installErr })
	if !errors.Is(err, installErr) {
		t.Fatalf("installation failure did not produce nonzero child outcome: %v", err)
	}
	result, err := exchange.ReadResult(nonce)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := setup.FileSHA256(executable)
	if err != nil {
		t.Fatal(err)
	}
	if result.Success || result.SetupSHA256 != digest || result.Code != "install_failed" {
		t.Fatalf("failure result = %#v", result)
	}
}

func TestCompleteElevatedRunSuccessWritesBoundSuccessAndReturnsZero(t *testing.T) {
	executable := filepath.Join(t.TempDir(), setup.SetupExecutableName)
	if err := os.WriteFile(executable, []byte("signed setup bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	nonce := strings.Repeat("e", 64)
	exchange := setup.Exchange{Root: t.TempDir()}
	if err := completeElevatedRun(nonce, executable, exchange, func() error { return nil }); err != nil {
		t.Fatalf("successful install returned nonzero outcome: %v", err)
	}
	result, err := exchange.ReadResult(nonce)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success || result.SetupSHA256 == "" {
		t.Fatalf("success result = %#v", result)
	}
}

type commandFlowSetupLock struct{ setupPath string }

func (lock *commandFlowSetupLock) VerifyPreTrustAuthenticode(setup.Identity) error { return nil }
func (lock *commandFlowSetupLock) SHA256() (string, error) {
	return setup.FileSHA256(lock.setupPath)
}
func (lock *commandFlowSetupLock) Close() error { return nil }

type commandFlowParentPlatform struct {
	setupPath string
	exchange  setup.Exchange
	launched  bool
	t         *testing.T
}

func (fake *commandFlowParentPlatform) IsElevated() (bool, error) { return false, nil }
func (fake *commandFlowParentPlatform) AcquireSetupLock(string) (setup.ParentSetupLock, error) {
	return &commandFlowSetupLock{setupPath: fake.setupPath}, nil
}
func (fake *commandFlowParentPlatform) Confirm(string) (bool, error) { return true, nil }
func (fake *commandFlowParentPlatform) ElevateAndWait(_ string, nonce string) (uint32, error) {
	installErr := errors.New("injected install failure")
	childErr := completeElevatedRun(nonce, fake.setupPath, fake.exchange, func() error { return installErr })
	if !errors.Is(childErr, installErr) {
		fake.t.Fatalf("child outcome = %v", childErr)
	}
	failure, err := fake.exchange.ReadResult(nonce)
	if err != nil {
		fake.t.Fatal(err)
	}
	if failure.Success || failure.SetupSHA256 == "" {
		fake.t.Fatalf("failure result = %#v", failure)
	}
	if err := fake.exchange.WriteResult(setup.Result{
		Nonce: nonce, SetupSHA256: failure.SetupSHA256, Success: true, Message: "substituted bound success",
	}); err != nil {
		fake.t.Fatal(err)
	}
	return 1, nil
}
func (fake *commandFlowParentPlatform) Launch(string) error {
	fake.launched = true
	return nil
}

func TestParentCannotLaunchWhenBoundSuccessReplacesFailureAfterNonzeroChild(t *testing.T) {
	executable := filepath.Join(t.TempDir(), setup.SetupExecutableName)
	if err := os.WriteFile(executable, []byte("signed setup bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	exchange := setup.Exchange{Root: t.TempDir()}
	fake := &commandFlowParentPlatform{setupPath: executable, exchange: exchange, t: t}
	err := setup.RunParent(context.Background(), setup.ParentOptions{
		Executable: executable, InstalledController: filepath.Join(setup.InstallRoot, setup.ControllerExecutableName),
		Identity: setup.Identity{Fingerprint: strings.Repeat("F", 95)}, VerifiedSetupSHA256: mustSetupDigest(t, executable), Nonce: strings.Repeat("f", 64), Exchange: exchange,
	}, fake)
	if err == nil || fake.launched {
		t.Fatalf("nonzero child authorized launch: err=%v launched=%v", err, fake.launched)
	}
}

func mustSetupDigest(t *testing.T, path string) string {
	t.Helper()
	digest, err := setup.FileSHA256(path)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}
