package macosrelease

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

const canonicalLock = `tool|version|kind|url|sha256|bytes
go|1.26.7|darwin-arm64-tar.gz|https://go.dev/dl/go1.26.7.darwin-arm64.tar.gz|020a1e8224811be75163e920bc77e0926a1390a6aeea19bdcf23f74b9d749f6d|64772572
node|24.20.0|darwin-arm64-tar.gz|https://nodejs.org/download/release/v24.20.0/node-v24.20.0-darwin-arm64.tar.gz|40e5607e5ecb3db9192723776da2d75d966260fc74a7a9e731c1bd67dda96bc8|52813331
wails|2.14.0|go-module-zip|https://proxy.golang.org/github.com/wailsapp/wails/v2/@v/v2.14.0.zip|be2413e0c23f65305adc6c9a102c38f79be79361ba6b64c4d5e8ca87cad39b49|6633703
`

func TestParsePinnedToolchainAcceptsOnlyCanonicalEntries(t *testing.T) {
	entries, err := ParsePinnedToolchain(strings.NewReader(canonicalLock))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 || entries[0].Tool != "go" || entries[1].Version != "24.20.0" || entries[2].SHA256 != "be2413e0c23f65305adc6c9a102c38f79be79361ba6b64c4d5e8ca87cad39b49" {
		t.Fatalf("unexpected entries: %#v", entries)
	}

	mutations := map[string]string{
		"wrong hash":  strings.Replace(canonicalLock, "020a1e8224811be75163e920bc77e0926a1390a6aeea19bdcf23f74b9d749f6d", strings.Repeat("0", 64), 1),
		"wrong host":  strings.Replace(canonicalLock, "https://nodejs.org/", "https://example.com/", 1),
		"duplicate":   strings.Replace(canonicalLock, "wails|2.14.0", "go|2.14.0", 1),
		"extra field": strings.Replace(canonicalLock, "|64772572\n", "|64772572|extra\n", 1),
	}
	for name, input := range mutations {
		t.Run(name, func(t *testing.T) {
			if _, err := ParsePinnedToolchain(strings.NewReader(input)); err == nil {
				t.Fatal("mutated lock was accepted")
			}
		})
	}
}

func TestVerifyArtifactRejectsSizeAndHashMismatch(t *testing.T) {
	payload := []byte("verified artifact")
	if err := VerifyArtifact(bytes.NewReader(payload), 17, "2127de9293abf1503418b9f78b3d530cdd2263417064815ee46b7ecdf1215ddc"); err != nil {
		t.Fatal(err)
	}
	if err := VerifyArtifact(bytes.NewReader(payload), 16, "2127de9293abf1503418b9f78b3d530cdd2263417064815ee46b7ecdf1215ddc"); err == nil {
		t.Fatal("size mismatch was accepted")
	}
	if err := VerifyArtifact(bytes.NewReader(payload), 17, strings.Repeat("0", 64)); err == nil {
		t.Fatal("hash mismatch was accepted")
	}
}

func TestValidateBundleLayoutRequiresControllerRelayAndLaunchDaemon(t *testing.T) {
	files := []string{
		"Contents/Info.plist",
		"Contents/embedded.provisionprofile",
		"Contents/MacOS/mobile-egress-windows",
		"Contents/Resources/iconfile.icns",
		"Contents/Resources/mobile-egress-relay",
		"Contents/Library/LaunchDaemons/com.cbjjensen.mobile-egress.relay.plist",
	}
	if err := ValidateBundleLayout(files); err != nil {
		t.Fatal(err)
	}
	if err := ValidateBundleLayout(files[:len(files)-1]); err == nil {
		t.Fatal("bundle without the LaunchDaemon plist was accepted")
	}
	if err := ValidateBundleLayout(append(files, "../outside")); err == nil {
		t.Fatal("unsafe bundle path was accepted")
	}
}

func TestSigningPlanKeepsNestedCodeBeforeOuterArtifacts(t *testing.T) {
	want := []string{
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
	got := SigningPlan()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("signing plan = %v, want %v", got, want)
	}
}

func TestDecodeVerificationRecordIsStrictAndChecksReleaseFacts(t *testing.T) {
	record := VerificationRecord{
		SchemaVersion:        1,
		ReleaseVersion:       "1.1.0",
		SourceCommit:         strings.Repeat("b", 40),
		NodeManifestSHA256:   strings.Repeat("c", 64),
		ArtifactName:         "mobile-egress-macos-1.1.0-arm64.pkg",
		ArtifactSHA256:       strings.Repeat("a", 64),
		Architecture:         "arm64",
		MinimumMacOS:         "13.0",
		ControllerBundleID:   "com.cbjjensen.mobile-egress.controller",
		RelayBundleID:        "com.cbjjensen.mobile-egress.relay",
		ApplicationIdentity:  "Developer ID Application: Example (ABCDEFGHIJ)",
		InstallerIdentity:    "Developer ID Installer: Example (ABCDEFGHIJ)",
		HardenedRuntime:      true,
		AppSandbox:           false,
		BundleLayout:         RequiredBundleLayout(),
		NestedRelaySignature: "valid",
		AppSignature:         "valid",
		PackageSignature:     "valid",
		Notarization:         "accepted",
		Staple:               "valid",
		Checks: VerificationChecks{
			Codesign: "passed",
			Pkgutil:  "passed",
			Spctl:    "passed",
			Stapler:  "passed",
		},
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeVerificationRecord(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	if err := decoded.Validate(VerificationExpectations{
		ReleaseVersion:      "1.1.0",
		SourceCommit:        strings.Repeat("b", 40),
		NodeManifestSHA256:  strings.Repeat("c", 64),
		ArtifactSHA256:      strings.Repeat("a", 64),
		ApplicationIdentity: record.ApplicationIdentity,
		InstallerIdentity:   record.InstallerIdentity,
	}); err != nil {
		t.Fatal(err)
	}

	withUnknown := append(append([]byte(nil), encoded[:len(encoded)-1]...), []byte(`,"credential":"secret"}`)...)
	if _, err := DecodeVerificationRecord(bytes.NewReader(withUnknown)); err == nil {
		t.Fatal("unknown verification-record field was accepted")
	}
	var missingSandbox map[string]any
	if err := json.Unmarshal(encoded, &missingSandbox); err != nil {
		t.Fatal(err)
	}
	delete(missingSandbox, "appSandbox")
	withoutSandbox, err := json.Marshal(missingSandbox)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeVerificationRecord(bytes.NewReader(withoutSandbox)); err == nil {
		t.Fatal("verification record missing appSandbox was accepted as false")
	}

	if err := decoded.Validate(VerificationExpectations{
		ReleaseVersion:      "1.1.0",
		SourceCommit:        strings.Repeat("d", 40),
		NodeManifestSHA256:  strings.Repeat("c", 64),
		ArtifactSHA256:      strings.Repeat("a", 64),
		ApplicationIdentity: record.ApplicationIdentity,
		InstallerIdentity:   record.InstallerIdentity,
	}); err == nil {
		t.Fatal("verification record was accepted for a different source commit")
	}
	if err := decoded.Validate(VerificationExpectations{
		ReleaseVersion:      "1.1.0",
		SourceCommit:        strings.Repeat("b", 40),
		NodeManifestSHA256:  strings.Repeat("d", 64),
		ArtifactSHA256:      strings.Repeat("a", 64),
		ApplicationIdentity: record.ApplicationIdentity,
		InstallerIdentity:   record.InstallerIdentity,
	}); err == nil {
		t.Fatal("verification record was accepted for a different node manifest")
	}

	record.Notarization = "pending"
	encoded, err = json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err = DecodeVerificationRecord(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	if err := decoded.Validate(VerificationExpectations{
		ReleaseVersion:      "1.1.0",
		SourceCommit:        strings.Repeat("b", 40),
		NodeManifestSHA256:  strings.Repeat("c", 64),
		ArtifactSHA256:      strings.Repeat("a", 64),
		ApplicationIdentity: record.ApplicationIdentity,
		InstallerIdentity:   record.InstallerIdentity,
	}); err == nil {
		t.Fatal("pending notarization was accepted")
	}
}
