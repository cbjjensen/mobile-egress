package macosrelease

import (
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
)

func ClientDMGSupported(version string) bool {
	if !releaseVersionPattern.MatchString(version) {
		return false
	}
	parts := strings.Split(strings.SplitN(version, "-", 2)[0], ".")
	major, _ := strconv.Atoi(parts[0])
	minor, _ := strconv.Atoi(parts[1])
	patch, _ := strconv.Atoi(parts[2])
	return major > 2 || major == 2 && (minor > 0 || patch >= 6)
}

func ClientDMGArtifactName(version string) string {
	return "inevitable-mobile-relay-macos-" + version + "-arm64.dmg"
}

type ClientDMGChecks struct {
	Codesign       string `json:"codesign"`
	SpctlApp       string `json:"spctlApp"`
	SpctlImage     string `json:"spctlImage"`
	StaplerApp     string `json:"staplerApp"`
	StaplerImage   string `json:"staplerImage"`
	MountedPayload string `json:"mountedPayload"`
	BinarySource   string `json:"binarySource"`
}

// Private publisher evidence; never a public release asset.
type ClientDMGVerificationRecord struct {
	SchemaVersion       int             `json:"schemaVersion"`
	ReleaseVersion      string          `json:"releaseVersion"`
	SourceCommit        string          `json:"sourceCommit"`
	ArtifactName        string          `json:"artifactName"`
	ArtifactSHA256      string          `json:"artifactSha256"`
	Architecture        string          `json:"architecture"`
	MinimumMacOS        string          `json:"minimumMacOS"`
	AppBundleID         string          `json:"appBundleId"`
	AppExecutable       string          `json:"appExecutable"`
	RuntimeMode         string          `json:"runtimeMode"`
	BinaryRuntimeMode   string          `json:"binaryRuntimeMode"`
	BinaryVersion       string          `json:"binaryVersion"`
	BinarySourceCommit  string          `json:"binarySourceCommit"`
	BinarySHA256        string          `json:"binarySha256"`
	MountedBinarySHA256 string          `json:"mountedBinarySha256"`
	ApplicationIdentity string          `json:"applicationIdentity"`
	HardenedRuntime     bool            `json:"hardenedRuntime"`
	AppSignature        string          `json:"appSignature"`
	ImageSignature      string          `json:"imageSignature"`
	AppNotarization     string          `json:"appNotarization"`
	ImageNotarization   string          `json:"imageNotarization"`
	AppStaple           string          `json:"appStaple"`
	ImageStaple         string          `json:"imageStaple"`
	Checks              ClientDMGChecks `json:"checks"`
}

func DecodeClientDMGVerificationRecord(reader io.Reader) (ClientDMGVerificationRecord, error) {
	decoder := json.NewDecoder(io.LimitReader(reader, 64*1024+1))
	decoder.DisallowUnknownFields()
	var record ClientDMGVerificationRecord
	if err := decoder.Decode(&record); err != nil {
		return record, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return record, errors.New("DMG verification record has trailing or oversized data")
	}
	return record, nil
}

func (r ClientDMGVerificationRecord) Validate(e VerificationExpectations) error {
	if r.SchemaVersion != 1 || !ClientDMGSupported(e.ReleaseVersion) || r.ReleaseVersion != e.ReleaseVersion || !sourceCommitPattern.MatchString(e.SourceCommit) || r.SourceCommit != e.SourceCommit {
		return errors.New("DMG verification source/version mismatch")
	}
	if r.ArtifactName != ClientDMGArtifactName(e.ReleaseVersion) || !validSHA256(e.ArtifactSHA256) || r.ArtifactSHA256 != e.ArtifactSHA256 {
		return errors.New("DMG verification artifact mismatch")
	}
	if r.Architecture != Architecture || r.MinimumMacOS != MinimumMacOS || r.AppBundleID != ClientAppBundleID || r.AppExecutable != "mobile-egress-client-app" || r.RuntimeMode != "app" || r.BinaryRuntimeMode != "app" {
		return errors.New("DMG verification platform/runtime/identity mismatch")
	}
	if r.BinaryVersion != e.ReleaseVersion || r.BinarySourceCommit != e.SourceCommit || !validSHA256(r.BinarySHA256) || r.MountedBinarySHA256 != r.BinarySHA256 {
		return errors.New("DMG mounted executable source/version/hash mismatch")
	}
	if !strings.HasPrefix(e.ApplicationIdentity, "Developer ID Application: ") || r.ApplicationIdentity != e.ApplicationIdentity {
		return errors.New("DMG verification publisher mismatch")
	}
	if !r.HardenedRuntime || r.AppSignature != "valid" || r.ImageSignature != "valid" || r.AppNotarization != "accepted" || r.ImageNotarization != "accepted" || r.AppStaple != "valid" || r.ImageStaple != "valid" || r.Checks != (ClientDMGChecks{Codesign: "passed", SpctlApp: "passed", SpctlImage: "passed", StaplerApp: "passed", StaplerImage: "passed", MountedPayload: "passed", BinarySource: "passed"}) {
		return errors.New("DMG native release verification did not pass")
	}
	return nil
}
