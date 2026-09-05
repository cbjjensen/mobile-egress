//go:build windows

package localbridge

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestVerifySignedPairAcceptsSignedPathsWithSpaces(t *testing.T) {
	t.Parallel()

	targetPath := filepath.Join(os.Getenv("ProgramFiles"), "Windows Defender", "MpCmdRun.exe")
	if _, err := os.Stat(targetPath); err != nil {
		t.Fatalf("signed Windows test executable is unavailable: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := verifySignedPair(ctx, targetPath, targetPath); err != nil {
		t.Fatalf("verifySignedPair() rejected matching signed paths with spaces: %v", err)
	}
}

func TestDecodeSetupResultReportsKnownFailureStage(t *testing.T) {
	t.Parallel()

	_, err := decodeSetupResult([]byte(`{"error":"relay-service"}`))
	if err == nil || err.Error() != "elevated relay setup failed at relay-service" {
		t.Fatalf("decodeSetupResult() error = %v", err)
	}
}

func TestDecodeSetupResultRejectsUnknownFailureStage(t *testing.T) {
	t.Parallel()

	_, err := decodeSetupResult([]byte(`{"error":"untrusted detail"}`))
	if err == nil || err.Error() != "elevated relay setup returned invalid public output" {
		t.Fatalf("decodeSetupResult() error = %v", err)
	}
}
