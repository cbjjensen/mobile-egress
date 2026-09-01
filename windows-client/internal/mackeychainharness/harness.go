// Package mackeychainharness builds and runs the signed, app-bundle-hosted
// macOS Keychain continuity integration test.
package mackeychainharness

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	controllerBundleIdentifier = "com.cbjjensen.mobile-egress.controller"
	integrationExecutableName  = "mobile-egress-keychain-integration.test"
	applicationNamePrefix      = "MobileEgressKeychain"
)

type Config struct {
	RepositoryRoot string
	ProfilePath    string
	Identity       string
	Workspace      string
	Output         io.Writer
}

type Command struct {
	Name string
	Args []string
	Dir  string
	Env  []string
}

type CommandResult struct {
	Stdout string
	Stderr string
}

type Runner interface {
	Run(context.Context, Command) (CommandResult, error)
}

func Run(ctx context.Context, runner Runner, config Config) error {
	if runner == nil {
		return errors.New("macOS Keychain harness command runner is required")
	}
	if !strings.HasPrefix(config.Identity, "Developer ID Application: ") {
		return errors.New("operator-supplied signing identity must be a Developer ID Application identity")
	}
	repositoryRoot, err := requireAbsoluteDirectory(config.RepositoryRoot, "repository root")
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(repositoryRoot, "go.mod")); err != nil {
		return fmt.Errorf("repository root does not contain go.mod: %w", err)
	}
	profilePath, err := requireAbsoluteFile(config.ProfilePath, "Developer ID distribution provisioning profile")
	if err != nil {
		return err
	}

	workspace, removeWorkspace, err := prepareWorkspace(config.Workspace)
	if err != nil {
		return err
	}
	if removeWorkspace {
		defer os.RemoveAll(workspace)
	}

	profilePlist := filepath.Join(workspace, "decoded-profile.plist")
	if _, err := runCommand(ctx, runner, Command{
		Name: "security",
		Args: []string{"cms", "-D", "-i", profilePath, "-o", profilePlist},
	}); err != nil {
		return fmt.Errorf("decode provisioning profile: %w", err)
	}
	applicationIdentifier, err := plistValue(ctx, runner, profilePlist, "Print :Entitlements:com.apple.application-identifier")
	if err != nil {
		return fmt.Errorf("read provisioned application identifier: %w", err)
	}
	teamIdentifier, err := plistValue(ctx, runner, profilePlist, "Print :Entitlements:com.apple.developer.team-identifier")
	if err != nil {
		return fmt.Errorf("read provisioned team identifier: %w", err)
	}
	profileAccessGroup, err := plistValue(ctx, runner, profilePlist, "Print :Entitlements:keychain-access-groups:0")
	if err != nil {
		return fmt.Errorf("read provisioned Keychain access group: %w", err)
	}
	getTaskAllow, err := plistValue(ctx, runner, profilePlist, "Print :Entitlements:com.apple.security.get-task-allow")
	if err != nil {
		return fmt.Errorf("read provisioning distribution entitlement: %w", err)
	}
	provisionsAllDevices, err := plistValue(ctx, runner, profilePlist, "Print :ProvisionsAllDevices")
	if err != nil {
		return fmt.Errorf("read provisioning distribution scope: %w", err)
	}
	if err := validateProvisionedIdentity(
		applicationIdentifier,
		teamIdentifier,
		profileAccessGroup,
		getTaskAllow,
		provisionsAllDevices,
		config.Identity,
	); err != nil {
		return err
	}
	identityResult, err := runCommand(ctx, runner, Command{
		Name: "security",
		Args: []string{"find-identity", "-v", "-p", "codesigning"},
	})
	if err != nil {
		return fmt.Errorf("enumerate code-signing identities: %w", err)
	}
	if !strings.Contains(identityResult.Stdout+identityResult.Stderr, `"`+config.Identity+`"`) {
		return errors.New("operator-supplied Developer ID Application identity is not available")
	}

	entitlementsPath := filepath.Join(workspace, "controller.entitlements.plist")
	if err := os.WriteFile(entitlementsPath, []byte(entitlementsPlist(applicationIdentifier, teamIdentifier)), 0o600); err != nil {
		return fmt.Errorf("write exact test entitlements: %w", err)
	}

	applications := make(map[string]string, 2)
	for _, version := range []string{"A", "B"} {
		applicationPath, err := buildAndSignApplication(
			ctx,
			runner,
			repositoryRoot,
			workspace,
			profilePath,
			entitlementsPath,
			applicationIdentifier,
			teamIdentifier,
			config.Identity,
			version,
		)
		if err != nil {
			return err
		}
		applications[version] = applicationPath
	}

	statePath := filepath.Join(workspace, "keychain-upgrade-state.json")
	versionAExecutable := applicationExecutable(applications["A"])
	versionBExecutable := applicationExecutable(applications["B"])
	versionAComplete := false
	defer func() {
		if !versionAComplete {
			return
		}
		if _, statErr := os.Stat(statePath); statErr != nil {
			return
		}
		_, _ = runSignedPhase(context.Background(), runner, versionBExecutable, repositoryRoot, statePath, "cleanup")
	}()
	if _, err := runSignedPhase(ctx, runner, versionAExecutable, repositoryRoot, statePath, "A"); err != nil {
		return fmt.Errorf("run signed version A Keychain phase: %w", err)
	}
	versionAComplete = true
	if _, err := runSignedPhase(ctx, runner, versionBExecutable, repositoryRoot, statePath, "B"); err != nil {
		return fmt.Errorf("run signed version B Keychain phase: %w", err)
	}
	if _, err := os.Stat(statePath); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("signed Keychain integration state was not cleaned up: %w", err)
	}
	if config.Output != nil {
		_, _ = fmt.Fprintln(config.Output, "Signed macOS Keychain version A/B integration passed and cleaned up.")
	}
	return nil
}

func buildAndSignApplication(
	ctx context.Context,
	runner Runner,
	repositoryRoot string,
	workspace string,
	profilePath string,
	entitlementsPath string,
	applicationIdentifier string,
	teamIdentifier string,
	identity string,
	version string,
) (string, error) {
	applicationPath := filepath.Join(workspace, applicationNamePrefix+version+".app")
	contentsPath := filepath.Join(applicationPath, "Contents")
	executablePath := applicationExecutable(applicationPath)
	if err := os.MkdirAll(filepath.Dir(executablePath), 0o700); err != nil {
		return "", fmt.Errorf("create version %s app bundle: %w", version, err)
	}
	if err := os.WriteFile(filepath.Join(contentsPath, "Info.plist"), []byte(infoPlist(version)), 0o600); err != nil {
		return "", fmt.Errorf("write version %s Info.plist: %w", version, err)
	}
	if err := copyFile(profilePath, filepath.Join(contentsPath, "embedded.provisionprofile")); err != nil {
		return "", fmt.Errorf("embed version %s provisioning profile: %w", version, err)
	}
	if _, err := runCommand(ctx, runner, Command{
		Name: "go",
		Args: []string{
			"test", "-c", "-tags", "macintegration",
			"-o", executablePath,
			"./windows-client/internal/securestore",
		},
		Dir: repositoryRoot,
		Env: []string{"CGO_ENABLED=1", "GOARCH=arm64"},
	}); err != nil {
		return "", fmt.Errorf("build version %s signed test executable: %w", version, err)
	}
	if err := os.Chmod(executablePath, 0o700); err != nil {
		return "", fmt.Errorf("protect version %s test executable: %w", version, err)
	}
	if _, err := runCommand(ctx, runner, Command{
		Name: "codesign",
		Args: []string{
			"--force", "--options", "runtime", "--timestamp=none",
			"--sign", identity,
			"--entitlements", entitlementsPath,
			applicationPath,
		},
	}); err != nil {
		return "", fmt.Errorf("sign version %s app bundle: %w", version, err)
	}
	if _, err := runCommand(ctx, runner, Command{
		Name: "codesign",
		Args: []string{"--verify", "--strict", "--verbose=2", applicationPath},
	}); err != nil {
		return "", fmt.Errorf("verify version %s app signature: %w", version, err)
	}

	signedEntitlements, err := runCommand(ctx, runner, Command{
		Name: "codesign",
		Args: []string{"--display", "--entitlements", ":-", executablePath},
	})
	if err != nil {
		return "", fmt.Errorf("read version %s signed entitlements: %w", version, err)
	}
	signedEntitlementsPath := filepath.Join(workspace, "signed-"+version+".entitlements.plist")
	if err := os.WriteFile(signedEntitlementsPath, []byte(signedEntitlements.Stdout), 0o600); err != nil {
		return "", fmt.Errorf("stage version %s signed entitlements: %w", version, err)
	}
	if err := verifySignedEntitlements(ctx, runner, signedEntitlementsPath, applicationIdentifier, teamIdentifier); err != nil {
		return "", fmt.Errorf("verify version %s signed entitlements: %w", version, err)
	}
	metadata, err := runCommand(ctx, runner, Command{
		Name: "codesign",
		Args: []string{"--display", "--verbose=4", applicationPath},
	})
	if err != nil {
		return "", fmt.Errorf("read version %s signature metadata: %w", version, err)
	}
	metadataText := metadata.Stdout + metadata.Stderr
	for _, exactLine := range []string{
		"Identifier=" + controllerBundleIdentifier,
		"TeamIdentifier=" + teamIdentifier,
		"Authority=" + identity,
	} {
		if !containsLine(metadataText, exactLine) {
			return "", fmt.Errorf("version %s signature metadata is missing %q", version, exactLine)
		}
	}
	return applicationPath, nil
}

func verifySignedEntitlements(ctx context.Context, runner Runner, path, applicationIdentifier, teamIdentifier string) error {
	checks := []struct {
		query string
		want  string
	}{
		{query: "Print :com.apple.application-identifier", want: applicationIdentifier},
		{query: "Print :com.apple.developer.team-identifier", want: teamIdentifier},
		{query: "Print :keychain-access-groups:0", want: applicationIdentifier},
	}
	for _, check := range checks {
		got, err := plistValue(ctx, runner, path, check.query)
		if err != nil {
			return err
		}
		if got != check.want {
			return fmt.Errorf("signed entitlement %q = %q, want %q", check.query, got, check.want)
		}
	}
	return nil
}

func validateProvisionedIdentity(applicationIdentifier, teamIdentifier, accessGroup, getTaskAllow, provisionsAllDevices, identity string) error {
	if len(teamIdentifier) != 10 {
		return errors.New("provisioned team identifier must contain exactly 10 uppercase letters or digits")
	}
	for _, character := range teamIdentifier {
		if (character < 'A' || character > 'Z') && (character < '0' || character > '9') {
			return errors.New("provisioned team identifier must contain exactly 10 uppercase letters or digits")
		}
	}
	expectedApplicationIdentifier := teamIdentifier + "." + controllerBundleIdentifier
	if applicationIdentifier != expectedApplicationIdentifier {
		return fmt.Errorf("provisioned application identifier must equal %s", expectedApplicationIdentifier)
	}
	if accessGroup != expectedApplicationIdentifier && accessGroup != teamIdentifier+".*" {
		return errors.New("provisioning profile does not authorize the exact private controller Keychain group")
	}
	if !strings.EqualFold(strings.TrimSpace(getTaskAllow), "false") ||
		!strings.EqualFold(strings.TrimSpace(provisionsAllDevices), "true") {
		return errors.New("provisioning profile is not a Developer ID distribution profile")
	}
	if !strings.HasSuffix(identity, "("+teamIdentifier+")") {
		return errors.New("Developer ID Application identity team does not match provisioning profile")
	}
	return nil
}

func runSignedPhase(ctx context.Context, runner Runner, executable, repositoryRoot, statePath, phase string) (CommandResult, error) {
	return runCommand(ctx, runner, Command{
		Name: executable,
		Args: []string{"-test.run=^TestKeychainStoreSignedVersionUpgradePhase$", "-test.v"},
		Dir:  repositoryRoot,
		Env: []string{
			"MOBILE_EGRESS_MAC_KEYCHAIN_PHASE=" + phase,
			"MOBILE_EGRESS_MAC_KEYCHAIN_STATE=" + statePath,
		},
	})
}

func runCommand(ctx context.Context, runner Runner, command Command) (CommandResult, error) {
	result, err := runner.Run(ctx, command)
	if err != nil {
		detail := strings.TrimSpace(result.Stdout + "\n" + result.Stderr)
		if detail != "" {
			return result, fmt.Errorf("%w: %s", err, detail)
		}
	}
	return result, err
}

func plistValue(ctx context.Context, runner Runner, path, query string) (string, error) {
	result, err := runCommand(ctx, runner, Command{
		Name: "/usr/libexec/PlistBuddy",
		Args: []string{"-c", query, path},
	})
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(result.Stdout)
	if value == "" {
		return "", errors.New("plist value is empty")
	}
	return value, nil
}

func requireAbsoluteDirectory(path, label string) (string, error) {
	if path == "" || !filepath.IsAbs(path) {
		return "", fmt.Errorf("%s must be an absolute path", label)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("%s: %w", label, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s must be a directory", label)
	}
	return filepath.Clean(path), nil
}

func requireAbsoluteFile(path, label string) (string, error) {
	if path == "" || !filepath.IsAbs(path) {
		return "", fmt.Errorf("%s must be an absolute path", label)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("%s: %w", label, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s must be a regular file", label)
	}
	return filepath.Clean(path), nil
}

func prepareWorkspace(configured string) (string, bool, error) {
	if configured == "" {
		workspace, err := os.MkdirTemp("", "mobile-egress-keychain-integration-")
		return workspace, true, err
	}
	if !filepath.IsAbs(configured) {
		return "", false, errors.New("Keychain harness workspace must be absolute")
	}
	if err := os.MkdirAll(configured, 0o700); err != nil {
		return "", false, err
	}
	return filepath.Clean(configured), false, nil
}

func applicationExecutable(applicationPath string) string {
	return filepath.Join(applicationPath, "Contents", "MacOS", integrationExecutableName)
}

func copyFile(source, destination string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return os.WriteFile(destination, data, 0o600)
}

func containsLine(value, line string) bool {
	for _, candidate := range strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(candidate) == line {
			return true
		}
	}
	return false
}

func entitlementsPlist(applicationIdentifier, teamIdentifier string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>com.apple.application-identifier</key>
  <string>` + applicationIdentifier + `</string>
  <key>com.apple.developer.team-identifier</key>
  <string>` + teamIdentifier + `</string>
  <key>keychain-access-groups</key>
  <array>
    <string>` + applicationIdentifier + `</string>
  </array>
</dict>
</plist>
`
}

func infoPlist(version string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleDevelopmentRegion</key>
  <string>en</string>
  <key>CFBundleExecutable</key>
  <string>` + integrationExecutableName + `</string>
  <key>CFBundleIdentifier</key>
  <string>` + controllerBundleIdentifier + `</string>
  <key>CFBundleName</key>
  <string>Mobile Egress Keychain Integration</string>
  <key>CFBundlePackageType</key>
  <string>APPL</string>
  <key>CFBundleShortVersionString</key>
  <string>1.0.` + map[string]string{"A": "1", "B": "2"}[version] + `</string>
  <key>CFBundleVersion</key>
  <string>` + map[string]string{"A": "1", "B": "2"}[version] + `</string>
</dict>
</plist>
`
}
