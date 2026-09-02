//go:build darwin

package macosrelease

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBootstrapMacOSToolchainValidatesWailsVersion(t *testing.T) {
	tests := []struct {
		name       string
		wails      string
		wantErr    bool
		wantOutput string
	}{
		{
			name: "accepts informational footer",
			wails: `#!/bin/sh
printf '%s\n' 'v2.14.0'
printf '%s\n' 'If Wails is useful to you or your company, please consider sponsoring the project:'
printf '%s\n' 'https://github.com/sponsors/leaanthony'
			`,
			wantOutput: "Pinned macOS toolchain is ready",
		},
		{
			name: "rejects wrong first line with footer",
			wails: `#!/bin/sh
printf '%s\n' 'v2.13.0'
printf '%s\n' 'If Wails is useful to you or your company, please consider sponsoring the project:'
			`,
			wantErr:    true,
			wantOutput: "installed Wails does not match the lock",
		},
		{
			name: "rejects nonzero command after expected version",
			wails: `#!/bin/sh
printf '%s\n' 'v2.14.0'
exit 1
			`,
			wantErr:    true,
			wantOutput: "installed Wails does not match the lock",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output, err := runBootstrapWithFakeWails(t, test.wails)
			if (err != nil) != test.wantErr {
				t.Fatalf("bootstrap error = %v, wantErr %t\n%s", err, test.wantErr, output)
			}
			if !strings.Contains(output, test.wantOutput) {
				t.Fatalf("bootstrap output does not contain %q:\n%s", test.wantOutput, output)
			}
		})
	}
}

func runBootstrapWithFakeWails(t *testing.T, wails string) (string, error) {
	t.Helper()
	buildRoot := t.TempDir()
	writeFakeTool(t, filepath.Join(buildRoot, "toolchains", "go", "1.26.7", "bin", "go"), `#!/bin/sh
printf '%s\n' 'go version go1.26.7 darwin/arm64'
`)
	writeFakeTool(t, filepath.Join(buildRoot, "toolchains", "node", "24.20.0", "bin", "node"), `#!/bin/sh
printf '%s\n' 'v24.20.0'
`)
	writeFakeTool(t, filepath.Join(buildRoot, "toolchains", "wails", "2.14.0", "bin", "wails"), wails)

	t.Setenv("MOBILE_EGRESS_MAC_BUILD_ROOT", buildRoot)
	script := filepath.Clean(filepath.Join("..", "..", "..", "scripts", "bootstrap-macos-toolchain.sh"))
	command := exec.Command("/bin/sh", script)
	output, err := command.CombinedOutput()
	return string(output), err
}

func writeFakeTool(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o700); err != nil {
		t.Fatal(err)
	}
}
