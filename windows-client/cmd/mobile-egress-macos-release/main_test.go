package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
