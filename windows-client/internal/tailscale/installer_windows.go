//go:build windows

package tailscale

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const tailscaleMSIPathEnvironment = "MOBILE_EGRESS_TAILSCALE_MSI_PATH"

func DefaultInstaller() Installer {
	return Installer{VerifyAuthenticode: verifyAuthenticode, ElevatedInstall: installWithUAC}
}

func verifyAuthenticode(path string) (Signature, error) {
	const script = `$path = $env:MOBILE_EGRESS_TAILSCALE_MSI_PATH
if ([string]::IsNullOrWhiteSpace($path) -or -not [IO.Path]::IsPathRooted($path) -or -not (Test-Path -LiteralPath $path -PathType Leaf)) { exit 87 }
$signature = Get-AuthenticodeSignature -LiteralPath $path
[pscustomobject]@{ valid = ($signature.Status -eq 'Valid'); subject = $signature.SignerCertificate.Subject } | ConvertTo-Json -Compress`
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	output, err := powershellWithMSIPath(ctx, script, path).Output()
	if err != nil {
		return Signature{}, errors.New("Authenticode verification command failed")
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	decoder.DisallowUnknownFields()
	var signature Signature
	if err := decoder.Decode(&signature); err != nil {
		return Signature{}, errors.New("Authenticode verification returned invalid status")
	}
	return signature, nil
}

func installWithUAC(path string) error {
	const script = `$path = $env:MOBILE_EGRESS_TAILSCALE_MSI_PATH
if ([string]::IsNullOrWhiteSpace($path) -or -not [IO.Path]::IsPathRooted($path) -or [IO.Path]::GetExtension($path) -ine '.msi' -or $path.Contains('"') -or -not (Test-Path -LiteralPath $path -PathType Leaf)) { exit 87 }
$quotedPath = '"' + $path + '"'
$process = Start-Process -FilePath 'msiexec.exe' -ArgumentList @('/i', $quotedPath, '/passive', '/norestart') -Verb RunAs -Wait -PassThru
if ($process.ExitCode -ne 0) { exit $process.ExitCode }`
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := powershellWithMSIPath(ctx, script, path).Run(); err != nil {
		return elevatedMSIFailure(err)
	}
	return nil
}

func elevatedMSIFailure(err error) error {
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return fmt.Errorf("elevated MSI installation failed with Windows Installer code %d", exitError.ExitCode())
	}
	return errors.New("elevated MSI installation failed")
}

func powershellWithMSIPath(ctx context.Context, script, path string) *exec.Cmd {
	command := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	environment := os.Environ()
	filtered := make([]string, 0, len(environment)+1)
	for _, value := range environment {
		name, _, found := strings.Cut(value, "=")
		if found && (strings.EqualFold(name, tailscaleMSIPathEnvironment) || strings.EqualFold(name, "PSModulePath")) {
			continue
		}
		filtered = append(filtered, value)
	}
	command.Env = append(filtered, tailscaleMSIPathEnvironment+"="+path)
	return command
}
