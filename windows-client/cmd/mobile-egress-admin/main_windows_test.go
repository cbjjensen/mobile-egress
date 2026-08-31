//go:build windows

package main

import (
	"context"
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
