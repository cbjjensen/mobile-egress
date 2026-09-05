//go:build windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestVerifyMobileEgressSignaturePairAcceptsSignedPathsWithSpaces(t *testing.T) {
	t.Parallel()

	targetPath := filepath.Join(os.Getenv("ProgramFiles"), "Windows Defender", "MpCmdRun.exe")
	if _, err := os.Stat(targetPath); err != nil {
		t.Fatalf("signed Windows test executable is unavailable: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := verifyMobileEgressSignaturePair(ctx, targetPath, targetPath); err != nil {
		t.Fatalf("verifyMobileEgressSignaturePair() rejected matching signed paths with spaces: %v", err)
	}
}

func TestRunSetupRelayAllowsExistingRelayStateForOwnerRecovery(t *testing.T) {
	t.Parallel()

	if setupRelayRejectsExistingState {
		t.Fatal("setup-relay still rejects existing relay state before relay bootstrap can recover Owner setup")
	}
}

func TestRunSetupRelayReportsFailureStage(t *testing.T) {
	t.Parallel()

	resultPath := filepath.Join(t.TempDir(), "result.json")
	status := runSetupRelay([]string{
		"--relay-exe", "relay.exe",
		"--public-name", "relay.example.com",
		"--public-url", "https://relay.example.com:8443",
		"--owner-csr-file", "owner.csr",
		"--result-file", resultPath,
	}, &bytes.Buffer{})
	if status != 2 {
		t.Fatalf("runSetupRelay() status = %d, want 2", status)
	}
	raw, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if result.Error != "funnel-endpoint" {
		t.Fatalf("runSetupRelay() error stage = %q, want %q", result.Error, "funnel-endpoint")
	}
}

func TestRecoverIncompleteRelayStateRemovesEmptySetupDirectory(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "Relay")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := recoverIncompleteRelayState(stateDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
		t.Fatalf("recoverIncompleteRelayState() left empty state directory, stat err = %v", err)
	}
}

func TestRecoverIncompleteRelayStatePreservesCompleteState(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "Relay")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ca.crt", "ca.key", "relay.crt", "relay.key", "state.db"} {
		if err := os.WriteFile(filepath.Join(stateDir, name), []byte("present"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := recoverIncompleteRelayState(stateDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stateDir); err != nil {
		t.Fatalf("recoverIncompleteRelayState() removed complete state: %v", err)
	}
}
