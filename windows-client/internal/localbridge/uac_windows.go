//go:build windows

package localbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

type UACHelper struct {
	AdminExecutable string
	RelayExecutable string
}

func (helper UACHelper) Setup(ctx context.Context, request SetupRequest) (OwnerBootstrapResult, error) {
	if helper.AdminExecutable == "" || helper.RelayExecutable == "" || request.PublicName == "" || request.PublicURL == "" || request.OwnerCSRPEM == "" {
		return OwnerBootstrapResult{}, errors.New("elevated relay setup input is incomplete")
	}
	if err := verifySignedSibling(helper.AdminExecutable); err != nil {
		return OwnerBootstrapResult{}, errors.New("elevated helper signature verification failed")
	}
	if err := verifySignedSibling(helper.RelayExecutable); err != nil {
		return OwnerBootstrapResult{}, errors.New("relay signature verification failed")
	}
	temporaryDirectory, err := os.MkdirTemp("", "mobile-egress-relay-setup-")
	if err != nil {
		return OwnerBootstrapResult{}, errors.New("create elevated relay setup directory")
	}
	defer os.RemoveAll(temporaryDirectory)
	csrPath := filepath.Join(temporaryDirectory, "owner.csr")
	resultPath := filepath.Join(temporaryDirectory, "owner-result.json")
	if err := os.WriteFile(csrPath, []byte(request.OwnerCSRPEM), 0o600); err != nil {
		return OwnerBootstrapResult{}, errors.New("stage Owner certificate request")
	}
	arguments := []string{
		"setup-relay", "--relay-exe", helper.RelayExecutable, "--public-name", request.PublicName,
		"--public-url", request.PublicURL, "--owner-csr-file", csrPath, "--result-file", resultPath,
	}
	if err := shellExecuteElevated(helper.AdminExecutable, arguments); err != nil {
		return OwnerBootstrapResult{}, errors.New("elevated relay setup was cancelled or failed to start")
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(5 * time.Minute)
	defer deadline.Stop()
	for {
		if raw, err := os.ReadFile(resultPath); err == nil {
			if len(raw) == 0 || len(raw) > 512<<10 {
				return OwnerBootstrapResult{}, errors.New("elevated relay setup returned invalid public output")
			}
			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.DisallowUnknownFields()
			var result OwnerBootstrapResult
			if decoder.Decode(&result) != nil || result.Role != "owner" || result.Serial == "" || strings.Contains(string(raw), "PRIVATE KEY") {
				return OwnerBootstrapResult{}, errors.New("elevated relay setup returned invalid public output")
			}
			return result, nil
		}
		select {
		case <-ctx.Done():
			return OwnerBootstrapResult{}, ctx.Err()
		case <-deadline.C:
			return OwnerBootstrapResult{}, errors.New("elevated relay setup timed out")
		case <-ticker.C:
		}
	}
}

func (helper UACHelper) Rotate(ctx context.Context, request RotateRequest) (EndpointRotationResult, error) {
	if helper.AdminExecutable == "" || request.PublicName == "" || request.PublicURL == "" {
		return EndpointRotationResult{}, errors.New("elevated relay rotation input is incomplete")
	}
	if err := verifySignedSibling(helper.AdminExecutable); err != nil {
		return EndpointRotationResult{}, errors.New("elevated helper signature verification failed")
	}
	temporaryDirectory, err := os.MkdirTemp("", "mobile-egress-relay-rotation-")
	if err != nil {
		return EndpointRotationResult{}, errors.New("create elevated relay rotation directory")
	}
	defer os.RemoveAll(temporaryDirectory)
	resultPath := filepath.Join(temporaryDirectory, "rotation-result.json")
	arguments := []string{
		"rotate-relay", "--public-name", request.PublicName, "--public-url", request.PublicURL, "--result-file", resultPath,
	}
	if err := shellExecuteElevated(helper.AdminExecutable, arguments); err != nil {
		return EndpointRotationResult{}, errors.New("elevated relay rotation was cancelled or failed to start")
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(5 * time.Minute)
	defer deadline.Stop()
	for {
		if raw, err := os.ReadFile(resultPath); err == nil {
			if len(raw) == 0 || len(raw) > 4096 || strings.Contains(string(raw), "PRIVATE KEY") {
				return EndpointRotationResult{}, errors.New("elevated relay rotation returned invalid public output")
			}
			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.DisallowUnknownFields()
			var result EndpointRotationResult
			if decoder.Decode(&result) != nil || result.PublicURL == "" || result.Serial == "" {
				return EndpointRotationResult{}, errors.New("elevated relay rotation returned invalid public output")
			}
			return result, nil
		}
		select {
		case <-ctx.Done():
			return EndpointRotationResult{}, ctx.Err()
		case <-deadline.C:
			return EndpointRotationResult{}, errors.New("elevated relay rotation timed out")
		case <-ticker.C:
		}
	}
}

func (helper UACHelper) Repair(ctx context.Context) error {
	if helper.AdminExecutable == "" || helper.RelayExecutable == "" {
		return errors.New("elevated relay repair input is incomplete")
	}
	if err := verifySignedSibling(helper.AdminExecutable); err != nil {
		return errors.New("elevated helper signature verification failed")
	}
	if err := verifySignedSibling(helper.RelayExecutable); err != nil {
		return errors.New("relay signature verification failed")
	}
	temporaryDirectory, err := os.MkdirTemp("", "mobile-egress-relay-repair-")
	if err != nil {
		return errors.New("create elevated relay repair directory")
	}
	defer os.RemoveAll(temporaryDirectory)
	resultPath := filepath.Join(temporaryDirectory, "repair-result.json")
	if err := shellExecuteElevated(helper.AdminExecutable, []string{
		"repair-relay", "--relay-exe", helper.RelayExecutable, "--result-file", resultPath,
	}); err != nil {
		return errors.New("elevated relay repair was cancelled or failed to start")
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(5 * time.Minute)
	defer deadline.Stop()
	for {
		if raw, err := os.ReadFile(resultPath); err == nil {
			if len(raw) == 0 || len(raw) > 4096 || strings.Contains(string(raw), "PRIVATE KEY") {
				return errors.New("elevated relay repair returned invalid public output")
			}
			var result struct {
				Ready bool `json:"ready"`
			}
			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.DisallowUnknownFields()
			if decoder.Decode(&result) != nil || !result.Ready {
				return errors.New("elevated relay repair returned invalid public output")
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("elevated relay repair timed out")
		case <-ticker.C:
		}
	}
}

func verifySignedSibling(path string) error {
	controllerPath, err := os.Executable()
	if err != nil {
		return err
	}
	const script = `$controller = Get-AuthenticodeSignature -LiteralPath $args[0]
$sibling = Get-AuthenticodeSignature -LiteralPath $args[1]
if ($controller.Status -ne 'Valid' -or $sibling.Status -ne 'Valid' -or
    $null -eq $controller.SignerCertificate -or $null -eq $sibling.SignerCertificate -or
    $controller.SignerCertificate.Thumbprint -ne $sibling.SignerCertificate.Thumbprint) { exit 1 }`
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script, controllerPath, path).Run()
}

func shellExecuteElevated(executable string, arguments []string) error {
	verb, err := windows.UTF16PtrFromString("runas")
	if err != nil {
		return err
	}
	file, err := windows.UTF16PtrFromString(executable)
	if err != nil {
		return err
	}
	escaped := make([]string, len(arguments))
	for index, argument := range arguments {
		escaped[index] = syscall.EscapeArg(argument)
	}
	parameters, err := windows.UTF16PtrFromString(strings.Join(escaped, " "))
	if err != nil {
		return err
	}
	return windows.ShellExecute(0, verb, file, parameters, nil, windows.SW_HIDE)
}
