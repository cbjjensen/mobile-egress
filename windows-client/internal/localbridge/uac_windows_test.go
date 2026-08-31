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
