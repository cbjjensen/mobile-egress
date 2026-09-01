package mackeychainharness

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	fixtureTeamID        = "A1B2C3D4E5"
	fixtureBundleID      = "com.cbjjensen.mobile-egress.controller"
	fixtureApplicationID = fixtureTeamID + "." + fixtureBundleID
	fixtureIdentity      = "Developer ID Application: Fixture Operator (A1B2C3D4E5)"
)

// Mutation caught: continuing to external build or signing tools when required operator inputs are absent.
func TestHarnessRejectsMissingSigningInputsBeforeCreatingArtifacts(t *testing.T) {
	t.Parallel()

	repository, profile, workspace := harnessFixturePaths(t)
	runner := &fixtureRunner{workspace: workspace}
	err := Run(context.Background(), runner, Config{
		RepositoryRoot: repository,
		ProfilePath:    profile,
		Workspace:      workspace,
	})
	if err == nil {
		t.Fatal("Run() succeeded without signing identity")
	}
	if _, statErr := os.Stat(filepath.Join(workspace, "external-tool-ran")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("external tool marker error = %v, want no invocation artifact", statErr)
	}
}

// Mutation caught: signing bundles whose provisioning application identifier does not match the exact team and controller bundle ID.
func TestHarnessRejectsMismatchedProvisionedIdentityBeforeBuildOrSigning(t *testing.T) {
	t.Parallel()

	repository, profile, workspace := harnessFixturePaths(t)
	runner := &fixtureRunner{
		workspace:            workspace,
		profileApplicationID: fixtureTeamID + ".com.cbjjensen.mobile-egress.helper",
		profileTeamID:        fixtureTeamID,
	}
	err := Run(context.Background(), runner, Config{
		RepositoryRoot: repository,
		ProfilePath:    profile,
		Identity:       fixtureIdentity,
		Workspace:      workspace,
	})
	if err == nil {
		t.Fatal("Run() succeeded with mismatched application identifier")
	}
	if _, statErr := os.Stat(filepath.Join(workspace, "signed-artifact")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("signed artifact marker error = %v, want no signed artifact", statErr)
	}
}

// Mutation caught: omitting either app version, embedded profile, exact entitlements, signature verification, or A/B state transition.
func TestHarnessBuildsVerifiesAndRunsTwoSignedApplicationVersions(t *testing.T) {
	t.Parallel()

	repository, profile, workspace := harnessFixturePaths(t)
	runner := &fixtureRunner{
		workspace:            workspace,
		profileApplicationID: fixtureApplicationID,
		profileTeamID:        fixtureTeamID,
		profileAccessGroup:   fixtureTeamID + ".*",
	}
	if err := Run(context.Background(), runner, Config{
		RepositoryRoot: repository,
		ProfilePath:    profile,
		Identity:       fixtureIdentity,
		Workspace:      workspace,
	}); err != nil {
		t.Fatal(err)
	}

	for _, version := range []string{"A", "B"} {
		app := filepath.Join(workspace, "MobileEgressKeychain"+version+".app")
		info := mustReadFile(t, filepath.Join(app, "Contents", "Info.plist"))
		bundleVersion := map[string]string{"A": "1", "B": "2"}[version]
		if !strings.Contains(info, "<string>"+fixtureBundleID+"</string>") ||
			!strings.Contains(info, "<string>"+bundleVersion+"</string>") {
			t.Fatalf("version %s Info.plist does not contain exact bundle identity/version", version)
		}
		if got := mustReadFile(t, filepath.Join(app, "Contents", "embedded.provisionprofile")); got != "non-secret-profile-fixture" {
			t.Fatalf("version %s embedded profile = %q", version, got)
		}
		if _, err := os.Stat(filepath.Join(app, "Contents", "_CodeSignature", "fixture-verified")); err != nil {
			t.Fatalf("version %s signature verification artifact: %v", version, err)
		}
	}
	entitlements := mustReadFile(t, filepath.Join(workspace, "controller.entitlements.plist"))
	if strings.Count(entitlements, "<string>"+fixtureApplicationID+"</string>") != 2 {
		t.Fatalf("entitlements do not bind both application identifier and private group: %s", entitlements)
	}
	if _, err := os.Stat(filepath.Join(workspace, "phase-a-complete")); err != nil {
		t.Fatalf("version A phase artifact: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "phase-b-complete")); err != nil {
		t.Fatalf("version B phase artifact: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "keychain-upgrade-state.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("state cleanup error = %v, want removed state", err)
	}
}

// Mutation caught: leaving the version-A Keychain fixture behind when version B cannot complete.
func TestHarnessRunsCleanupPhaseAfterVersionBFailure(t *testing.T) {
	t.Parallel()

	repository, profile, workspace := harnessFixturePaths(t)
	runner := &fixtureRunner{
		workspace:            workspace,
		profileApplicationID: fixtureApplicationID,
		profileTeamID:        fixtureTeamID,
		profileAccessGroup:   fixtureTeamID + ".*",
		failVersionB:         true,
	}
	err := Run(context.Background(), runner, Config{
		RepositoryRoot: repository,
		ProfilePath:    profile,
		Identity:       fixtureIdentity,
		Workspace:      workspace,
	})
	if err == nil {
		t.Fatal("Run() succeeded despite version B phase failure")
	}
	if _, statErr := os.Stat(filepath.Join(workspace, "cleanup-complete")); statErr != nil {
		t.Fatalf("cleanup phase artifact: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(workspace, "keychain-upgrade-state.json")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("state cleanup error = %v, want removed state", statErr)
	}
}

func harnessFixturePaths(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	repository := filepath.Join(root, "repository")
	workspace := filepath.Join(root, "harness")
	if err := os.MkdirAll(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "go.mod"), []byte("module fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(root, "controller.provisionprofile")
	if err := os.WriteFile(profile, []byte("non-secret-profile-fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	return repository, profile, workspace
}

func mustReadFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

type fixtureRunner struct {
	workspace            string
	profileApplicationID string
	profileTeamID        string
	profileAccessGroup   string
	failVersionB         bool
}

func (runner *fixtureRunner) Run(_ context.Context, command Command) (CommandResult, error) {
	if err := os.WriteFile(filepath.Join(runner.workspace, "external-tool-ran"), []byte("non-secret fixture"), 0o600); err != nil {
		return CommandResult{}, err
	}
	switch filepath.Base(command.Name) {
	case "security", "security.exe":
		return runner.runSecurity(command)
	case "PlistBuddy", "PlistBuddy.exe":
		return runner.runPlistBuddy(command)
	case "go", "go.exe":
		return runner.runGo(command)
	case "codesign", "codesign.exe":
		return runner.runCodesign(command)
	default:
		return runner.runPhase(command)
	}
}

func (runner *fixtureRunner) runSecurity(command Command) (CommandResult, error) {
	if len(command.Args) > 0 && command.Args[0] == "cms" {
		output := argumentValue(command.Args, "-o")
		if output == "" {
			return CommandResult{}, errors.New("fixture security cms missing output")
		}
		if err := os.WriteFile(output, []byte("decoded non-secret profile fixture"), 0o600); err != nil {
			return CommandResult{}, err
		}
		return CommandResult{}, nil
	}
	if len(command.Args) >= 3 && command.Args[0] == "find-identity" {
		return CommandResult{Stdout: "1) ABCDEF \"" + fixtureIdentity + "\"\n  1 valid identities found\n"}, nil
	}
	return CommandResult{}, errors.New("unexpected security fixture command")
}

func (runner *fixtureRunner) runPlistBuddy(command Command) (CommandResult, error) {
	query := argumentValue(command.Args, "-c")
	switch query {
	case "Print :Entitlements:com.apple.application-identifier", "Print :com.apple.application-identifier":
		return CommandResult{Stdout: runner.profileApplicationID + "\n"}, nil
	case "Print :Entitlements:com.apple.developer.team-identifier", "Print :com.apple.developer.team-identifier":
		return CommandResult{Stdout: runner.profileTeamID + "\n"}, nil
	case "Print :Entitlements:keychain-access-groups:0", "Print :keychain-access-groups:0":
		plistPath := command.Args[len(command.Args)-1]
		if runner.profileAccessGroup != "" && !strings.Contains(filepath.Base(plistPath), "signed-") {
			return CommandResult{Stdout: runner.profileAccessGroup + "\n"}, nil
		}
		return CommandResult{Stdout: runner.profileApplicationID + "\n"}, nil
	case "Print :Entitlements:com.apple.security.get-task-allow":
		return CommandResult{Stdout: "false\n"}, nil
	case "Print :ProvisionsAllDevices":
		return CommandResult{Stdout: "true\n"}, nil
	default:
		return CommandResult{}, errors.New("unexpected PlistBuddy fixture query: " + query)
	}
}

func (runner *fixtureRunner) runGo(command Command) (CommandResult, error) {
	output := argumentValue(command.Args, "-o")
	if output == "" || !containsArgument(command.Args, "macintegration") {
		return CommandResult{}, errors.New("fixture go build missing macintegration output")
	}
	if err := os.WriteFile(output, []byte("non-secret signed test executable fixture"), 0o700); err != nil {
		return CommandResult{}, err
	}
	return CommandResult{}, nil
}

func (runner *fixtureRunner) runCodesign(command Command) (CommandResult, error) {
	if containsArgument(command.Args, "--sign") {
		app := command.Args[len(command.Args)-1]
		marker := filepath.Join(app, "Contents", "_CodeSignature", "fixture-signed")
		if err := os.MkdirAll(filepath.Dir(marker), 0o700); err != nil {
			return CommandResult{}, err
		}
		if err := os.WriteFile(marker, []byte("signed by "+fixtureIdentity), 0o600); err != nil {
			return CommandResult{}, err
		}
		if err := os.WriteFile(filepath.Join(runner.workspace, "signed-artifact"), []byte(app), 0o600); err != nil {
			return CommandResult{}, err
		}
		return CommandResult{}, nil
	}
	if containsArgument(command.Args, "--verify") {
		app := command.Args[len(command.Args)-1]
		if _, err := os.Stat(filepath.Join(app, "Contents", "_CodeSignature", "fixture-signed")); err != nil {
			return CommandResult{}, err
		}
		return CommandResult{}, os.WriteFile(filepath.Join(app, "Contents", "_CodeSignature", "fixture-verified"), []byte("verified"), 0o600)
	}
	if containsArgument(command.Args, "--entitlements") {
		return CommandResult{Stdout: signedEntitlementsFixture()}, nil
	}
	if containsArgument(command.Args, "--verbose=4") {
		return CommandResult{Stderr: "Identifier=" + fixtureBundleID + "\nTeamIdentifier=" + fixtureTeamID + "\nAuthority=" + fixtureIdentity + "\n"}, nil
	}
	return CommandResult{}, errors.New("unexpected codesign fixture command")
}

func (runner *fixtureRunner) runPhase(command Command) (CommandResult, error) {
	phase := environmentValue(command.Env, "MOBILE_EGRESS_MAC_KEYCHAIN_PHASE")
	state := environmentValue(command.Env, "MOBILE_EGRESS_MAC_KEYCHAIN_STATE")
	switch phase {
	case "A":
		if err := os.WriteFile(state, []byte(`{"logicalKey":"non-secret-random-fixture","persistentReference":"01020304"}`), 0o600); err != nil {
			return CommandResult{}, err
		}
		return CommandResult{}, os.WriteFile(filepath.Join(runner.workspace, "phase-a-complete"), []byte("A"), 0o600)
	case "B":
		if runner.failVersionB {
			return CommandResult{}, errors.New("fixture version B failure")
		}
		if _, err := os.Stat(state); err != nil {
			return CommandResult{}, err
		}
		if err := os.Remove(state); err != nil {
			return CommandResult{}, err
		}
		return CommandResult{}, os.WriteFile(filepath.Join(runner.workspace, "phase-b-complete"), []byte("B"), 0o600)
	case "cleanup":
		_ = os.Remove(state)
		return CommandResult{}, os.WriteFile(filepath.Join(runner.workspace, "cleanup-complete"), []byte("cleanup"), 0o600)
	default:
		return CommandResult{}, errors.New("unexpected signed phase fixture")
	}
}

func signedEntitlementsFixture() string {
	return "<?xml version=\"1.0\"?><plist><dict>" +
		"<key>com.apple.application-identifier</key><string>" + fixtureApplicationID + "</string>" +
		"<key>com.apple.developer.team-identifier</key><string>" + fixtureTeamID + "</string>" +
		"<key>keychain-access-groups</key><array><string>" + fixtureApplicationID + "</string></array>" +
		"</dict></plist>"
}

func argumentValue(arguments []string, name string) string {
	for index := 0; index+1 < len(arguments); index++ {
		if arguments[index] == name {
			return arguments[index+1]
		}
	}
	return ""
}

func containsArgument(arguments []string, value string) bool {
	for _, argument := range arguments {
		if argument == value || strings.Contains(argument, value) {
			return true
		}
	}
	return false
}

func environmentValue(environment []string, name string) string {
	prefix := name + "="
	for _, value := range environment {
		if strings.HasPrefix(value, prefix) {
			return strings.TrimPrefix(value, prefix)
		}
	}
	return ""
}
