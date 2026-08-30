package setup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	ErrConfirmationDeclined = errors.New("setup confirmation was declined")
	ErrTrustRollback        = errors.New("publisher trust rollback failed")

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
	Identity            Identity
	Nonce               string
	Exchange            Exchange
}

type ParentPlatform interface {
	IsElevated() (bool, error)
	VerifyPreTrustAuthenticode(path string, identity Identity) error
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
	if err := platform.VerifyPreTrustAuthenticode(options.Executable, options.Identity); err != nil {
		return errors.New("setup Authenticode signature is not intact and bound to the expected signer")
	}
	confirmed, err := platform.Confirm(options.Identity.Fingerprint)
	if err != nil {
		return errors.New("show publisher confirmation")
	}
	if !confirmed {
		return ErrConfirmationDeclined
	}
	setupSHA256, err := FileSHA256(options.Executable)
	if err != nil {
		return errors.New("hash confirmed setup executable")
	}
	if err := options.Exchange.CreateRequest(options.Nonce, setupSHA256); err != nil {
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
	if result.SetupSHA256 != setupSHA256 {
		return errors.New("elevated setup result does not match the confirmed setup executable")
	}
	if err := platform.Launch(options.InstalledController); err != nil {
		return errors.New("launch installed controller")
	}
	return nil
}

type ElevatedOptions struct {
	Nonce     string
	SetupPath string
	Exchange  Exchange
	Identity  Identity
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
	VerifyPreTrustAuthenticode(path string, identity Identity) error
	EnsureTrust(Identity) (TrustChanges, error)
	RollbackTrust(Identity, TrustChanges) error
	VerifyAuthenticode(path string, identity Identity) error
	Install(files []InstallFile, identity Identity) error
}

func RunElevated(options ElevatedOptions, platform ElevatedPlatform) (resultErr error) {
	elevated, err := platform.IsElevated()
	if err != nil || !elevated {
		return errors.New("internal setup mode requires elevation")
	}
	request, err := options.Exchange.ConsumeRequest(options.Nonce)
	if err != nil {
		return err
	}
	if !filepath.IsAbs(options.SetupPath) || !strings.EqualFold(filepath.Base(options.SetupPath), SetupExecutableName) {
		return errors.New("setup executable path is invalid")
	}
	setupSHA256, err := FileSHA256(options.SetupPath)
	if err != nil || setupSHA256 != request.SetupSHA256 {
		return errors.New("setup executable digest does not match the confirmed request")
	}
	if err := platform.VerifyPreTrustAuthenticode(options.SetupPath, options.Identity); err != nil {
		return errors.New("setup Authenticode signature is not intact and bound to the expected signer")
	}
	releaseDir := filepath.Dir(options.SetupPath)
	for _, name := range verifiedReleaseExecutables {
		info, err := os.Lstat(filepath.Join(releaseDir, name))
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("required signed release file is missing: %s", name)
		}
	}
	changes, err := platform.EnsureTrust(options.Identity)
	if err != nil {
		return rollbackTrustAfterFailure(platform, options.Identity, changes, fmt.Errorf("install publisher trust: %w", err))
	}
	defer func() {
		if resultErr != nil && (changes.RootAdded || changes.TrustedPublisherAdded) {
			resultErr = rollbackTrustAfterFailure(platform, options.Identity, changes, resultErr)
		}
	}()
	for _, name := range verifiedReleaseExecutables {
		if err := platform.VerifyAuthenticode(filepath.Join(releaseDir, name), options.Identity); err != nil {
			return fmt.Errorf("verify signed release file %s: %w", name, err)
		}
	}
	files := []InstallFile{
		{Source: filepath.Join(releaseDir, ControllerExecutableName), Destination: filepath.Join(InstallRoot, ControllerExecutableName)},
		{Source: filepath.Join(releaseDir, AdminExecutableName), Destination: filepath.Join(InstallRoot, AdminExecutableName)},
		{Source: filepath.Join(releaseDir, RelayExecutableName), Destination: filepath.Join(InstallRoot, RelayExecutableName)},
	}
	if err := platform.Install(files, options.Identity); err != nil {
		return errors.New("transactionally install signed release files and Start Menu shortcut")
	}
	return nil
}

func rollbackTrustAfterFailure(platform ElevatedPlatform, identity Identity, changes TrustChanges, cause error) error {
	if !changes.RootAdded && !changes.TrustedPublisherAdded {
		return cause
	}
	if err := platform.RollbackTrust(identity, changes); err != nil {
		return errors.Join(cause, ErrTrustRollback, fmt.Errorf("roll back publisher trust: %w", err))
	}
	return cause
}
