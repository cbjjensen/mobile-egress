package macosrelease

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"strconv"
	"strings"
)

const CanonicalToolchainLock = `tool|version|kind|url|sha256|bytes
go|1.26.7|darwin-arm64-tar.gz|https://go.dev/dl/go1.26.7.darwin-arm64.tar.gz|020a1e8224811be75163e920bc77e0926a1390a6aeea19bdcf23f74b9d749f6d|64772572
node|24.20.0|darwin-arm64-tar.gz|https://nodejs.org/download/release/v24.20.0/node-v24.20.0-darwin-arm64.tar.gz|40e5607e5ecb3db9192723776da2d75d966260fc74a7a9e731c1bd67dda96bc8|52813331
wails|2.14.0|go-module-zip|https://proxy.golang.org/github.com/wailsapp/wails/v2/@v/v2.14.0.zip|be2413e0c23f65305adc6c9a102c38f79be79361ba6b64c4d5e8ca87cad39b49|6633703
`

const (
	ControllerBundleID = "com.cbjjensen.mobile-egress.controller"
	RelayBundleID      = "com.cbjjensen.mobile-egress.relay"
	Architecture       = "arm64"
	MinimumMacOS       = "13.0"
)

var releaseVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$`)
var sourceCommitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

type ToolchainEntry struct {
	Tool    string
	Version string
	Kind    string
	URL     string
	SHA256  string
	Bytes   int64
}

// ParsePinnedToolchain deliberately accepts one byte-exact lock. A toolchain
// update is therefore a reviewed source change, not a runtime policy decision.
func ParsePinnedToolchain(reader io.Reader) ([]ToolchainEntry, error) {
	data, err := io.ReadAll(io.LimitReader(reader, 64*1024+1))
	if err != nil {
		return nil, fmt.Errorf("read toolchain lock: %w", err)
	}
	if len(data) > 64*1024 {
		return nil, errors.New("toolchain lock exceeds 64 KiB")
	}
	if string(data) != CanonicalToolchainLock {
		return nil, errors.New("toolchain lock does not match the reviewed pins")
	}

	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	entries := make([]ToolchainEntry, 0, len(lines)-1)
	for _, line := range lines[1:] {
		fields := strings.Split(line, "|")
		bytes, err := strconv.ParseInt(fields[5], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse %s size: %w", fields[0], err)
		}
		entries = append(entries, ToolchainEntry{
			Tool: fields[0], Version: fields[1], Kind: fields[2],
			URL: fields[3], SHA256: fields[4], Bytes: bytes,
		})
	}
	return entries, nil
}

func VerifyArtifact(reader io.Reader, expectedBytes int64, expectedSHA256 string) error {
	if expectedBytes < 0 || !validSHA256(expectedSHA256) {
		return errors.New("invalid artifact expectation")
	}
	hash := sha256.New()
	written, err := io.Copy(hash, io.LimitReader(reader, expectedBytes+1))
	if err != nil {
		return fmt.Errorf("hash artifact: %w", err)
	}
	if written != expectedBytes {
		return fmt.Errorf("artifact size is %d, expected %d", written, expectedBytes)
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if actual != expectedSHA256 {
		return fmt.Errorf("artifact SHA-256 is %s, expected %s", actual, expectedSHA256)
	}
	return nil
}

var requiredBundleLayout = []string{
	"Contents/Info.plist",
	"Contents/embedded.provisionprofile",
	"Contents/MacOS/mobile-egress-windows",
	"Contents/Resources/iconfile.icns",
	"Contents/Resources/mobile-egress-relay",
	"Contents/Library/LaunchDaemons/com.cbjjensen.mobile-egress.relay.plist",
}

func RequiredBundleLayout() []string { return append([]string(nil), requiredBundleLayout...) }

func ValidateBundleLayout(files []string) error {
	present := make(map[string]bool, len(files))
	for _, file := range files {
		if file == "" || strings.Contains(file, "\\") || path.IsAbs(file) || path.Clean(file) != file || strings.HasPrefix(file, "../") || file == ".." {
			return fmt.Errorf("unsafe bundle path %q", file)
		}
		if present[file] {
			return fmt.Errorf("duplicate bundle path %q", file)
		}
		present[file] = true
	}
	for _, required := range requiredBundleLayout {
		if !present[required] {
			return fmt.Errorf("required bundle path is missing: %s", required)
		}
	}
	return nil
}

var signingPlan = []string{
	"verify-preflight",
	"sign-relay",
	"sign-app",
	"build-component-pkg",
	"sign-pkg",
	"notarize-pkg",
	"staple-pkg",
	"verify-final",
	"write-record",
}

func SigningPlan() []string { return append([]string(nil), signingPlan...) }

type VerificationChecks struct {
	Codesign string `json:"codesign"`
	Pkgutil  string `json:"pkgutil"`
	Spctl    string `json:"spctl"`
	Stapler  string `json:"stapler"`
}

type VerificationRecord struct {
	SchemaVersion        int                `json:"schemaVersion"`
	ReleaseVersion       string             `json:"releaseVersion"`
	SourceCommit         string             `json:"sourceCommit"`
	NodeManifestSHA256   string             `json:"nodeManifestSha256"`
	ArtifactName         string             `json:"artifactName"`
	ArtifactSHA256       string             `json:"artifactSha256"`
	Architecture         string             `json:"architecture"`
	MinimumMacOS         string             `json:"minimumMacOS"`
	ControllerBundleID   string             `json:"controllerBundleId"`
	RelayBundleID        string             `json:"relayBundleId"`
	ApplicationIdentity  string             `json:"applicationIdentity"`
	InstallerIdentity    string             `json:"installerIdentity"`
	HardenedRuntime      bool               `json:"hardenedRuntime"`
	AppSandbox           bool               `json:"appSandbox"`
	BundleLayout         []string           `json:"bundleLayout"`
	NestedRelaySignature string             `json:"nestedRelaySignature"`
	AppSignature         string             `json:"appSignature"`
	PackageSignature     string             `json:"packageSignature"`
	Notarization         string             `json:"notarization"`
	Staple               string             `json:"staple"`
	Checks               VerificationChecks `json:"checks"`
}

type VerificationExpectations struct {
	ReleaseVersion      string
	SourceCommit        string
	NodeManifestSHA256  string
	ArtifactSHA256      string
	ApplicationIdentity string
	InstallerIdentity   string
}

func DecodeVerificationRecord(reader io.Reader) (VerificationRecord, error) {
	data, err := io.ReadAll(io.LimitReader(reader, 64*1024+1))
	if err != nil {
		return VerificationRecord{}, fmt.Errorf("read verification record: %w", err)
	}
	if len(data) > 64*1024 {
		return VerificationRecord{}, errors.New("verification record exceeds 64 KiB")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return VerificationRecord{}, fmt.Errorf("decode verification record: %w", err)
	}
	requiredFields := []string{
		"schemaVersion", "releaseVersion", "sourceCommit", "nodeManifestSha256", "artifactName", "artifactSha256", "architecture", "minimumMacOS",
		"controllerBundleId", "relayBundleId", "applicationIdentity", "installerIdentity", "hardenedRuntime",
		"appSandbox", "bundleLayout", "nestedRelaySignature", "appSignature", "packageSignature", "notarization",
		"staple", "checks",
	}
	if len(fields) != len(requiredFields) {
		return VerificationRecord{}, errors.New("verification record fields are incomplete or unknown")
	}
	for _, field := range requiredFields {
		if _, ok := fields[field]; !ok {
			return VerificationRecord{}, fmt.Errorf("verification record field is missing: %s", field)
		}
	}

	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	var record VerificationRecord
	if err := decoder.Decode(&record); err != nil {
		return VerificationRecord{}, fmt.Errorf("decode verification record: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("additional JSON value")
		}
		return VerificationRecord{}, fmt.Errorf("decode verification record: %w", err)
	}
	return record, nil
}

func (record VerificationRecord) Validate(expected VerificationExpectations) error {
	if record.SchemaVersion != 1 {
		return errors.New("verification record schemaVersion must be 1")
	}
	if !releaseVersionPattern.MatchString(expected.ReleaseVersion) || record.ReleaseVersion != expected.ReleaseVersion {
		return errors.New("verification record release version mismatch")
	}
	if !sourceCommitPattern.MatchString(expected.SourceCommit) || record.SourceCommit != expected.SourceCommit {
		return errors.New("verification record source commit mismatch")
	}
	if !validSHA256(expected.NodeManifestSHA256) || record.NodeManifestSHA256 != expected.NodeManifestSHA256 {
		return errors.New("verification record node manifest SHA-256 mismatch")
	}
	if record.ArtifactName != "mobile-egress-macos-"+expected.ReleaseVersion+"-arm64.pkg" {
		return errors.New("verification record artifact name mismatch")
	}
	if !validSHA256(expected.ArtifactSHA256) || record.ArtifactSHA256 != expected.ArtifactSHA256 {
		return errors.New("verification record artifact SHA-256 mismatch")
	}
	if record.Architecture != Architecture || record.MinimumMacOS != MinimumMacOS || record.ControllerBundleID != ControllerBundleID || record.RelayBundleID != RelayBundleID {
		return errors.New("verification record platform facts mismatch")
	}
	if expected.ApplicationIdentity == "" || expected.InstallerIdentity == "" || record.ApplicationIdentity != expected.ApplicationIdentity || record.InstallerIdentity != expected.InstallerIdentity {
		return errors.New("verification record signing identity mismatch")
	}
	if !record.HardenedRuntime || record.AppSandbox {
		return errors.New("verification record security options mismatch")
	}
	if strings.Join(record.BundleLayout, "\n") != strings.Join(requiredBundleLayout, "\n") {
		return errors.New("verification record bundle layout mismatch")
	}
	if record.NestedRelaySignature != "valid" || record.AppSignature != "valid" || record.PackageSignature != "valid" || record.Notarization != "accepted" || record.Staple != "valid" {
		return errors.New("verification record release gate did not pass")
	}
	if record.Checks != (VerificationChecks{Codesign: "passed", Pkgutil: "passed", Spctl: "passed", Stapler: "passed"}) {
		return errors.New("verification record command checks did not pass")
	}
	return nil
}

func validSHA256(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
