package macosrelease

import (
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
)

const ClientAppBundleID = "com.zfnf.mobile-egress.client.app"
const ClientDaemonBundleID = "com.zfnf.mobile-egress.client"

// The first branded contract is 2.0.2. Older signed verification records
// continue to bind the exact published name, including prerelease versions.
func ClientArtifactName(version string) string {
	base := strings.SplitN(version, "-", 2)[0]
	parts := strings.Split(base, ".")
	branded := false
	if len(parts) == 3 {
		major, _ := strconv.Atoi(parts[0])
		minor, _ := strconv.Atoi(parts[1])
		patch, _ := strconv.Atoi(parts[2])
		branded = major > 2 || major == 2 && (minor > 0 || patch >= 2)
	}
	if branded {
		return "inevitable-mobile-relay-macos-" + version + "-arm64.pkg"
	}
	return "mobile-egress-client-macos-" + version + "-arm64.pkg"
}

type ClientVerificationRecord struct {
	SchemaVersion       int                `json:"schemaVersion"`
	ReleaseVersion      string             `json:"releaseVersion"`
	SourceCommit        string             `json:"sourceCommit"`
	ArtifactName        string             `json:"artifactName"`
	ArtifactSHA256      string             `json:"artifactSha256"`
	Architecture        string             `json:"architecture"`
	MinimumMacOS        string             `json:"minimumMacOS"`
	AppBundleID         string             `json:"appBundleId"`
	DaemonBundleID      string             `json:"daemonBundleId"`
	ApplicationIdentity string             `json:"applicationIdentity"`
	InstallerIdentity   string             `json:"installerIdentity"`
	HardenedRuntime     bool               `json:"hardenedRuntime"`
	AppSignature        string             `json:"appSignature"`
	DaemonSignature     string             `json:"daemonSignature"`
	PackageSignature    string             `json:"packageSignature"`
	Notarization        string             `json:"notarization"`
	Staple              string             `json:"staple"`
	Checks              VerificationChecks `json:"checks"`
}

func DecodeClientVerificationRecord(reader io.Reader) (ClientVerificationRecord, error) {
	decoder := json.NewDecoder(io.LimitReader(reader, 64*1024+1))
	decoder.DisallowUnknownFields()
	var record ClientVerificationRecord
	if err := decoder.Decode(&record); err != nil {
		return record, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return record, errors.New("Client verification record has trailing or oversized data")
	}
	return record, nil
}

func (r ClientVerificationRecord) Validate(e VerificationExpectations) error {
	if r.SchemaVersion != 1 || !releaseVersionPattern.MatchString(e.ReleaseVersion) || r.ReleaseVersion != e.ReleaseVersion || !sourceCommitPattern.MatchString(e.SourceCommit) || r.SourceCommit != e.SourceCommit {
		return errors.New("Client verification source/version mismatch")
	}
	if r.ArtifactName != ClientArtifactName(e.ReleaseVersion) || !validSHA256(e.ArtifactSHA256) || r.ArtifactSHA256 != e.ArtifactSHA256 {
		return errors.New("Client verification artifact mismatch")
	}
	if r.Architecture != Architecture || r.MinimumMacOS != MinimumMacOS || r.AppBundleID != ClientAppBundleID || r.DaemonBundleID != ClientDaemonBundleID {
		return errors.New("Client verification platform/identity mismatch")
	}
	if e.ApplicationIdentity == "" || e.InstallerIdentity == "" || r.ApplicationIdentity != e.ApplicationIdentity || r.InstallerIdentity != e.InstallerIdentity {
		return errors.New("Client verification publisher mismatch")
	}
	if !r.HardenedRuntime || r.AppSignature != "valid" || r.DaemonSignature != "valid" || r.PackageSignature != "valid" || r.Notarization != "accepted" || r.Staple != "valid" || r.Checks != (VerificationChecks{Codesign: "passed", Pkgutil: "passed", Spctl: "passed", Stapler: "passed"}) {
		return errors.New("Client native release verification did not pass")
	}
	return nil
}
