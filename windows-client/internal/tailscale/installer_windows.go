//go:build windows

package tailscale

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"time"
)

func DefaultInstaller() Installer {
	return Installer{VerifyAuthenticode: verifyAuthenticode, ElevatedInstall: installWithUAC}
}

func verifyAuthenticode(path string) (Signature, error) {
	const script = `$signature = Get-AuthenticodeSignature -LiteralPath $args[0]
[pscustomobject]@{ valid = ($signature.Status -eq 'Valid'); subject = $signature.SignerCertificate.Subject } | ConvertTo-Json -Compress`
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script, path).Output()
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
	const script = `$process = Start-Process -FilePath 'msiexec.exe' -ArgumentList @('/i', $args[0], '/passive', '/norestart') -Verb RunAs -Wait -PassThru
if ($process.ExitCode -ne 0) { exit $process.ExitCode }`
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script, path).Run(); err != nil {
		return errors.New("elevated MSI installation failed")
	}
	return nil
}
