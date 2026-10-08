package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunValidatesUserDMGRecordWithoutAcceptingMissingNativeChecks(t *testing.T) {
	valid := map[string]any{"schemaVersion": 1, "releaseVersion": "2.0.6", "sourceCommit": strings.Repeat("a", 40), "artifactName": "inevitable-mobile-relay-macos-2.0.6-arm64.dmg", "artifactSha256": strings.Repeat("b", 64), "architecture": "arm64", "minimumMacOS": "13.0", "appBundleId": "com.zfnf.mobile-egress.client.app", "appExecutable": "mobile-egress-client-app", "runtimeMode": "app", "binaryRuntimeMode": "app", "binaryVersion": "2.0.6", "binarySourceCommit": strings.Repeat("a", 40), "binarySha256": strings.Repeat("c", 64), "mountedBinarySha256": strings.Repeat("c", 64), "applicationIdentity": "Developer ID Application: Fixture (ABCDEFGHIJ)", "hardenedRuntime": true, "appSignature": "valid", "imageSignature": "valid", "appNotarization": "accepted", "imageNotarization": "accepted", "appStaple": "valid", "imageStaple": "valid", "checks": map[string]string{"codesign": "passed", "spctlApp": "passed", "spctlImage": "passed", "staplerApp": "passed", "staplerImage": "passed", "mountedPayload": "passed", "binarySource": "passed"}}
	path := filepath.Join(t.TempDir(), "dmg.json")
	validate := func(record map[string]any) error {
		data, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		return run([]string{"validate-client-dmg-record", path, "2.0.6", strings.Repeat("a", 40), strings.Repeat("b", 64), "Developer ID Application: Fixture (ABCDEFGHIJ)"}, &bytes.Buffer{})
	}
	if err := validate(valid); err != nil {
		t.Fatalf("valid user DMG rejected: %v", err)
	}
	for _, field := range []string{"sourceCommit", "artifactName", "artifactSha256", "architecture", "minimumMacOS", "appBundleId", "appExecutable", "runtimeMode", "binaryRuntimeMode", "binaryVersion", "binarySourceCommit", "mountedBinarySha256", "applicationIdentity", "appSignature", "imageSignature", "appNotarization", "imageNotarization", "appStaple", "imageStaple", "checks"} {
		t.Run(field, func(t *testing.T) {
			changed := make(map[string]any)
			for k, v := range valid {
				changed[k] = v
			}
			delete(changed, field)
			if validate(changed) == nil {
				t.Fatalf("DMG missing %s accepted", field)
			}
		})
	}
	valid["binaryRuntimeMode"] = "service"
	if validate(valid) == nil {
		t.Fatal("service-mode payload accepted as user app")
	}
}

func TestRunValidatesLockAndPrintsSigningPlanWithoutCredentials(t *testing.T) {
	temporary := t.TempDir()
	lockPath := filepath.Join(temporary, "toolchain.lock")
	if err := os.WriteFile(lockPath, []byte(canonicalLockFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := run([]string{"validate-lock", lockPath}, &output); err != nil {
		t.Fatal(err)
	}
	if output.String() != "go 1.26.7\nnode 24.20.0\nwails 2.14.0\n" {
		t.Fatalf("lock output = %q", output.String())
	}
	output.Reset()
	if err := run([]string{"signing-plan"}, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(output.String(), "verify-preflight\nsign-relay\nsign-app\n") || !strings.HasSuffix(output.String(), "verify-final\nwrite-record\n") {
		t.Fatalf("signing plan output = %q", output.String())
	}
}

const canonicalLockFixture = `tool|version|kind|url|sha256|bytes
go|1.26.7|darwin-arm64-tar.gz|https://go.dev/dl/go1.26.7.darwin-arm64.tar.gz|020a1e8224811be75163e920bc77e0926a1390a6aeea19bdcf23f74b9d749f6d|64772572
node|24.20.0|darwin-arm64-tar.gz|https://nodejs.org/download/release/v24.20.0/node-v24.20.0-darwin-arm64.tar.gz|40e5607e5ecb3db9192723776da2d75d966260fc74a7a9e731c1bd67dda96bc8|52813331
wails|2.14.0|go-module-zip|https://proxy.golang.org/github.com/wailsapp/wails/v2/@v/v2.14.0.zip|be2413e0c23f65305adc6c9a102c38f79be79361ba6b64c4d5e8ca87cad39b49|6633703
`
