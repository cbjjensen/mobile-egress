package setup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	SetupExecutableName      = "MobileEgressSetup.exe"
	ControllerExecutableName = "mobile-egress-windows.exe"
	AdminExecutableName      = "mobile-egress-admin.exe"
	RelayExecutableName      = "mobile-egress-relay.exe"
	ClientExecutableName     = "mobile-egress-client.exe"
	ManifestName             = "release-manifest.json"
	PublicCertificateName    = "mobile-egress-code-signing.cer"
	PublicIdentityRecordName = "release-signing-certificate.txt"
	InstallRoot              = `C:\Program Files\MobileEgress\Controller`
)

var (
	ErrConfirmationDeclined    = errors.New("setup confirmation was declined")
	verifiedReleaseExecutables = [...]string{
		SetupExecutableName,
		ControllerExecutableName,
		AdminExecutableName,
		RelayExecutableName,
	}
)

type ParentOptions struct {
	Executable          string
	InstalledController string
	Fingerprint         string
	Nonce               string
	Exchange            Exchange
}

type ParentPlatform interface {
	IsElevated() (bool, error)
	Confirm(fingerprint string) (bool, error)
	ElevateAndWait(executable, nonce string) error
	Launch(executable string) error
}

func RunParent(ctx context.Context, options ParentOptions, platform ParentPlatform) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	elevated, err := platform.IsElevated()
	if err != nil {
		return errors.New("inspect setup elevation")
	}
	if elevated {
		return errors.New("run Mobile Egress Setup normally, not from an elevated process")
	}
	confirmed, err := platform.Confirm(options.Fingerprint)
	if err != nil {
		return errors.New("show publisher confirmation")
	}
	if !confirmed {
		return ErrConfirmationDeclined
	}
	if err := options.Exchange.CreateRequest(options.Nonce); err != nil {
		return err
	}
	defer os.Remove(options.Exchange.RequestPath(options.Nonce))
	defer os.Remove(options.Exchange.ResultPath(options.Nonce))
	if err := platform.ElevateAndWait(options.Executable, options.Nonce); err != nil {
		return errors.New("elevated setup was cancelled or failed")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	result, err := options.Exchange.ReadResult(options.Nonce)
	if err != nil {
		return err
	}
	if !result.Success {
		return fmt.Errorf("setup failed (%s): %s", result.Code, result.Message)
	}
	if err := platform.Launch(options.InstalledController); err != nil {
		return errors.New("launch installed controller")
	}
	return nil
}

type ElevatedOptions struct {
	Nonce      string
	ReleaseDir string
	Exchange   Exchange
	Identity   Identity
}

type TrustChanges struct {
	RootAdded             bool
	TrustedPublisherAdded bool
}

type InstallFile struct {
	Source      string
	Destination string
}

type ElevatedPlatform interface {
	IsElevated() (bool, error)
	EnsureTrust(Identity) (TrustChanges, error)
	RollbackTrust(Identity, TrustChanges) error
	VerifyAuthenticode(path string, identity Identity) error
	Install(files []InstallFile, identity Identity) error
	CreateShortcut(controllerPath string) error
}

func RunElevated(options ElevatedOptions, platform ElevatedPlatform) (resultErr error) {
	if _, err := options.Exchange.ConsumeRequest(options.Nonce); err != nil {
		return err
	}
	elevated, err := platform.IsElevated()
	if err != nil || !elevated {
		return errors.New("internal setup mode requires elevation")
	}
	if filepath.Base(options.ReleaseDir) == "." || !filepath.IsAbs(options.ReleaseDir) {
		return errors.New("release directory is invalid")
	}
	for _, name := range verifiedReleaseExecutables {
		info, err := os.Lstat(filepath.Join(options.ReleaseDir, name))
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("required signed release file is missing: %s", name)
		}
	}
	changes, err := platform.EnsureTrust(options.Identity)
	if err != nil {
		return errors.New("install publisher trust")
	}
	defer func() {
		if resultErr != nil && (changes.RootAdded || changes.TrustedPublisherAdded) {
			resultErr = errors.Join(resultErr, platform.RollbackTrust(options.Identity, changes))
		}
	}()
	for _, name := range verifiedReleaseExecutables {
		if err := platform.VerifyAuthenticode(filepath.Join(options.ReleaseDir, name), options.Identity); err != nil {
			return fmt.Errorf("verify signed release file %s: %w", name, err)
		}
	}
	files := []InstallFile{
		{Source: filepath.Join(options.ReleaseDir, ControllerExecutableName), Destination: filepath.Join(InstallRoot, ControllerExecutableName)},
		{Source: filepath.Join(options.ReleaseDir, AdminExecutableName), Destination: filepath.Join(InstallRoot, AdminExecutableName)},
		{Source: filepath.Join(options.ReleaseDir, RelayExecutableName), Destination: filepath.Join(InstallRoot, RelayExecutableName)},
	}
	if err := platform.Install(files, options.Identity); err != nil {
		return errors.New("install signed release files")
	}
	controllerPath := filepath.Join(InstallRoot, ControllerExecutableName)
	if err := platform.CreateShortcut(controllerPath); err != nil {
		return errors.New("create Start Menu shortcut")
	}
	return nil
}
