//go:build windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"mobile-egress/pairing"
	"mobile-egress/windows-client/internal/localbridge"
)

const (
	installedRelayPath = `C:\Program Files\MobileEgress\mobile-egress-relay.exe`
	relayStatePath     = `C:\ProgramData\MobileEgress\Relay`
)

const setupRelayRejectsExistingState = false

var version = "dev"

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(arguments []string, stdout, stderr io.Writer) int {
	if len(arguments) == 1 && (arguments[0] == "--version" || arguments[0] == "version") {
		fmt.Fprintln(stdout, version)
		return 0
	}
	if len(arguments) == 0 {
		fmt.Fprintln(stderr, "usage: mobile-egress-admin <setup-relay|rotate-relay|repair-relay|--version> [flags]")
		return 2
	}
	switch arguments[0] {
	case "setup-relay":
		return runSetupRelay(arguments[1:], stderr)
	case "rotate-relay":
		return runRotateRelay(arguments[1:], stderr)
	case "repair-relay":
		return runRepairRelay(arguments[1:], stderr)
	default:
		fmt.Fprintln(stderr, "usage: mobile-egress-admin <setup-relay|rotate-relay|repair-relay|--version> [flags]")
		return 2
	}
}

func runRepairRelay(arguments []string, stderr io.Writer) (status int) {
	flags := flag.NewFlagSet("mobile-egress-admin repair-relay", flag.ContinueOnError)
	flags.SetOutput(stderr)
	relayExecutable := flags.String("relay-exe", "", "signed relay executable")
	resultFile := flags.String("result-file", "", "public repair result path")
	if err := flags.Parse(arguments); err != nil {
		return 2
	}
	if flags.NArg() != 0 || *relayExecutable == "" || *resultFile == "" {
		fmt.Fprintln(stderr, "mobile-egress-admin repair-relay: required input is missing")
		return 2
	}
	defer func() {
		if status != 0 {
			_ = writeResult(*resultFile, map[string]string{"error": "repair_failed"})
		}
	}()
	if info, err := os.Stat(relayStatePath); err != nil || !info.IsDir() {
		fmt.Fprintln(stderr, "mobile-egress-admin repair-relay: initialized relay state is unavailable")
		return 1
	}
	if err := verifyMobileEgressSignature(*relayExecutable); err != nil {
		fmt.Fprintln(stderr, "mobile-egress-admin repair-relay: relay signature verification failed")
		return 1
	}
	if err := stopRelayService(); err != nil {
		fmt.Fprintln(stderr, "mobile-egress-admin repair-relay: stop relay service failed")
		return 1
	}
	if err := copyFile(*relayExecutable, installedRelayPath); err != nil {
		fmt.Fprintln(stderr, "mobile-egress-admin repair-relay: install relay executable failed")
		return 1
	}
	if err := protectRelayState(); err != nil {
		fmt.Fprintln(stderr, "mobile-egress-admin repair-relay: protect relay state failed")
		return 1
	}
	if err := installRelayService(); err != nil {
		fmt.Fprintln(stderr, "mobile-egress-admin repair-relay: install relay service failed")
		return 1
	}
	if err := writeResult(*resultFile, map[string]bool{"ready": true}); err != nil {
		fmt.Fprintln(stderr, "mobile-egress-admin repair-relay: write public result failed")
		return 1
	}
	return 0
}

func runRotateRelay(arguments []string, stderr io.Writer) (status int) {
	flags := flag.NewFlagSet("mobile-egress-admin rotate-relay", flag.ContinueOnError)
	flags.SetOutput(stderr)
	publicName := flags.String("public-name", "", "Tailscale Funnel DNS name")
	publicURL := flags.String("public-url", "", "Tailscale Funnel HTTPS origin")
	resultFile := flags.String("result-file", "", "public rotation result path")
	if err := flags.Parse(arguments); err != nil {
		return 2
	}
	if flags.NArg() != 0 || *publicName == "" || *publicURL == "" || *resultFile == "" {
		fmt.Fprintln(stderr, "mobile-egress-admin rotate-relay: required input is missing")
		return 2
	}
	defer func() {
		if status != 0 {
			_ = writeResult(*resultFile, map[string]string{"error": "rotation_failed"})
		}
	}()
	origin, err := pairing.RelayOrigin(*publicURL)
	if err != nil || origin.Hostname() != *publicName || !strings.HasSuffix(strings.ToLower(*publicName), ".ts.net") {
		fmt.Fprintln(stderr, "mobile-egress-admin rotate-relay: invalid Funnel endpoint")
		return 2
	}
	if info, err := os.Stat(relayStatePath); err != nil || !info.IsDir() {
		fmt.Fprintln(stderr, "mobile-egress-admin rotate-relay: initialized relay state is unavailable")
		return 1
	}
	output, err := exec.Command(installedRelayPath,
		"rotate-endpoint", "--state-dir", relayStatePath, "--public-name", *publicName, "--public-url", origin.String(),
	).Output()
	if err != nil {
		fmt.Fprintln(stderr, "mobile-egress-admin rotate-relay: relay rotation failed")
		return 1
	}
	var result localbridge.EndpointRotationResult
	decoder := json.NewDecoder(bytes.NewReader(output))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&result) != nil || result.PublicURL != origin.String() || result.Serial == "" {
		fmt.Fprintln(stderr, "mobile-egress-admin rotate-relay: relay returned invalid public output")
		return 1
	}
	if err := restartRelayService(); err != nil {
		fmt.Fprintln(stderr, "mobile-egress-admin rotate-relay: relay restart failed")
		return 1
	}
	if err := writeResult(*resultFile, result); err != nil {
		fmt.Fprintln(stderr, "mobile-egress-admin rotate-relay: write public result failed")
		return 1
	}
	return 0
}

func restartRelayService() error {
	const script = `$service = Get-Service -Name 'MobileEgressRelay' -ErrorAction Stop
if ($service.Status -ne 'Stopped') {
  Stop-Service -Name 'MobileEgressRelay' -Force -ErrorAction Stop
  $service.WaitForStatus('Stopped', [TimeSpan]::FromSeconds(30))
}
Start-Service -Name 'MobileEgressRelay' -ErrorAction Stop
(Get-Service -Name 'MobileEgressRelay').WaitForStatus('Running', [TimeSpan]::FromSeconds(30))`
	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script).Run()
}

func stopRelayService() error {
	const script = `$service = Get-Service -Name 'MobileEgressRelay' -ErrorAction SilentlyContinue
if ($null -ne $service -and $service.Status -ne 'Stopped') {
  Stop-Service -Name 'MobileEgressRelay' -Force -ErrorAction Stop
  $service.WaitForStatus('Stopped', [TimeSpan]::FromSeconds(30))
}`
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script).Run()
}

func runSetupRelay(arguments []string, stderr io.Writer) (status int) {
	flags := flag.NewFlagSet("mobile-egress-admin setup-relay", flag.ContinueOnError)
	flags.SetOutput(stderr)
	relayExecutable := flags.String("relay-exe", "", "signed relay executable")
	publicName := flags.String("public-name", "", "Tailscale Funnel DNS name")
	publicURL := flags.String("public-url", "", "Tailscale Funnel HTTPS origin")
	ownerCSRFile := flags.String("owner-csr-file", "", "Owner CSR path")
	resultFile := flags.String("result-file", "", "public Owner result path")
	if err := flags.Parse(arguments); err != nil {
		return 2
	}
	if flags.NArg() != 0 || *relayExecutable == "" || *publicName == "" || *publicURL == "" || *ownerCSRFile == "" || *resultFile == "" {
		fmt.Fprintln(stderr, "mobile-egress-admin setup-relay: required input is missing")
		return 2
	}
	defer func() {
		if status != 0 {
			_ = writeResult(*resultFile, map[string]string{"error": "setup_failed"})
		}
	}()
	origin, err := pairing.RelayOrigin(*publicURL)
	if err != nil || origin.Hostname() != *publicName || !strings.HasSuffix(strings.ToLower(*publicName), ".ts.net") {
		fmt.Fprintln(stderr, "mobile-egress-admin setup-relay: invalid Funnel endpoint")
		return 2
	}
	if err := verifyMobileEgressSignature(*relayExecutable); err != nil {
		fmt.Fprintln(stderr, "mobile-egress-admin setup-relay: relay signature verification failed")
		return 1
	}
	if err := os.MkdirAll(filepath.Dir(installedRelayPath), 0o755); err != nil {
		fmt.Fprintln(stderr, "mobile-egress-admin setup-relay: create install directory failed")
		return 1
	}
	if err := copyFile(*relayExecutable, installedRelayPath); err != nil {
		fmt.Fprintln(stderr, "mobile-egress-admin setup-relay: install relay executable failed")
		return 1
	}
	if err := recoverIncompleteRelayState(relayStatePath); err != nil {
		fmt.Fprintln(stderr, "mobile-egress-admin setup-relay: recover incomplete relay state failed")
		return 1
	}
	command := exec.Command(installedRelayPath,
		"bootstrap-owner", "--state-dir", relayStatePath, "--public-name", *publicName,
		"--public-url", origin.String(), "--owner-csr-file", *ownerCSRFile,
	)
	output, err := command.Output()
	if err != nil {
		fmt.Fprintln(stderr, "mobile-egress-admin setup-relay: relay bootstrap failed")
		return 1
	}
	var result localbridge.OwnerBootstrapResult
	decoder := json.NewDecoder(bytes.NewReader(output))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&result) != nil || result.Role != "owner" || result.Serial == "" || result.CertificatePEM == "" || result.CACertificatePEM == "" {
		fmt.Fprintln(stderr, "mobile-egress-admin setup-relay: relay returned invalid public bootstrap output")
		return 1
	}
	if err := protectRelayState(); err != nil {
		fmt.Fprintln(stderr, "mobile-egress-admin setup-relay: protect relay state failed")
		return 1
	}
	if err := installRelayService(); err != nil {
		fmt.Fprintln(stderr, "mobile-egress-admin setup-relay: install relay service failed")
		return 1
	}
	if err := writeResult(*resultFile, result); err != nil {
		fmt.Fprintln(stderr, "mobile-egress-admin setup-relay: write public result failed")
		return 1
	}
	return 0
}

func verifyMobileEgressSignature(path string) error {
	adminPath, err := os.Executable()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return verifyMobileEgressSignaturePair(ctx, adminPath, path)
}

func verifyMobileEgressSignaturePair(ctx context.Context, adminPath, targetPath string) error {
	const script = `Import-Module "$env:SystemRoot\System32\WindowsPowerShell\v1.0\Modules\Microsoft.PowerShell.Security\Microsoft.PowerShell.Security.psd1" -ErrorAction Stop
$admin = Get-AuthenticodeSignature -LiteralPath $env:MOBILE_EGRESS_SIGNATURE_ADMIN_PATH
$target = Get-AuthenticodeSignature -LiteralPath $env:MOBILE_EGRESS_SIGNATURE_TARGET_PATH
if ($admin.Status -ne 'Valid' -or $target.Status -ne 'Valid' -or
    $null -eq $admin.SignerCertificate -or $null -eq $target.SignerCertificate -or
    $admin.SignerCertificate.Thumbprint -ne $target.SignerCertificate.Thumbprint) { exit 1 }`
	command := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	command.Env = append(os.Environ(),
		"MOBILE_EGRESS_SIGNATURE_ADMIN_PATH="+adminPath,
		"MOBILE_EGRESS_SIGNATURE_TARGET_PATH="+targetPath,
	)
	return command.Run()
}

func copyFile(sourcePath, destinationPath string) error {
	source, err := os.Open(filepath.Clean(sourcePath))
	if err != nil {
		return err
	}
	defer source.Close()
	temporary, err := os.CreateTemp(filepath.Dir(destinationPath), ".relay-install-")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := io.Copy(temporary, source); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	_ = os.Remove(destinationPath)
	return os.Rename(temporaryPath, destinationPath)
}

func protectRelayState() error {
	return exec.Command("icacls.exe", relayStatePath, "/inheritance:r", "/grant:r",
		"SYSTEM:(OI)(CI)F", `BUILTIN\Administrators:(OI)(CI)F`).Run()
}

func installRelayService() error {
	binPath := `"` + installedRelayPath + `" serve --state-dir "` + relayStatePath + `" --listen 127.0.0.1:8443`
	query := exec.Command("sc.exe", "query", "MobileEgressRelay").Run()
	var command *exec.Cmd
	if query == nil {
		command = exec.Command("sc.exe", "config", "MobileEgressRelay", "binPath=", binPath, "start=", "auto", "obj=", "LocalSystem")
	} else {
		command = exec.Command("sc.exe", "create", "MobileEgressRelay", "binPath=", binPath, "start=", "auto", "obj=", "LocalSystem")
	}
	if err := command.Run(); err != nil {
		return err
	}
	_ = exec.Command("sc.exe", "failure", "MobileEgressRelay", "reset=", "86400", "actions=", "restart/5000/restart/15000/\"\"/0").Run()
	return exec.Command("sc.exe", "start", "MobileEgressRelay").Run()
}

func recoverIncompleteRelayState(statePath string) error {
	info, err := os.Stat(statePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("relay state path is not a directory")
	}
	required := []string{"ca.crt", "ca.key", "relay.crt", "relay.key", "state.db"}
	for _, name := range required {
		if _, err := os.Stat(filepath.Join(statePath, name)); errors.Is(err, os.ErrNotExist) {
			return os.RemoveAll(statePath)
		} else if err != nil {
			return err
		}
	}
	return nil
}

func writeResult(path string, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(filepath.Clean(path)), ".relay-result-")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.Write(encoded); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	_ = os.Remove(path)
	return os.Rename(temporaryPath, path)
}
