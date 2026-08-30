//go:build windows

package tailscale

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestVerifyAuthenticodePassesExactMSIPathThroughPowerShell(t *testing.T) {
	t.Setenv("MOBILE_EGRESS_TAILSCALE_MSI_PATH", `C:\stale\wrong.msi`)
	t.Setenv("PSModulePath", `C:\incompatible-powershell-modules`)
	signedSystemExecutable := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	signature, err := verifyAuthenticode(signedSystemExecutable)
	if err != nil {
		t.Fatalf("verifyAuthenticode() could not inspect the exact MSI path: %v", err)
	}
	if !signature.Valid || !strings.Contains(signature.Subject, "Microsoft") {
		t.Fatalf("verifyAuthenticode() = %#v, want valid system signature", signature)
	}
}

func TestPowerShellMSIPathHandoffPreservesSpaces(t *testing.T) {
	t.Setenv("MOBILE_EGRESS_TAILSCALE_MSI_PATH", `C:\stale\wrong.msi`)
	want := `C:\Users\Example User\AppData\Local\Temp\tailscale setup.msi`
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	output, err := powershellWithMSIPath(ctx, `[Console]::Out.Write($env:MOBILE_EGRESS_TAILSCALE_MSI_PATH)`, want).Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(output) != want {
		t.Fatalf("PowerShell MSI path = %q, want exact %q", output, want)
	}
}

func TestElevatedMSIFailureIncludesWindowsInstallerExitCode(t *testing.T) {
	err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "exit 1632").Run()
	if err == nil {
		t.Fatal("expected nonzero Windows Installer exit")
	}
	message := elevatedMSIFailure(err).Error()
	if !strings.Contains(message, "1632") {
		t.Fatalf("elevated MSI failure = %q, want exit code 1632", message)
	}
}
