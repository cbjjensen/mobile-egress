//go:build windows

package prerequisites

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// Microsoft documents the Evergreen bootstrapper for prerequisite deployment:
// https://learn.microsoft.com/microsoft-edge/webview2/concepts/distribution
func EnsureWebView2Installed(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	return EnsureRuntime(ctx, CheckWebView2Installed, installWebView2)
}

func installWebView2(ctx context.Context) error {
	const guidance = "Mobile Egress is installed, but WebView2 setup did not finish. Check your internet connection and run MobileEgressSetup.exe again. If this is a work PC, ask your administrator whether runtime installation is allowed."
	directory, err := os.MkdirTemp("", "mobile-egress-webview2-")
	if err != nil {
		return errors.New(guidance)
	}
	defer os.RemoveAll(directory)
	path := filepath.Join(directory, "MicrosoftEdgeWebview2Setup.exe")
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://go.microsoft.com/fwlink/p/?LinkId=2124703", nil)
	if err != nil {
		return errors.New(guidance)
	}
	client := &http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" || len(via) >= 10 {
			return errors.New("invalid runtime redirect")
		}
		return nil
	}}
	response, err := client.Do(request)
	if err != nil {
		return errors.New(guidance)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return errors.New(guidance)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return errors.New(guidance)
	}
	count, copyErr := io.Copy(file, io.LimitReader(response.Body, (32<<20)+1))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil || count == 0 || count > 32<<20 {
		return errors.New(guidance)
	}
	// Keep the verified bytes locked until the official installer exits. Never
	// import trust or accept the application's self-signed publisher for this file.
	const script = `$ErrorActionPreference = 'Stop'
$path = $env:MOBILE_EGRESS_WEBVIEW2_INSTALLER
$lock = [IO.File]::Open($path, 'Open', 'Read', 'Read')
try {
  Import-Module "$env:SystemRoot\System32\WindowsPowerShell\v1.0\Modules\Microsoft.PowerShell.Security\Microsoft.PowerShell.Security.psd1" -ErrorAction Stop
  $signature = Get-AuthenticodeSignature -LiteralPath $path
  if ($signature.Status -ne 'Valid' -or $null -eq $signature.SignerCertificate -or $signature.SignerCertificate.GetNameInfo('SimpleName', $false) -ne 'Microsoft Corporation') { throw 'Runtime publisher verification failed' }
  $process = Start-Process -FilePath $path -ArgumentList '/silent','/install' -WindowStyle Hidden -PassThru
  if (-not $process.WaitForExit(420000)) { $process.Kill(); throw 'Runtime installation timed out' }
  if ($process.ExitCode -ne 0) { throw 'Runtime installation failed' }
} finally { $lock.Dispose() }`
	powershell := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	command := exec.CommandContext(ctx, powershell, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", script)
	command.Env = append(os.Environ(), "MOBILE_EGRESS_WEBVIEW2_INSTALLER="+path)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	if command.Run() != nil {
		return errors.New(guidance)
	}
	// The bootstrapper can finish before runtime registration becomes visible.
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if CheckWebView2Installed() == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.New(guidance)
		case <-timer.C:
			return errors.New(guidance)
		case <-ticker.C:
		}
	}
}
