// Package mackeychainharness builds and runs the signed, app-bundle-hosted
// macOS Keychain continuity integration test.
package mackeychainharness

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	controllerBundleIdentifier = "com.cbjjensen.mobile-egress.controller"
	integrationExecutableName  = "mobile-egress-keychain-integration.test"
	applicationNamePrefix      = "MobileEgressKeychain"
)

var (
	developerIDApplicationOID = asn1.ObjectIdentifier{1, 2, 840, 113635, 100, 6, 1, 13}
	identityLinePattern       = regexp.MustCompile(`(?m)^[ \t]*[0-9]+\)[ \t]+([0-9A-Fa-f]{40})[ \t]+"([^"\r\n]+)"[^\r\n]*$`)
)

type signingIdentity struct {
	label             string
	teamIdentifier    string
	sha1Fingerprint   string
	sha256Fingerprint string
	certificate       *x509.Certificate
}

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
	signing, cleanup, err := prepareSigningContext(ctx, runner, config)
	if err != nil {
		return err
	}
	defer cleanup()

	applications := make(map[string]string, 2)
	for _, version := range []string{"A", "B"} {
		applicationPath, err := buildAndSignApplication(
			ctx,
			runner,
			signing.repositoryRoot,
			signing.workspace,
			signing.profilePath,
			signing.entitlementsPath,
			signing.applicationIdentifier,
			signing.teamIdentifier,
			signing.identity,
			version,
		)
		if err != nil {
			return err
		}
		applications[version] = applicationPath
	}

	statePath := filepath.Join(signing.workspace, "keychain-upgrade-state.json")
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
		_, _ = runSignedPhase(context.Background(), runner, versionBExecutable, signing.repositoryRoot, statePath, "cleanup")
	}()
	if _, err := runSignedPhase(ctx, runner, versionAExecutable, signing.repositoryRoot, statePath, "A"); err != nil {
		return fmt.Errorf("run signed version A Keychain phase: %w", err)
	}
	versionAComplete = true
	if _, err := runSignedPhase(ctx, runner, versionBExecutable, signing.repositoryRoot, statePath, "B"); err != nil {
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
	identity signingIdentity,
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
			"--sign", identity.sha1Fingerprint,
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
	if err := verifySignedEntitlements(signedEntitlementsPath, applicationIdentifier, teamIdentifier); err != nil {
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
	} {
		if !containsLine(metadataText, exactLine) {
			return "", fmt.Errorf("version %s signature metadata is missing %q", version, exactLine)
		}
	}
	certificateDirectory := filepath.Join(workspace, "signed-"+version+"-certificates")
	if err := os.MkdirAll(certificateDirectory, 0o700); err != nil {
		return "", fmt.Errorf("create version %s certificate inspection directory: %w", version, err)
	}
	signedLeafPath := filepath.Join(certificateDirectory, "codesign0")
	if err := os.Remove(signedLeafPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("clear stale version %s signing certificate: %w", version, err)
	}
	if _, err := runCommand(ctx, runner, Command{
		Name: "codesign",
		Args: []string{"--display", "--extract-certificates", applicationPath},
		Dir:  certificateDirectory,
	}); err != nil {
		return "", fmt.Errorf("extract version %s signing certificate: %w", version, err)
	}
	signedLeafDER, err := os.ReadFile(signedLeafPath)
	if err != nil {
		return "", fmt.Errorf("read version %s signing certificate: %w", version, err)
	}
	if err := verifySignedLeafCertificate(signedLeafDER, identity); err != nil {
		return "", fmt.Errorf("verify version %s signing certificate: %w", version, err)
	}
	return applicationPath, nil
}

func verifySignedEntitlements(path, applicationIdentifier, teamIdentifier string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return validateExactSignedEntitlements(data, applicationIdentifier, teamIdentifier)
}

func loadProfileCertificates(ctx context.Context, runner Runner, profilePlist string) (map[string]*x509.Certificate, error) {
	result, err := runCommand(ctx, runner, Command{
		Name: "/usr/bin/plutil",
		Args: []string{"-extract", "DeveloperCertificates", "json", "-o", "-", profilePlist},
	})
	if err != nil {
		return nil, err
	}
	var encodedCertificates []string
	if err := json.Unmarshal([]byte(result.Stdout), &encodedCertificates); err != nil {
		return nil, fmt.Errorf("decode DeveloperCertificates array: %w", err)
	}
	if len(encodedCertificates) == 0 {
		return nil, errors.New("provisioning profile contains no developer certificates")
	}
	certificates := make(map[string]*x509.Certificate, len(encodedCertificates))
	for index, encodedCertificate := range encodedCertificates {
		der, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encodedCertificate))
		if err != nil {
			return nil, fmt.Errorf("decode DeveloperCertificates[%d] DER: %w", index, err)
		}
		certificate, err := x509.ParseCertificate(der)
		if err != nil {
			return nil, fmt.Errorf("parse DeveloperCertificates[%d]: %w", index, err)
		}
		sha1Fingerprint, _ := certificateFingerprints(certificate.Raw)
		if _, duplicate := certificates[sha1Fingerprint]; duplicate {
			return nil, fmt.Errorf("provisioning profile repeats developer certificate %s", sha1Fingerprint)
		}
		certificates[sha1Fingerprint] = certificate
	}
	return certificates, nil
}

func resolveSigningIdentity(
	ctx context.Context,
	runner Runner,
	label string,
	teamIdentifier string,
	profileCertificates map[string]*x509.Certificate,
	currentTime time.Time,
) (signingIdentity, error) {
	identityResult, err := runCommand(ctx, runner, Command{
		Name: "security",
		Args: []string{"find-identity", "-v", "-p", "codesigning"},
	})
	if err != nil {
		return signingIdentity{}, fmt.Errorf("enumerate code-signing identities: %w", err)
	}
	fingerprints := identityFingerprintsForLabel(identityResult.Stdout+identityResult.Stderr, label)
	switch len(fingerprints) {
	case 0:
		return signingIdentity{}, errors.New("operator-supplied Developer ID Application identity is not available as one valid code-signing identity")
	case 1:
	default:
		return signingIdentity{}, errors.New("operator-supplied Developer ID Application identity is ambiguous; remove renewed same-label identities or supply an unambiguous keychain")
	}
	sha1Fingerprint := fingerprints[0]
	profileCertificate, authorized := profileCertificates[sha1Fingerprint]
	if !authorized {
		return signingIdentity{}, fmt.Errorf("signing identity certificate %s is not authorized by provisioning profile DeveloperCertificates", sha1Fingerprint)
	}

	certificateResult, err := runCommand(ctx, runner, Command{
		Name: "security",
		Args: []string{"find-certificate", "-a", "-c", label, "-p"},
	})
	if err != nil {
		return signingIdentity{}, fmt.Errorf("read signing identity certificate: %w", err)
	}
	installedCertificates, err := parsePEMCertificates([]byte(certificateResult.Stdout))
	if err != nil {
		return signingIdentity{}, fmt.Errorf("parse installed signing certificates: %w", err)
	}
	var selectedCertificates []*x509.Certificate
	for _, certificate := range installedCertificates {
		candidateSHA1, _ := certificateFingerprints(certificate.Raw)
		if candidateSHA1 == sha1Fingerprint {
			selectedCertificates = append(selectedCertificates, certificate)
		}
	}
	if len(selectedCertificates) != 1 {
		return signingIdentity{}, fmt.Errorf("valid signing identity %s did not resolve to exactly one installed leaf certificate", sha1Fingerprint)
	}
	certificate := selectedCertificates[0]
	if !bytes.Equal(certificate.Raw, profileCertificate.Raw) {
		return signingIdentity{}, fmt.Errorf("installed signing certificate %s does not exactly match the provisioning profile certificate", sha1Fingerprint)
	}
	if err := validateDeveloperIDApplicationCertificate(certificate, label, teamIdentifier, currentTime); err != nil {
		return signingIdentity{}, err
	}
	verifiedSHA1, verifiedSHA256 := certificateFingerprints(certificate.Raw)
	return signingIdentity{
		label:             label,
		teamIdentifier:    teamIdentifier,
		sha1Fingerprint:   verifiedSHA1,
		sha256Fingerprint: verifiedSHA256,
		certificate:       certificate,
	}, nil
}

func identityFingerprintsForLabel(output, label string) []string {
	var fingerprints []string
	for _, match := range identityLinePattern.FindAllStringSubmatch(output, -1) {
		if match[2] == label {
			fingerprints = append(fingerprints, strings.ToUpper(match[1]))
		}
	}
	return fingerprints
}

func parsePEMCertificates(data []byte) ([]*x509.Certificate, error) {
	var certificates []*x509.Certificate
	for len(bytes.TrimSpace(data)) > 0 {
		block, remainder := pem.Decode(data)
		if block == nil {
			return nil, errors.New("certificate output contains invalid PEM data")
		}
		data = remainder
		if block.Type != "CERTIFICATE" {
			return nil, fmt.Errorf("certificate output contains unexpected PEM block %q", block.Type)
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, err
		}
		certificates = append(certificates, certificate)
	}
	if len(certificates) == 0 {
		return nil, errors.New("certificate output is empty")
	}
	return certificates, nil
}

func validateDeveloperIDApplicationCertificate(certificate *x509.Certificate, label, teamIdentifier string, currentTime time.Time) error {
	if certificate.Subject.CommonName != label || !strings.HasPrefix(certificate.Subject.CommonName, "Developer ID Application: ") {
		return errors.New("signing certificate common name is not the exact supplied Developer ID Application identity")
	}
	if len(certificate.Subject.OrganizationalUnit) != 1 || certificate.Subject.OrganizationalUnit[0] != teamIdentifier {
		return errors.New("signing certificate team identifier does not exactly match the provisioning profile")
	}
	if currentTime.Before(certificate.NotBefore) || currentTime.After(certificate.NotAfter) {
		return errors.New("signing certificate is not currently valid")
	}
	if certificate.KeyUsage&x509.KeyUsageDigitalSignature == 0 {
		return errors.New("signing certificate does not permit digital signatures")
	}
	codeSigningPurpose := false
	for _, purpose := range certificate.ExtKeyUsage {
		if purpose == x509.ExtKeyUsageCodeSigning {
			codeSigningPurpose = true
			break
		}
	}
	if !codeSigningPurpose {
		return errors.New("signing certificate does not have the code-signing extended key usage")
	}
	developerIDApplicationPurpose := false
	for _, extension := range certificate.Extensions {
		if extension.Id.Equal(developerIDApplicationOID) {
			developerIDApplicationPurpose = true
			break
		}
	}
	if !developerIDApplicationPurpose {
		return errors.New("signing certificate is not a Developer ID Application certificate")
	}
	if certificate.IsCA {
		return errors.New("signing certificate must be a leaf certificate")
	}
	return nil
}

func certificateFingerprints(der []byte) (string, string) {
	sha1Digest := sha1.Sum(der)
	sha256Digest := sha256.Sum256(der)
	return strings.ToUpper(hex.EncodeToString(sha1Digest[:])), strings.ToUpper(hex.EncodeToString(sha256Digest[:]))
}

func verifySignedLeafCertificate(der []byte, identity signingIdentity) error {
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		return err
	}
	sha1Fingerprint, sha256Fingerprint := certificateFingerprints(certificate.Raw)
	if sha1Fingerprint != identity.sha1Fingerprint || sha256Fingerprint != identity.sha256Fingerprint ||
		!bytes.Equal(certificate.Raw, identity.certificate.Raw) {
		return errors.New("signed bundle leaf certificate does not match the authorized signing identity")
	}
	return validateDeveloperIDApplicationCertificate(certificate, identity.label, identity.teamIdentifier, time.Now())
}

func validateExactSignedEntitlements(data []byte, applicationIdentifier, teamIdentifier string) error {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	token, err := nextSignificantXMLToken(decoder)
	if err != nil {
		return fmt.Errorf("parse signed entitlements: %w", err)
	}
	plistStart, ok := token.(xml.StartElement)
	if !ok || plistStart.Name.Local != "plist" {
		return errors.New("signed entitlements are not an XML property list")
	}
	token, err = nextSignificantXMLToken(decoder)
	if err != nil {
		return fmt.Errorf("parse signed entitlements dictionary: %w", err)
	}
	dictionaryStart, ok := token.(xml.StartElement)
	if !ok || dictionaryStart.Name.Local != "dict" {
		return errors.New("signed entitlements property list root must be a dictionary")
	}

	expectedStrings := map[string]string{
		"com.apple.application-identifier":    applicationIdentifier,
		"com.apple.developer.team-identifier": teamIdentifier,
	}
	seen := make(map[string]bool, 3)
	for {
		token, err = nextSignificantXMLToken(decoder)
		if err != nil {
			return fmt.Errorf("parse signed entitlement key: %w", err)
		}
		if end, ok := token.(xml.EndElement); ok && end.Name.Local == "dict" {
			break
		}
		keyStart, ok := token.(xml.StartElement)
		if !ok || keyStart.Name.Local != "key" {
			return errors.New("signed entitlements dictionary contains a non-key entry")
		}
		var key string
		if err := decoder.DecodeElement(&key, &keyStart); err != nil {
			return err
		}
		if seen[key] {
			return fmt.Errorf("signed entitlements repeat %q", key)
		}
		seen[key] = true
		valueToken, err := nextSignificantXMLToken(decoder)
		if err != nil {
			return fmt.Errorf("parse signed entitlement %q: %w", key, err)
		}
		valueStart, ok := valueToken.(xml.StartElement)
		if !ok {
			return fmt.Errorf("signed entitlement %q has no value", key)
		}
		if expected, known := expectedStrings[key]; known {
			if valueStart.Name.Local != "string" {
				return fmt.Errorf("signed entitlement %q must be a string", key)
			}
			var value string
			if err := decoder.DecodeElement(&value, &valueStart); err != nil {
				return err
			}
			if value != expected {
				return fmt.Errorf("signed entitlement %q = %q, want %q", key, value, expected)
			}
			continue
		}
		if key != "keychain-access-groups" {
			return fmt.Errorf("signed entitlements unexpectedly include %q", key)
		}
		if valueStart.Name.Local != "array" {
			return errors.New("signed keychain-access-groups entitlement must be an array")
		}
		groups, err := decodeStringArray(decoder, valueStart)
		if err != nil {
			return fmt.Errorf("parse signed keychain-access-groups entitlement: %w", err)
		}
		if len(groups) != 1 || groups[0] != applicationIdentifier {
			return fmt.Errorf("signed keychain-access-groups must contain only %q", applicationIdentifier)
		}
	}
	for _, key := range []string{
		"com.apple.application-identifier",
		"com.apple.developer.team-identifier",
		"keychain-access-groups",
	} {
		if !seen[key] {
			return fmt.Errorf("signed entitlements are missing %q", key)
		}
	}
	token, err = nextSignificantXMLToken(decoder)
	if err != nil {
		return fmt.Errorf("parse signed entitlements property list end: %w", err)
	}
	if end, ok := token.(xml.EndElement); !ok || end.Name.Local != plistStart.Name.Local {
		return errors.New("signed entitlements property list has trailing content")
	}
	if token, err = nextSignificantXMLToken(decoder); !errors.Is(err, io.EOF) {
		if err != nil {
			return fmt.Errorf("parse signed entitlements trailing content: %w", err)
		}
		return fmt.Errorf("signed entitlements contain unexpected trailing token %T", token)
	}
	return nil
}

func decodeStringArray(decoder *xml.Decoder, start xml.StartElement) ([]string, error) {
	var values []string
	for {
		token, err := nextSignificantXMLToken(decoder)
		if err != nil {
			return nil, err
		}
		if end, ok := token.(xml.EndElement); ok && end.Name.Local == start.Name.Local {
			return values, nil
		}
		valueStart, ok := token.(xml.StartElement)
		if !ok || valueStart.Name.Local != "string" {
			return nil, errors.New("array contains a non-string value")
		}
		var value string
		if err := decoder.DecodeElement(&value, &valueStart); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
}

func nextSignificantXMLToken(decoder *xml.Decoder) (xml.Token, error) {
	for {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		switch value := token.(type) {
		case xml.CharData:
			if len(bytes.TrimSpace(value)) == 0 {
				continue
			}
		case xml.Comment, xml.Directive, xml.ProcInst:
			continue
		}
		return token, nil
	}
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
