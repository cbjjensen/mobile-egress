//go:build darwin

package macosrelease

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Exercise the real build script with a clean fixture checkout and a fake Go
// driver producing tiny native Mach-O files. No publisher identity is used.
func TestClientMacBuildUsesIndependentStages(t *testing.T) {
	repo, commit := clientMacBuildFixture(t)
	first := runClientMacFixtureBuild(t, repo, commit, "2.0.2")
	second := runClientMacFixtureBuild(t, repo, commit, "2.0.3")
	if first == second {
		t.Fatal("successive builds reused a staging directory")
	}
	for _, stage := range []string{first, second} {
		if !strings.HasPrefix(stage, filepath.Join(repo, "windows-client", "build")+string(os.PathSeparator)) {
			t.Fatalf("stage escaped build directory: %s", stage)
		}
		if _, err := os.Stat(filepath.Join(stage, "Applications", "Inevitable Mobile Relay.app", "Contents", "MacOS", "mobile-egress-client-app")); err != nil {
			t.Fatal(err)
		}
	}
}

func TestClientMacHelpersRejectHistoricalBuildsBeforeStaging(t *testing.T) {
	repo, commit := clientMacBuildFixture(t)
	for _, script := range []string{"build-client-macos.sh", "release-client-macos.sh"} {
		for _, version := range []string{"1.2.3", "2.0.0", "2.0.1"} {
			output, err := exec.Command("/bin/sh", filepath.Join(repo, "scripts", script), "--release-version", version, "--source-commit", commit).CombinedOutput()
			if err == nil || !strings.Contains(string(output), "original historical source checkout") {
				t.Fatalf("historical %s %s did not stop before build/signing: %v %s", script, version, err, output)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(repo, "windows-client", "build")); !os.IsNotExist(err) {
		t.Fatalf("historical rejection created a build directory: %v", err)
	}
}

func TestClientMacBuildCleansOnlyNewFailedStages(t *testing.T) {
	repo, commit := clientMacBuildFixture(t)
	t.Setenv("FAKE_CLIENT_BUILD_FAIL", "1")
	command := exec.Command("/bin/sh", filepath.Join(repo, "scripts", "build-client-macos.sh"), "--release-version", "2.0.2", "--source-commit", commit)
	if output, err := command.CombinedOutput(); err == nil {
		t.Fatalf("injected failed build succeeded: %s", output)
	}
	stages, err := filepath.Glob(filepath.Join(repo, "windows-client", "build", "client-macos.*"))
	if err != nil || len(stages) != 0 {
		t.Fatalf("failed build left staging behind: %v %v", stages, err)
	}
	stage := filepath.Join(repo, "windows-client", "build", "caller-owned")
	marker := filepath.Join(stage, "keep")
	writeFakeTool(t, marker, "caller-owned")
	command = exec.Command("/bin/sh", filepath.Join(repo, "scripts", "build-client-macos.sh"), "--release-version", "2.0.2", "--source-commit", commit, "--stage-dir", stage)
	if output, err := command.CombinedOutput(); err == nil {
		t.Fatalf("existing stage accepted: %s", output)
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "caller-owned" {
		t.Fatalf("build removed caller-owned contents: %s %v", data, err)
	}
}

func TestClientMacReleaseOwnsAndCleansStagingAcrossAttempts(t *testing.T) {
	repo, _ := clientMacBuildFixture(t)
	stub := filepath.Join(t.TempDir(), "reject-signing")
	writeFakeTool(t, stub, "#!/bin/sh\nprintf 'fixture-signing-stage\\n' >&2\nexit 23\n")
	script := filepath.Join(repo, "scripts", "release-client-macos.sh")
	data, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	// Replace only the external signer in this isolated fixture. The production
	// build/release staging and cleanup logic still executes unchanged.
	modified := strings.ReplaceAll(string(data), "/usr/bin/codesign", "'"+strings.ReplaceAll(stub, "'", "'\\''")+"'")
	writeFakeTool(t, script, modified)
	commit := commitClientMacFixture(t, repo)
	key := filepath.Join(t.TempDir(), "fixture.key")
	writeFakeTool(t, key, "fixture-notary-placeholder")
	run := func(version string) ([]byte, error) {
		return exec.Command("/bin/sh", script, "--release-version", version, "--source-commit", commit, "--team-id", "ABCDEFGHIJ", "--application-identity", "Developer ID Application: Fixture (ABCDEFGHIJ)", "--installer-identity", "Developer ID Installer: Fixture (ABCDEFGHIJ)", "--notary-api-key", key).CombinedOutput()
	}
	for _, version := range []string{"2.0.2", "2.0.3"} {
		output, err := run(version)
		if err == nil || !strings.Contains(string(output), "fixture-signing-stage") {
			t.Fatalf("release did not reach injected signer: %v %s", err, output)
		}
		entries, err := os.ReadDir(filepath.Join(repo, "windows-client", "build"))
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 || entries[0].Name() != "release" {
			t.Fatalf("release left build staging: %v", entries)
		}
		entries, err = os.ReadDir(filepath.Join(repo, "windows-client", "build", "release"))
		if err != nil || len(entries) != 0 {
			t.Fatalf("release left private work directory: %v %v", entries, err)
		}
	}
	artifact := filepath.Join(repo, "windows-client", "build", "release", "inevitable-mobile-relay-macos-2.0.3-arm64.pkg")
	writeFakeTool(t, artifact, "immutable-fixture")
	output, err := run("2.0.3")
	if err == nil || !strings.Contains(string(output), "Client release output already exists") || strings.Contains(string(output), "fixture-signing-stage") {
		t.Fatalf("existing artifact gate was bypassed: %v %s", err, output)
	}
	if data, err := os.ReadFile(artifact); err != nil || string(data) != "immutable-fixture" {
		t.Fatal("existing artifact was modified")
	}
}

func runClientMacFixtureBuild(t *testing.T, repo, commit, version string, extra ...string) string {
	t.Helper()
	args := []string{filepath.Join(repo, "scripts", "build-client-macos.sh"), "--release-version", version, "--source-commit", commit}
	command := exec.Command("/bin/sh", append(args, extra...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Client build failed: %v\n%s", err, output)
	}
	return strings.TrimSpace(string(output))
}

func clientMacBuildFixture(t *testing.T) (string, string) {
	t.Helper()
	repo, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Clean(filepath.Join("..", "..", ".."))
	for _, path := range []string{"scripts/build-client-macos.sh", "scripts/release-client-macos.sh", "windows-client/macos/client/Info.plist.tmpl", "windows-client/macos/client/com.zfnf.mobile-egress.client.plist"} {
		data, err := os.ReadFile(filepath.Join(source, path))
		if err != nil {
			t.Fatal(err)
		}
		writeFakeTool(t, filepath.Join(repo, path), string(data))
	}
	writeFakeTool(t, filepath.Join(repo, "scripts", "bootstrap-macos-toolchain.sh"), "#!/bin/sh\nexit 0\n")
	writeFakeTool(t, filepath.Join(repo, "windows-client", "macos", "appicon.icns"), "fixture-icon")
	writeFakeTool(t, filepath.Join(repo, ".gitignore"), "windows-client/build/\n")
	toolRoot := t.TempDir()
	writeFakeTool(t, filepath.Join(toolRoot, "toolchains", "go", "1.26.7", "bin", "go"), `#!/bin/sh
set -eu
[ "${FAKE_CLIENT_BUILD_FAIL:-0}" = 0 ] || exit 17
VERSION='' OUTPUT=''
while [ "$#" -gt 0 ]; do
 case "$1" in
  -ldflags) VERSION=${2#-X main.version=}; shift 2;;
  -o) OUTPUT=$2; shift 2;;
  *) shift;;
 esac
done
printf '#include <stdio.h>\nint main(void){puts("%s");return 0;}\n' "$VERSION" > "$OUTPUT.c"
/usr/bin/clang -arch arm64 -mmacosx-version-min=13.0 -o "$OUTPUT" "$OUTPUT.c"
/bin/rm "$OUTPUT.c"
`)
	t.Setenv("MOBILE_EGRESS_MAC_BUILD_ROOT", toolRoot)
	if output, err := exec.Command("/usr/bin/git", "-C", repo, "init", "--quiet").CombinedOutput(); err != nil {
		t.Fatalf("fixture git init: %v %s", err, output)
	}
	return repo, commitClientMacFixture(t, repo)
}

func commitClientMacFixture(t *testing.T, repo string) string {
	t.Helper()
	for _, args := range [][]string{{"add", "."}, {"-c", "commit.gpgSign=false", "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--quiet", "-m", "fixture"}} {
		command := exec.Command("/usr/bin/git", append([]string{"-C", repo}, args...)...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("fixture git: %v %s", err, output)
		}
	}
	output, err := exec.Command("/usr/bin/git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(output))
}
