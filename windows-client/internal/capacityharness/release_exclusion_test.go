//go:build capacityharness

package capacityharness

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCapacityHarnessIsAbsentUntaggedAndDiscoverableOnlyWithExplicitTag(t *testing.T) {
	repositoryRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	goExecutable := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		goExecutable += ".exe"
	}

	untagged := runGoForExclusionTest(t, repositoryRoot, goExecutable, "list", "-e", "-f", "{{.ImportPath}}", "./...")
	for _, forbidden := range []string{
		"mobile-egress/windows-client/internal/capacityharness",
		"mobile-egress/windows-client/cmd/mobile-egress-capacity",
	} {
		if containsImportPath(untagged, forbidden) {
			t.Fatalf("untagged go list unexpectedly discovered %s", forbidden)
		}
	}
	untaggedMacHarnessFiles := runGoForExclusionTest(t, repositoryRoot, goExecutable,
		"list", "-f", "{{range .GoFiles}}{{.}}{{\"\\n\"}}{{end}}", "./windows-client/internal/mackeychainharness",
	)
	if strings.Contains(untaggedMacHarnessFiles, "signed_capacity.go") || strings.Contains(untaggedMacHarnessFiles, "exec_attached.go") {
		t.Fatal("untagged macOS Keychain harness compiled capacity signed-host sources")
	}
	taggedMacHarnessFiles := runGoForExclusionTest(t, repositoryRoot, goExecutable,
		"list", "-tags", "capacityharness", "-f", "{{range .GoFiles}}{{.}}{{\"\\n\"}}{{end}}", "./windows-client/internal/mackeychainharness",
	)
	for _, required := range []string{"signed_capacity.go", "exec_attached.go"} {
		if !strings.Contains(taggedMacHarnessFiles, required) {
			t.Fatalf("tagged macOS Keychain harness did not compile %s", required)
		}
	}

	releaseDependencies := runGoForExclusionTest(t, repositoryRoot, goExecutable,
		"list", "-deps", "-f", "{{.ImportPath}}",
		"./windows-client/cmd/mobile-egress-windows", "./windows-client/cmd/mobile-egress-client",
	)
	if strings.Contains(releaseDependencies, "capacityharness") || strings.Contains(releaseDependencies, "mobile-egress-capacity") {
		t.Fatal("normal Windows release package dependency graph contains the capacity harness")
	}

	tagged := runGoForExclusionTest(t, repositoryRoot, goExecutable,
		"list", "-tags", "capacityharness", "-f", "{{.ImportPath}}",
		"./windows-client/internal/capacityharness", "./windows-client/cmd/mobile-egress-capacity",
	)
	for _, required := range []string{
		"mobile-egress/windows-client/internal/capacityharness",
		"mobile-egress/windows-client/cmd/mobile-egress-capacity",
	} {
		if !containsImportPath(tagged, required) {
			t.Fatalf("tagged go list did not discover %s", required)
		}
	}

	darwinEnvironment := []string{"GOOS=darwin", "GOARCH=arm64", "CGO_ENABLED=1"}
	darwinUntagged := runGoForExclusionTestWithEnv(t, repositoryRoot, goExecutable, darwinEnvironment,
		"list", "-e", "-f", "{{.ImportPath}}", "./...",
	)
	if containsImportPath(darwinUntagged, "mobile-egress/windows-client/cmd/mobile-egress-capacity") {
		t.Fatal("untagged Darwin go list unexpectedly discovered the capacity command")
	}
	darwinReleaseDependencies := runGoForExclusionTestWithEnv(t, repositoryRoot, goExecutable, darwinEnvironment,
		"list", "-e", "-deps", "-f", "{{.ImportPath}}", "./windows-client",
	)
	if strings.Contains(darwinReleaseDependencies, "capacityharness") || strings.Contains(darwinReleaseDependencies, "mobile-egress-capacity") {
		t.Fatal("normal Darwin release package dependency graph contains the capacity harness")
	}
	darwinTagged := runGoForExclusionTestWithEnv(t, repositoryRoot, goExecutable, darwinEnvironment,
		"list", "-tags", "capacityharness", "-f", "{{.ImportPath}}",
		"./windows-client/internal/capacityharness", "./windows-client/cmd/mobile-egress-capacity",
	)
	for _, required := range []string{
		"mobile-egress/windows-client/internal/capacityharness",
		"mobile-egress/windows-client/cmd/mobile-egress-capacity",
	} {
		if !containsImportPath(darwinTagged, required) {
			t.Fatalf("tagged Darwin go list did not discover %s", required)
		}
	}
	darwinTaggedCommandTests := runGoForExclusionTestWithEnv(t, repositoryRoot, goExecutable, darwinEnvironment,
		"list", "-tags", "capacityharness", "-f", "{{range .TestGoFiles}}{{.}}{{\"\\n\"}}{{end}}",
		"./windows-client/cmd/mobile-egress-capacity",
	)
	for _, required := range []string{"main_test.go", "console_darwin_test.go", "input_gate_darwin_test.go"} {
		if !strings.Contains(darwinTaggedCommandTests, required) {
			t.Fatalf("tagged Darwin command tests did not include %s", required)
		}
	}
	if strings.Contains(darwinTaggedCommandTests, "console_windows_test.go") {
		t.Fatal("tagged Darwin command tests unexpectedly included Windows console tests")
	}
	darwinTaggedCommandFiles := runGoForExclusionTestWithEnv(t, repositoryRoot, goExecutable, darwinEnvironment,
		"list", "-tags", "capacityharness", "-f", "{{range .GoFiles}}{{.}}{{\"\\n\"}}{{end}}",
		"./windows-client/cmd/mobile-egress-capacity",
	)
	for _, required := range []string{"main.go", "console_darwin.go", "input_gate_darwin.go"} {
		if !strings.Contains(darwinTaggedCommandFiles, required) {
			t.Fatalf("tagged Darwin command sources did not include %s", required)
		}
	}
	for _, forbidden := range []string{"console_windows.go", "input_gate_windows.go"} {
		if strings.Contains(darwinTaggedCommandFiles, forbidden) {
			t.Fatalf("tagged Darwin command sources unexpectedly included %s", forbidden)
		}
	}

	output := filepath.Join(t.TempDir(), "mobile-egress-capacity")
	if runtime.GOOS == "windows" {
		output += ".exe"
	}
	runGoForExclusionTest(t, repositoryRoot, goExecutable,
		"build", "-tags", "capacityharness", "-o", output, "./windows-client/cmd/mobile-egress-capacity",
	)
}

func runGoForExclusionTest(t *testing.T, directory, goExecutable string, arguments ...string) string {
	t.Helper()
	return runGoForExclusionTestWithEnv(t, directory, goExecutable, nil, arguments...)
}

func runGoForExclusionTestWithEnv(t *testing.T, directory, goExecutable string, environment []string, arguments ...string) string {
	t.Helper()
	command := exec.Command(goExecutable, arguments...)
	command.Dir = directory
	command.Env = append(os.Environ(), environment...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("go %s failed: %v\n%s", strings.Join(arguments, " "), err, output)
	}
	return string(output)
}

func containsImportPath(output, importPath string) bool {
	for _, line := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == importPath {
			return true
		}
	}
	return false
}
