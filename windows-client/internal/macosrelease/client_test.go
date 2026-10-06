package macosrelease

import (
	"strings"
	"testing"
)

func TestClientVerificationBindsArtifactSourceAndNativeIdentity(t *testing.T) {
	expected := VerificationExpectations{ReleaseVersion: "1.2.3", SourceCommit: strings.Repeat("a", 40), ArtifactSHA256: strings.Repeat("b", 64), ApplicationIdentity: "Developer ID Application: Fixture (ABCDEFGHIJ)", InstallerIdentity: "Developer ID Installer: Fixture (ABCDEFGHIJ)"}
	valid := ClientVerificationRecord{SchemaVersion: 1, ReleaseVersion: expected.ReleaseVersion, SourceCommit: expected.SourceCommit, ArtifactName: "mobile-egress-client-macos-1.2.3-arm64.pkg", ArtifactSHA256: expected.ArtifactSHA256, Architecture: "arm64", MinimumMacOS: "13.0", AppBundleID: ClientAppBundleID, DaemonBundleID: ClientDaemonBundleID, ApplicationIdentity: expected.ApplicationIdentity, InstallerIdentity: expected.InstallerIdentity, HardenedRuntime: true, AppSignature: "valid", DaemonSignature: "valid", PackageSignature: "valid", Notarization: "accepted", Staple: "valid", Checks: VerificationChecks{Codesign: "passed", Pkgutil: "passed", Spctl: "passed", Stapler: "passed"}}
	if err := valid.Validate(expected); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*ClientVerificationRecord){
		"source":             func(r *ClientVerificationRecord) { r.SourceCommit = strings.Repeat("c", 40) },
		"controller package": func(r *ClientVerificationRecord) { r.ArtifactName = "mobile-egress-macos-1.2.3-arm64.pkg" },
		"hash":               func(r *ClientVerificationRecord) { r.ArtifactSHA256 = strings.Repeat("c", 64) },
		"identity":           func(r *ClientVerificationRecord) { r.DaemonBundleID = ControllerBundleID },
		"signer":             func(r *ClientVerificationRecord) { r.ApplicationIdentity = "other" },
		"notarization":       func(r *ClientVerificationRecord) { r.Notarization = "pending" },
		"runtime":            func(r *ClientVerificationRecord) { r.HardenedRuntime = false },
		"command":            func(r *ClientVerificationRecord) { r.Checks.Spctl = "skipped" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := valid
			mutate(&changed)
			if changed.Validate(expected) == nil {
				t.Fatal("unsafe Client record accepted")
			}
		})
	}
}

func TestClientVerificationPreservesHistoricalAndBrandedArtifactContracts(t *testing.T) {
	for _, tc := range []struct{ version, name string }{
		{"2.0.0", "mobile-egress-client-macos-2.0.0-arm64.pkg"},
		{"2.0.1", "mobile-egress-client-macos-2.0.1-arm64.pkg"},
		{"2.0.2", "inevitable-mobile-relay-macos-2.0.2-arm64.pkg"},
		{"2.0.2-pilot.1", "inevitable-mobile-relay-macos-2.0.2-pilot.1-arm64.pkg"},
		{"2.1.0", "inevitable-mobile-relay-macos-2.1.0-arm64.pkg"},
		{"3.0.0", "inevitable-mobile-relay-macos-3.0.0-arm64.pkg"},
	} {
		t.Run(tc.version, func(t *testing.T) {
			e := VerificationExpectations{ReleaseVersion: tc.version, SourceCommit: strings.Repeat("a", 40), ArtifactSHA256: strings.Repeat("b", 64), ApplicationIdentity: "fixture application", InstallerIdentity: "fixture installer"}
			r := ClientVerificationRecord{SchemaVersion: 1, ReleaseVersion: tc.version, SourceCommit: e.SourceCommit, ArtifactName: tc.name, ArtifactSHA256: e.ArtifactSHA256, Architecture: Architecture, MinimumMacOS: MinimumMacOS, AppBundleID: ClientAppBundleID, DaemonBundleID: ClientDaemonBundleID, ApplicationIdentity: e.ApplicationIdentity, InstallerIdentity: e.InstallerIdentity, HardenedRuntime: true, AppSignature: "valid", DaemonSignature: "valid", PackageSignature: "valid", Notarization: "accepted", Staple: "valid", Checks: VerificationChecks{Codesign: "passed", Pkgutil: "passed", Spctl: "passed", Stapler: "passed"}}
			if err := r.Validate(e); err != nil {
				t.Fatal(err)
			}
			r.ArtifactName = "wrong-name.pkg"
			if r.Validate(e) == nil {
				t.Fatal("wrong branded/historical artifact accepted")
			}
		})
	}
}
