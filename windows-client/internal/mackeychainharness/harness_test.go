package mackeychainharness

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha1"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	runner := newFixtureRunner(t, workspace)
	runner.profileApplicationID = fixtureTeamID + ".com.cbjjensen.mobile-egress.helper"
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
	runner := newFixtureRunner(t, workspace)
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
		extractedLeaf, err := os.ReadFile(filepath.Join(workspace, "signed-"+version+"-certificates", "codesign0"))
		if err != nil {
			t.Fatalf("version %s extracted signing leaf: %v", version, err)
		}
		if !bytes.Equal(extractedLeaf, runner.profileCertificates[0]) {
			t.Fatalf("version %s extracted signing leaf does not equal the authorized profile certificate", version)
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
	runner := newFixtureRunner(t, workspace)
	runner.failVersionB = true
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

// Mutation caught: accepting a renewed same-label signing certificate that the supplied provisioning profile does not authorize.
func TestHarnessRejectsUnauthorizedRenewedCertificateWithSameLabel(t *testing.T) {
	t.Parallel()

	repository, profile, workspace := harnessFixturePaths(t)
	runner := newFixtureRunner(t, workspace)
	runner.identities = []fixtureIdentityRecord{{
		label:          fixtureIdentity,
		certificateDER: newFixtureCertificate(t, fixtureIdentity, fixtureTeamID, fixtureCertificateOptions{}),
		valid:          true,
	}}
	err := Run(context.Background(), runner, Config{
		RepositoryRoot: repository,
		ProfilePath:    profile,
		Identity:       fixtureIdentity,
		Workspace:      workspace,
	})
	if err == nil {
		t.Fatal("Run() accepted an unauthorized renewed certificate with the same display label")
	}
}

// Mutation caught: omitting --entitlements while the fixture fabricates the expected post-sign entitlement output.
func TestHarnessRejectsSigningWhenEntitlementsArgumentIsOmitted(t *testing.T) {
	t.Parallel()

	repository, profile, workspace := harnessFixturePaths(t)
	base := newFixtureRunner(t, workspace)
	runner := signingArgumentMutationRunner{Runner: base, omitEntitlements: true}
	err := Run(context.Background(), runner, Config{
		RepositoryRoot: repository,
		ProfilePath:    profile,
		Identity:       fixtureIdentity,
		Workspace:      workspace,
	})
	if err == nil {
		t.Fatal("Run() accepted a signature created without the required entitlements argument")
	}
}

// Mutation caught: routing --entitlements to a different file while the fixture fabricates the expected signed entitlements.
func TestHarnessRejectsSigningWhenEntitlementsPathIsMisdirected(t *testing.T) {
	t.Parallel()

	repository, profile, workspace := harnessFixturePaths(t)
	base := newFixtureRunner(t, workspace)
	runner := signingArgumentMutationRunner{
		Runner:           base,
		entitlementsPath: filepath.Join(workspace, "wrong.entitlements.plist"),
	}
	err := Run(context.Background(), runner, Config{
		RepositoryRoot: repository,
		ProfilePath:    profile,
		Identity:       fixtureIdentity,
		Workspace:      workspace,
	})
	if err == nil {
		t.Fatal("Run() accepted a signature created with a misdirected entitlements path")
	}
}

// Mutation caught: choosing one of multiple valid same-label identities instead of failing the operator's ambiguous selection.
func TestHarnessRejectsAmbiguousSameLabelSigningIdentities(t *testing.T) {
	t.Parallel()

	repository, profile, workspace := harnessFixturePaths(t)
	runner := newFixtureRunner(t, workspace)
	renewedCertificate := newFixtureCertificate(t, fixtureIdentity, fixtureTeamID, fixtureCertificateOptions{})
	runner.profileCertificates = append(runner.profileCertificates, renewedCertificate)
	runner.identities = append(runner.identities, fixtureIdentityRecord{
		label:          fixtureIdentity,
		certificateDER: renewedCertificate,
		valid:          true,
	})
	identityOutput, err := runner.runSecurity(Command{Args: []string{"find-identity", "-v", "-p", "codesigning"}})
	if err != nil {
		t.Fatal(err)
	}
	if fingerprints := identityFingerprintsForLabel(identityOutput.Stdout, fixtureIdentity); len(fingerprints) != 2 {
		t.Fatalf("parsed fingerprints = %v, want both same-label identities from %q", fingerprints, identityOutput.Stdout)
	}
	err = Run(context.Background(), runner, Config{
		RepositoryRoot: repository,
		ProfilePath:    profile,
		Identity:       fixtureIdentity,
		Workspace:      workspace,
	})
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("Run() error = %v, want ambiguous identity rejection", err)
	}
}

// Mutation caught: proceeding when the requested display label resolves to zero valid local code-signing identities.
func TestHarnessRejectsUnavailableSigningIdentity(t *testing.T) {
	t.Parallel()

	repository, profile, workspace := harnessFixturePaths(t)
	runner := newFixtureRunner(t, workspace)
	runner.identities = nil
	err := Run(context.Background(), runner, Config{
		RepositoryRoot: repository,
		ProfilePath:    profile,
		Identity:       fixtureIdentity,
		Workspace:      workspace,
	})
	if err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("Run() error = %v, want unavailable identity rejection", err)
	}
}

// Mutation caught: trusting find-identity output without independently rejecting an expired leaf certificate.
func TestHarnessRejectsExpiredSigningCertificate(t *testing.T) {
	t.Parallel()

	repository, profile, workspace := harnessFixturePaths(t)
	expiredCertificate := newFixtureCertificate(t, fixtureIdentity, fixtureTeamID, fixtureCertificateOptions{expired: true})
	runner := newFixtureRunner(t, workspace)
	runner.profileCertificates = [][]byte{expiredCertificate}
	runner.identities = []fixtureIdentityRecord{{label: fixtureIdentity, certificateDER: expiredCertificate, valid: true}}
	err := Run(context.Background(), runner, Config{
		RepositoryRoot: repository,
		ProfilePath:    profile,
		Identity:       fixtureIdentity,
		Workspace:      workspace,
	})
	if err == nil || !strings.Contains(err.Error(), "not currently valid") {
		t.Fatalf("Run() error = %v, want expired certificate rejection", err)
	}
}

// Mutation caught: accepting a generic or installer code-signing leaf that lacks the Developer ID Application purpose OID.
func TestHarnessRejectsWrongPurposeSigningCertificate(t *testing.T) {
	t.Parallel()

	repository, profile, workspace := harnessFixturePaths(t)
	wrongPurposeCertificate := newFixtureCertificate(t, fixtureIdentity, fixtureTeamID, fixtureCertificateOptions{wrongPurpose: true})
	runner := newFixtureRunner(t, workspace)
	runner.profileCertificates = [][]byte{wrongPurposeCertificate}
	runner.identities = []fixtureIdentityRecord{{label: fixtureIdentity, certificateDER: wrongPurposeCertificate, valid: true}}
	err := Run(context.Background(), runner, Config{
		RepositoryRoot: repository,
		ProfilePath:    profile,
		Identity:       fixtureIdentity,
		Workspace:      workspace,
	})
	if err == nil || !strings.Contains(err.Error(), "not a Developer ID Application") {
		t.Fatalf("Run() error = %v, want wrong-purpose certificate rejection", err)
	}
}

// Mutation caught: trusting the team suffix in a display label instead of the selected certificate's exact OU team identifier.
func TestHarnessRejectsSigningCertificateWithInconsistentTeam(t *testing.T) {
	t.Parallel()

	repository, profile, workspace := harnessFixturePaths(t)
	wrongTeamCertificate := newFixtureCertificate(t, fixtureIdentity, "Z9Y8X7W6V5", fixtureCertificateOptions{})
	runner := newFixtureRunner(t, workspace)
	runner.profileCertificates = [][]byte{wrongTeamCertificate}
	runner.identities = []fixtureIdentityRecord{{label: fixtureIdentity, certificateDER: wrongTeamCertificate, valid: true}}
	err := Run(context.Background(), runner, Config{
		RepositoryRoot: repository,
		ProfilePath:    profile,
		Identity:       fixtureIdentity,
		Workspace:      workspace,
	})
	if err == nil || !strings.Contains(err.Error(), "team identifier") {
		t.Fatalf("Run() error = %v, want certificate team rejection", err)
	}
}

// Mutation caught: skipping malformed entries instead of decoding every DeveloperCertificates DER value fail-closed.
func TestHarnessRejectsMalformedAdditionalProfileCertificate(t *testing.T) {
	t.Parallel()

	repository, profile, workspace := harnessFixturePaths(t)
	runner := newFixtureRunner(t, workspace)
	runner.profileCertificates = append(runner.profileCertificates, []byte("not a DER certificate"))
	err := Run(context.Background(), runner, Config{
		RepositoryRoot: repository,
		ProfilePath:    profile,
		Identity:       fixtureIdentity,
		Workspace:      workspace,
	})
	if err == nil || !strings.Contains(err.Error(), "DeveloperCertificates[1]") {
		t.Fatalf("Run() error = %v, want malformed profile certificate rejection", err)
	}
}

// Mutation caught: signing by an installed but different fingerprint and trusting matching label/team metadata afterward.
func TestHarnessRejectsWrongSignerFingerprint(t *testing.T) {
	t.Parallel()

	repository, profile, workspace := harnessFixturePaths(t)
	runner := newFixtureRunner(t, workspace)
	otherLabel := "Developer ID Application: Other Fixture Operator (" + fixtureTeamID + ")"
	otherCertificate := newFixtureCertificate(t, otherLabel, fixtureTeamID, fixtureCertificateOptions{})
	runner.identities = append(runner.identities, fixtureIdentityRecord{label: otherLabel, certificateDER: otherCertificate, valid: true})
	runner.profileCertificates = append(runner.profileCertificates, otherCertificate)
	mutatingRunner := signerFingerprintMutationRunner{
		Runner:      runner,
		fingerprint: fixtureSHA1Fingerprint(otherCertificate),
	}
	err := Run(context.Background(), mutatingRunner, Config{
		RepositoryRoot: repository,
		ProfilePath:    profile,
		Identity:       fixtureIdentity,
		Workspace:      workspace,
	})
	if err == nil {
		t.Fatal("Run() accepted a bundle signed by a different installed fingerprint")
	}
}

// Mutation caught: trusting pre-sign selection without comparing the leaf certificate extracted from the signed bundle.
func TestHarnessRejectsWrongPostSignLeafCertificate(t *testing.T) {
	t.Parallel()

	repository, profile, workspace := harnessFixturePaths(t)
	runner := newFixtureRunner(t, workspace)
	wrongLeaf := newFixtureCertificate(t, fixtureIdentity, fixtureTeamID, fixtureCertificateOptions{})
	mutatingRunner := postSignLeafMutationRunner{Runner: runner, certificateDER: wrongLeaf}
	err := Run(context.Background(), mutatingRunner, Config{
		RepositoryRoot: repository,
		ProfilePath:    profile,
		Identity:       fixtureIdentity,
		Workspace:      workspace,
	})
	if err == nil {
		t.Fatal("Run() accepted a signed bundle whose extracted leaf differs from the authorized identity")
	}
}

// Mutation caught: accepting a stale certificate artifact when post-sign leaf extraction produces no current output.
func TestHarnessRejectsMissingFreshPostSignLeafExtraction(t *testing.T) {
	t.Parallel()

	repository, profile, workspace := harnessFixturePaths(t)
	runner := newFixtureRunner(t, workspace)
	for _, version := range []string{"A", "B"} {
		directory := filepath.Join(workspace, "signed-"+version+"-certificates")
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "codesign0"), runner.profileCertificates[0], 0o600); err != nil {
			t.Fatal(err)
		}
	}
	err := Run(context.Background(), suppressCertificateExtractionRunner{Runner: runner}, Config{
		RepositoryRoot: repository,
		ProfilePath:    profile,
		Identity:       fixtureIdentity,
		Workspace:      workspace,
	})
	if err == nil {
		t.Fatal("Run() accepted stale leaf certificates when extraction produced no fresh artifact")
	}
}

// Mutation caught: accepting multiple Keychain groups or an unrelated broadened entitlement in the actual signed entitlement plist.
func TestHarnessRejectsBroadenedSignedEntitlements(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		replacement string
	}{
		{name: "extra access group", replacement: "<string>" + fixtureTeamID + ".shared</string></array>"},
		{name: "unexpected entitlement", replacement: "</array><key>com.apple.security.get-task-allow</key><true/>"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			repository, profile, workspace := harnessFixturePaths(t)
			runner := newFixtureRunner(t, workspace)
			broadened := strings.Replace(entitlementsPlist(fixtureApplicationID, fixtureTeamID), "</array>", testCase.replacement, 1)
			path := filepath.Join(workspace, "broadened.entitlements.plist")
			if err := os.WriteFile(path, []byte(broadened), 0o600); err != nil {
				t.Fatal(err)
			}
			mutatingRunner := signingArgumentMutationRunner{Runner: runner, entitlementsPath: path}
			err := Run(context.Background(), mutatingRunner, Config{
				RepositoryRoot: repository,
				ProfilePath:    profile,
				Identity:       fixtureIdentity,
				Workspace:      workspace,
			})
			if err == nil {
				t.Fatal("Run() accepted broadened signed entitlements")
			}
		})
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
	profileCertificates  [][]byte
	identities           []fixtureIdentityRecord
	signedBundles        map[string]fixtureSignedBundle
	failVersionB         bool
}

type fixtureIdentityRecord struct {
	label          string
	certificateDER []byte
	valid          bool
}

type fixtureSignedBundle struct {
	certificateDER []byte
	entitlements   []byte
}

type fixtureDecodedProfile struct {
	ApplicationIdentifier string   `json:"applicationIdentifier"`
	TeamIdentifier        string   `json:"teamIdentifier"`
	AccessGroup           string   `json:"accessGroup"`
	DeveloperCertificates []string `json:"developerCertificates"`
	GetTaskAllow          bool     `json:"getTaskAllow"`
	ProvisionsAllDevices  bool     `json:"provisionsAllDevices"`
}

type fixtureCertificateOptions struct {
	expired      bool
	wrongPurpose bool
}

func newFixtureRunner(t *testing.T, workspace string) *fixtureRunner {
	t.Helper()
	certificateDER := newFixtureCertificate(t, fixtureIdentity, fixtureTeamID, fixtureCertificateOptions{})
	return &fixtureRunner{
		workspace:            workspace,
		profileApplicationID: fixtureApplicationID,
		profileTeamID:        fixtureTeamID,
		profileAccessGroup:   fixtureTeamID + ".*",
		profileCertificates:  [][]byte{certificateDER},
		identities: []fixtureIdentityRecord{{
			label:          fixtureIdentity,
			certificateDER: certificateDER,
			valid:          true,
		}},
		signedBundles: make(map[string]fixtureSignedBundle),
	}
}

func newFixtureCertificate(t *testing.T, label, teamIdentifier string, options fixtureCertificateOptions) []byte {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	notBefore := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	notAfter := time.Date(2040, 1, 1, 0, 0, 0, 0, time.UTC)
	if options.expired {
		notAfter = time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	purposeOID := asn1.ObjectIdentifier{1, 2, 840, 113635, 100, 6, 1, 13}
	if options.wrongPurpose {
		purposeOID = asn1.ObjectIdentifier{1, 2, 840, 113635, 100, 6, 1, 14}
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName:         label,
			Organization:       []string{"Fixture Organization"},
			OrganizationalUnit: []string{teamIdentifier},
		},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
		BasicConstraintsValid: true,
		ExtraExtensions: []pkix.Extension{{
			Id:       purposeOID,
			Critical: true,
			Value:    []byte{0x05, 0x00},
		}},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

type signingArgumentMutationRunner struct {
	Runner
	omitEntitlements bool
	entitlementsPath string
}

func (runner signingArgumentMutationRunner) Run(ctx context.Context, command Command) (CommandResult, error) {
	if filepath.Base(command.Name) == "codesign" && containsArgument(command.Args, "--sign") {
		arguments := append([]string(nil), command.Args...)
		for index := 0; index+1 < len(arguments); index++ {
			if arguments[index] != "--entitlements" {
				continue
			}
			if runner.omitEntitlements {
				arguments = append(arguments[:index], arguments[index+2:]...)
			} else {
				arguments[index+1] = runner.entitlementsPath
			}
			break
		}
		command.Args = arguments
	}
	return runner.Runner.Run(ctx, command)
}

type signerFingerprintMutationRunner struct {
	Runner
	fingerprint string
}

func (runner signerFingerprintMutationRunner) Run(ctx context.Context, command Command) (CommandResult, error) {
	if filepath.Base(command.Name) == "codesign" && containsArgument(command.Args, "--sign") {
		arguments := append([]string(nil), command.Args...)
		for index := 0; index+1 < len(arguments); index++ {
			if arguments[index] == "--sign" {
				arguments[index+1] = runner.fingerprint
				break
			}
		}
		command.Args = arguments
	}
	return runner.Runner.Run(ctx, command)
}

type postSignLeafMutationRunner struct {
	Runner
	certificateDER []byte
}

func (runner postSignLeafMutationRunner) Run(ctx context.Context, command Command) (CommandResult, error) {
	result, err := runner.Runner.Run(ctx, command)
	if err == nil && filepath.Base(command.Name) == "codesign" && containsArgument(command.Args, "--extract-certificates") {
		err = os.WriteFile(filepath.Join(command.Dir, "codesign0"), runner.certificateDER, 0o600)
	}
	return result, err
}

type suppressCertificateExtractionRunner struct {
	Runner
}

func (runner suppressCertificateExtractionRunner) Run(ctx context.Context, command Command) (CommandResult, error) {
	if filepath.Base(command.Name) == "codesign" && containsArgument(command.Args, "--extract-certificates") {
		return CommandResult{}, nil
	}
	return runner.Runner.Run(ctx, command)
}

func (runner *fixtureRunner) Run(_ context.Context, command Command) (CommandResult, error) {
	if err := os.WriteFile(filepath.Join(runner.workspace, "external-tool-ran"), []byte("non-secret fixture"), 0o600); err != nil {
		return CommandResult{}, err
	}
	switch filepath.Base(command.Name) {
	case "security", "security.exe":
		return runner.runSecurity(command)
	case "plutil", "plutil.exe":
		return runner.runPlutil(command)
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
		input := argumentValue(command.Args, "-i")
		if _, err := os.Stat(input); err != nil {
			return CommandResult{}, err
		}
		encodedCertificates := make([]string, 0, len(runner.profileCertificates))
		for _, certificateDER := range runner.profileCertificates {
			encodedCertificates = append(encodedCertificates, base64.StdEncoding.EncodeToString(certificateDER))
		}
		decodedProfile, err := json.Marshal(fixtureDecodedProfile{
			ApplicationIdentifier: runner.profileApplicationID,
			TeamIdentifier:        runner.profileTeamID,
			AccessGroup:           runner.profileAccessGroup,
			DeveloperCertificates: encodedCertificates,
			GetTaskAllow:          false,
			ProvisionsAllDevices:  true,
		})
		if err != nil {
			return CommandResult{}, err
		}
		if err := os.WriteFile(output, decodedProfile, 0o600); err != nil {
			return CommandResult{}, err
		}
		return CommandResult{}, nil
	}
	if len(command.Args) >= 3 && command.Args[0] == "find-identity" {
		var output strings.Builder
		count := 0
		for _, identity := range runner.identities {
			if !identity.valid {
				continue
			}
			count++
			fmt.Fprintf(&output, "%d) %s \"%s\"\n", count, fixtureSHA1Fingerprint(identity.certificateDER), identity.label)
		}
		fmt.Fprintf(&output, "  %d valid identities found\n", count)
		return CommandResult{Stdout: output.String()}, nil
	}
	if len(command.Args) > 0 && command.Args[0] == "find-certificate" {
		label := argumentValue(command.Args, "-c")
		var output strings.Builder
		for _, identity := range runner.identities {
			if identity.label != label {
				continue
			}
			if err := pem.Encode(&output, &pem.Block{Type: "CERTIFICATE", Bytes: identity.certificateDER}); err != nil {
				return CommandResult{}, err
			}
		}
		return CommandResult{Stdout: output.String()}, nil
	}
	return CommandResult{}, errors.New("unexpected security fixture command")
}

func (runner *fixtureRunner) runPlutil(command Command) (CommandResult, error) {
	if !containsArgument(command.Args, "DeveloperCertificates") {
		return CommandResult{}, errors.New("unexpected plutil fixture command")
	}
	profile, err := readFixtureDecodedProfile(command.Args[len(command.Args)-1])
	if err != nil {
		return CommandResult{}, err
	}
	data, err := json.Marshal(profile.DeveloperCertificates)
	if err != nil {
		return CommandResult{}, err
	}
	return CommandResult{Stdout: string(data)}, nil
}

func (runner *fixtureRunner) runPlistBuddy(command Command) (CommandResult, error) {
	query := argumentValue(command.Args, "-c")
	profile, err := readFixtureDecodedProfile(command.Args[len(command.Args)-1])
	if err != nil {
		return CommandResult{}, err
	}
	switch query {
	case "Print :Entitlements:com.apple.application-identifier":
		return CommandResult{Stdout: profile.ApplicationIdentifier + "\n"}, nil
	case "Print :Entitlements:com.apple.developer.team-identifier":
		return CommandResult{Stdout: profile.TeamIdentifier + "\n"}, nil
	case "Print :Entitlements:keychain-access-groups:0":
		return CommandResult{Stdout: profile.AccessGroup + "\n"}, nil
	case "Print :Entitlements:com.apple.security.get-task-allow":
		return CommandResult{Stdout: fmt.Sprintf("%t\n", profile.GetTaskAllow)}, nil
	case "Print :ProvisionsAllDevices":
		return CommandResult{Stdout: fmt.Sprintf("%t\n", profile.ProvisionsAllDevices)}, nil
	default:
		return CommandResult{}, errors.New("unexpected PlistBuddy fixture query: " + query)
	}
}

func readFixtureDecodedProfile(path string) (fixtureDecodedProfile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return fixtureDecodedProfile{}, err
	}
	var profile fixtureDecodedProfile
	if err := json.Unmarshal(data, &profile); err != nil {
		return fixtureDecodedProfile{}, err
	}
	return profile, nil
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
		signer := argumentValue(command.Args, "--sign")
		identity, ok := runner.resolveFixtureSigner(signer)
		if !ok {
			return CommandResult{}, fmt.Errorf("fixture signer %q is unavailable", signer)
		}
		entitlementsPath := argumentValue(command.Args, "--entitlements")
		entitlements := []byte("<?xml version=\"1.0\"?><plist><dict></dict></plist>")
		if entitlementsPath != "" {
			var err error
			entitlements, err = os.ReadFile(entitlementsPath)
			if err != nil {
				return CommandResult{}, err
			}
		}
		app := command.Args[len(command.Args)-1]
		if runner.signedBundles == nil {
			runner.signedBundles = make(map[string]fixtureSignedBundle)
		}
		runner.signedBundles[app] = fixtureSignedBundle{
			certificateDER: append([]byte(nil), identity.certificateDER...),
			entitlements:   append([]byte(nil), entitlements...),
		}
		marker := filepath.Join(app, "Contents", "_CodeSignature", "fixture-signed")
		if err := os.MkdirAll(filepath.Dir(marker), 0o700); err != nil {
			return CommandResult{}, err
		}
		if err := os.WriteFile(marker, []byte("signed by "+fixtureSHA1Fingerprint(identity.certificateDER)), 0o600); err != nil {
			return CommandResult{}, err
		}
		if err := os.WriteFile(filepath.Join(runner.workspace, "signed-artifact"), []byte(app), 0o600); err != nil {
			return CommandResult{}, err
		}
		return CommandResult{}, nil
	}
	if containsArgument(command.Args, "--verify") {
		app := command.Args[len(command.Args)-1]
		if _, ok := runner.signedBundles[app]; !ok {
			return CommandResult{}, errors.New("fixture bundle is not signed")
		}
		if _, err := os.Stat(filepath.Join(app, "Contents", "_CodeSignature", "fixture-signed")); err != nil {
			return CommandResult{}, err
		}
		return CommandResult{}, os.WriteFile(filepath.Join(app, "Contents", "_CodeSignature", "fixture-verified"), []byte("verified"), 0o600)
	}
	if containsArgument(command.Args, "--extract-certificates") {
		bundle, ok := runner.signedBundleForTarget(command.Args[len(command.Args)-1])
		if !ok {
			return CommandResult{}, errors.New("fixture signed bundle certificate is unavailable")
		}
		return CommandResult{}, os.WriteFile(filepath.Join(command.Dir, "codesign0"), bundle.certificateDER, 0o600)
	}
	if containsArgument(command.Args, "--entitlements") {
		bundle, ok := runner.signedBundleForTarget(command.Args[len(command.Args)-1])
		if !ok {
			return CommandResult{}, errors.New("fixture signed bundle entitlements are unavailable")
		}
		return CommandResult{Stdout: string(bundle.entitlements)}, nil
	}
	if containsArgument(command.Args, "--verbose=4") {
		app := command.Args[len(command.Args)-1]
		bundle, ok := runner.signedBundleForTarget(app)
		if !ok {
			return CommandResult{}, errors.New("fixture signed bundle metadata is unavailable")
		}
		certificate, err := x509.ParseCertificate(bundle.certificateDER)
		if err != nil {
			return CommandResult{}, err
		}
		identifier, err := fixturePlistString(filepath.Join(app, "Contents", "Info.plist"), "CFBundleIdentifier")
		if err != nil {
			return CommandResult{}, err
		}
		teamIdentifier := ""
		if len(certificate.Subject.OrganizationalUnit) == 1 {
			teamIdentifier = certificate.Subject.OrganizationalUnit[0]
		}
		return CommandResult{Stderr: "Identifier=" + identifier + "\nTeamIdentifier=" + teamIdentifier + "\nAuthority=" + certificate.Subject.CommonName + "\n"}, nil
	}
	return CommandResult{}, errors.New("unexpected codesign fixture command")
}

func (runner *fixtureRunner) resolveFixtureSigner(reference string) (fixtureIdentityRecord, bool) {
	for _, identity := range runner.identities {
		if identity.valid && fixtureSHA1Fingerprint(identity.certificateDER) == strings.ToUpper(reference) {
			return identity, true
		}
	}
	return fixtureIdentityRecord{}, false
}

func (runner *fixtureRunner) signedBundleForTarget(target string) (fixtureSignedBundle, bool) {
	for applicationPath, bundle := range runner.signedBundles {
		if target == applicationPath || strings.HasPrefix(target, applicationPath+string(filepath.Separator)) {
			return bundle, true
		}
	}
	return fixtureSignedBundle{}, false
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

func fixtureSHA1Fingerprint(der []byte) string {
	digest := sha1.Sum(der)
	return strings.ToUpper(hex.EncodeToString(digest[:]))
}

func fixturePlistString(path, key string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	content := string(data)
	keyMarker := "<key>" + key + "</key>"
	keyIndex := strings.Index(content, keyMarker)
	if keyIndex < 0 {
		return "", fmt.Errorf("fixture plist is missing %s", key)
	}
	remaining := content[keyIndex+len(keyMarker):]
	startIndex := strings.Index(remaining, "<string>")
	endIndex := strings.Index(remaining, "</string>")
	if startIndex < 0 || endIndex < 0 || endIndex < startIndex {
		return "", fmt.Errorf("fixture plist %s is not a string", key)
	}
	return remaining[startIndex+len("<string>") : endIndex], nil
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
