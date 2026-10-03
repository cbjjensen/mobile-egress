package macosrelease

import (
	"encoding/json"
	"errors"
	"io"
)

const ClientAppBundleID = "com.zfnf.mobile-egress.client.app"
const ClientDaemonBundleID = "com.zfnf.mobile-egress.client"

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
	if r.ArtifactName != "mobile-egress-client-macos-"+e.ReleaseVersion+"-arm64.pkg" || !validSHA256(e.ArtifactSHA256) || r.ArtifactSHA256 != e.ArtifactSHA256 {
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
