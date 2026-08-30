//go:build windows

package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"mobile-egress/windows-client/internal/setup"
)

const (
	parentMode          = "parent"
	elevatedInstallMode = "elevated-install"
)

var commandNoncePattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func main() {
	platform := setup.NewWindowsPlatform()
	mode, nonce, err := parseMode(os.Args[1:])
	if err == nil {
		if mode == elevatedInstallMode {
			err = runElevated(nonce, platform)
			if err != nil {
				os.Exit(1)
			}
			return
		}
		err = runParent(platform)
	}
	if errors.Is(err, setup.ErrConfirmationDeclined) {
		return
	}
	platform.ShowError(err)
	os.Exit(1)
}

func parseMode(arguments []string) (string, string, error) {
	if len(arguments) == 0 {
		return parentMode, "", nil
	}
	if len(arguments) == 2 && arguments[0] == "--internal-elevated-install" && commandNoncePattern.MatchString(arguments[1]) {
		return elevatedInstallMode, arguments[1], nil
	}
	return "", "", errors.New("Mobile Egress Setup accepts no custom operation or destination")
}

func runParent(platform *setup.WindowsPlatform) error {
	identity, err := setup.EmbeddedIdentity()
	if err != nil {
		return err
	}
	executable, err := ownExecutable()
	if err != nil {
		return err
	}
	exchangeRoot, err := setup.DefaultExchangeRoot()
	if err != nil {
		return err
	}
	nonceBytes := make([]byte, 32)
	if _, err := rand.Read(nonceBytes); err != nil {
		return errors.New("create setup request nonce")
	}
	nonce := hex.EncodeToString(nonceBytes)
	return setup.RunParent(context.Background(), setup.ParentOptions{
		Executable:          executable,
		InstalledController: filepath.Join(setup.InstallRoot, setup.ControllerExecutableName),
		Identity:            identity,
		Nonce:               nonce,
		Exchange:            setup.Exchange{Root: exchangeRoot},
	}, platform)
}

func runElevated(nonce string, platform *setup.WindowsPlatform) error {
	identity, err := setup.EmbeddedIdentity()
	if err != nil {
		return err
	}
	executable, err := ownExecutable()
	if err != nil {
		return err
	}
	exchangeRoot, err := setup.DefaultExchangeRoot()
	if err != nil {
		return err
	}
	exchange := setup.Exchange{Root: exchangeRoot}
	installErr := setup.RunElevated(setup.ElevatedOptions{
		Nonce:     nonce,
		SetupPath: executable,
		Exchange:  exchange,
		Identity:  identity,
	}, platform)
	setupDigest, digestErr := setup.FileSHA256(executable)
	if installErr == nil && digestErr != nil {
		installErr = digestErr
	}
	result := setup.Result{Nonce: nonce, SetupSHA256: setupDigest, Success: true, Message: "Mobile Egress was installed."}
	if installErr != nil {
		result = failureResult(nonce, installErr)
	}
	if err := exchange.WriteResult(result); err != nil {
		return errors.Join(installErr, err)
	}
	return nil
}

func failureResult(nonce string, installErr error) setup.Result {
	code := "install_failed"
	message := "Installation did not complete. Verify the Mobile Egress publisher trust before retrying."
	if errors.Is(installErr, setup.ErrTrustRollback) {
		code = "trust_rollback_failed"
		message = "Installation did not complete and publisher trust cleanup failed. Review the Mobile Egress certificate entries before retrying."
	}
	return setup.Result{
		Nonce:   nonce,
		Success: false,
		Code:    code,
		Message: message,
	}
}

func ownExecutable() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", errors.New("locate Mobile Egress Setup")
	}
	executable, err = filepath.Abs(executable)
	if err != nil || !strings.EqualFold(filepath.Base(executable), setup.SetupExecutableName) {
		return "", fmt.Errorf("setup executable must keep the exact name %s", setup.SetupExecutableName)
	}
	return filepath.Clean(executable), nil
}
