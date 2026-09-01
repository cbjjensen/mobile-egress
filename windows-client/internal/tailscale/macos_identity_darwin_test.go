//go:build darwin

package tailscale

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDarwinIdentityVerifierUsesExactCodesignSpctlDisplayPolicy(t *testing.T) {
	events := []string{}
	state, runner, opener := successfulIdentityVerifierDependencies(&events)
	verified, err := verifyDarwinAppWithDependencies(
		context.Background(), fixedTailscaleBundlePath, fixedTailscaleExecutablePath,
		expectedTailscaleAppRequirement, opener, runner,
	)
	if err != nil {
		t.Fatalf("verifyDarwinAppWithDependencies() error = %v", err)
	}
	defer func() {
		if closeErr := verified.Guard.Close(); closeErr != nil {
			t.Errorf("Close() error = %v", closeErr)
		}
	}()
	want := []identityRecordedCommand{
		{path: "/usr/bin/codesign", args: []string{"--verify", "--deep", "--strict", "--verbose=4", "-R", expectedTailscaleAppRequirement, fixedTailscaleBundlePath}, env: []string{"LC_ALL=C", "LANG=C", "PATH=/usr/bin:/bin:/usr/sbin:/sbin"}, limit: 4 << 20},
		{path: "/usr/sbin/spctl", args: []string{"--assess", "--type", "execute", fixedTailscaleBundlePath}, env: []string{"LC_ALL=C", "LANG=C", "PATH=/usr/bin:/bin:/usr/sbin:/sbin"}, limit: 4 << 20},
		{path: "/usr/bin/codesign", args: []string{"--display", "--verbose=4", fixedTailscaleBundlePath}, env: []string{"LC_ALL=C", "LANG=C", "PATH=/usr/bin:/bin:/usr/sbin:/sbin"}, limit: 4 << 20},
	}
	runner.mu.Lock()
	got := append([]identityRecordedCommand(nil), runner.commands...)
	runner.mu.Unlock()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("commands = %#v, want %#v", got, want)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.observeCalls != 8 {
		t.Fatalf("guard revalidations = %d, want 8", state.observeCalls)
	}
}

func TestDarwinIdentityTrustRunnerUsesNoShellExactEnvironmentAndCombinedCap(t *testing.T) {
	for key, value := range map[string]string{
		"LC_ALL": "hostile-locale", "LANG": "hostile-language",
		"DYLD_INSERT_LIBRARIES": "/tmp/hostile.dylib", "LD_PRELOAD": "/tmp/hostile.so",
		"VERSIONER_PYTHON_VERSION": "hostile", "CODESIGN_ALLOCATE": "/tmp/hostile-tool",
	} {
		t.Setenv(key, value)
	}
	var mu sync.Mutex
	var requestedPath string
	var requestedArgs []string
	var requestedCommand *exec.Cmd
	var factoryContextIsBackground bool
	factory := func(ctx context.Context, path string, args ...string) *exec.Cmd {
		mu.Lock()
		requestedPath = path
		requestedArgs = append([]string(nil), args...)
		factoryContextIsBackground = ctx == context.Background() && ctx.Done() == nil
		mu.Unlock()
		mode := args[len(args)-1]
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestIdentityDarwinTrustRunnerHelper$", "--", mode)
		mu.Lock()
		requestedCommand = command
		mu.Unlock()
		return command
	}
	runner := identityDarwinTrustRunner{newCommand: factory}
	env := []string{"LC_ALL=C", "LANG=C", "PATH=/usr/bin:/bin:/usr/sbin:/sbin"}
	output, err := runner.Run(context.Background(), "/usr/bin/codesign", []string{"--display", "emit"}, env, maximumIdentityTrustOutput)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if string(output) != "Identifier=io.tailscale.ipn.macsys\nTeamIdentifier=W5364U7YZB\n" {
		t.Fatalf("output = %q", output)
	}
	mu.Lock()
	if requestedPath != "/usr/bin/codesign" || fmt.Sprint(requestedArgs) != fmt.Sprint([]string{"--display", "emit"}) ||
		!factoryContextIsBackground || requestedCommand == nil || requestedCommand.Cancel != nil ||
		requestedCommand.WaitDelay != 0 || requestedCommand.SysProcAttr == nil ||
		!requestedCommand.SysProcAttr.Setpgid {
		t.Fatalf("factory path/args/background/command = %q %v %t %#v", requestedPath, requestedArgs, factoryContextIsBackground, requestedCommand)
	}
	mu.Unlock()

	exact, err := runner.Run(context.Background(), "/usr/bin/codesign", []string{"--display", "exact"}, env, maximumIdentityTrustOutput)
	if err != nil || len(exact) != maximumIdentityTrustOutput {
		t.Fatalf("exact cap len=%d error=%v", len(exact), err)
	}
	_, err = runner.Run(context.Background(), "/usr/bin/codesign", []string{"--display", "overflow"}, env, maximumIdentityTrustOutput)
	if !errors.Is(err, errIdentityTrustOutput) || fmt.Sprint(err) != "Tailscale application trust output limit exceeded" {
		t.Fatalf("overflow error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err = runner.Run(ctx, "/usr/bin/codesign", []string{"--display", "block"}, env, maximumIdentityTrustOutput)
	if err == nil {
		t.Fatal("timeout Run() succeeded")
	}
}

func TestDarwinIdentityVerifierBoundsDescendantHeldPipesAndJoinsGuardCleanup(t *testing.T) {
	for _, test := range []struct {
		name          string
		mode          string
		overflowBytes int
		cancel        bool
	}{
		{name: "cancellation", mode: "parent-timeout", cancel: true},
		{name: "aggregate output overflow", mode: "parent-overflow", overflowBytes: maximumIdentityTrustOutput + 1},
		{name: "nonzero leader with descendant-held pipes", mode: "parent-nonzero"},
	} {
		t.Run(test.name, func(t *testing.T) {
			pidFile := filepath.Join(t.TempDir(), "descendant.pid")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			state, _, opener := successfulIdentityVerifierDependencies(nil)
			runner := identityDarwinTrustRunner{
				newCommand: darwinDescendantCommandFactory(test.mode, pidFile, test.overflowBytes),
			}
			type verifierResult struct {
				verified verifiedDarwinApp
				err      error
			}
			result := make(chan verifierResult, 1)
			started := time.Now()
			go func() {
				verified, err := verifyDarwinAppWithDependencies(
					ctx,
					fixedTailscaleBundlePath,
					fixedTailscaleExecutablePath,
					expectedTailscaleAppRequirement,
					opener,
					runner,
				)
				result <- verifierResult{verified: verified, err: err}
			}()
			descendantPID := awaitDarwinTestPID(t, pidFile, 2*time.Second)
			if test.cancel {
				cancel()
			}
			var got verifierResult
			select {
			case got = <-result:
			case <-time.After(maximumDarwinBoundedCommandDuration()):
				t.Fatal("identity verifier did not return within the bounded command policy")
			}
			if !errors.Is(got.err, errTailscaleAppVerification) ||
				fmt.Sprint(got.err) != "Tailscale application verification failed" || got.verified.Guard != nil {
				t.Fatalf("verified=%#v error=%v, want fixed verifier rejection", got.verified, got.err)
			}
			if elapsed := time.Since(started); elapsed > maximumDarwinBoundedCommandDuration() {
				t.Fatalf("identity verifier returned after %v", elapsed)
			}
			state.mu.Lock()
			closeExecutable := state.closeExecutable
			closeBundle := state.closeBundle
			state.mu.Unlock()
			if closeExecutable != 1 || closeBundle != 1 {
				t.Fatalf("guard close executable=%d bundle=%d, want 1/1", closeExecutable, closeBundle)
			}
			assertDarwinTestProcessGone(t, descendantPID)
		})
	}
}

func TestIdentityDarwinTrustRunnerHelper(t *testing.T) {
	separator := -1
	for index, argument := range os.Args {
		if argument == "--" {
			separator = index
			break
		}
	}
	if separator < 0 || separator+1 >= len(os.Args) {
		return
	}
	mode := os.Args[separator+1]
	switch mode {
	case "emit":
		if fmt.Sprint(os.Environ()) != fmt.Sprint([]string{"LC_ALL=C", "LANG=C", "PATH=/usr/bin:/bin:/usr/sbin:/sbin"}) {
			os.Exit(19)
		}
		_, _ = fmt.Fprint(os.Stderr, "Identifier=io.tailscale.ipn.macsys\nTeamIdentifier=W5364U7YZB\n")
	case "exact":
		_, _ = fmt.Fprint(os.Stdout, strings.Repeat("x", maximumIdentityTrustOutput))
	case "overflow":
		_, _ = fmt.Fprint(os.Stdout, strings.Repeat("x", maximumIdentityTrustOutput/2))
		_, _ = fmt.Fprint(os.Stderr, strings.Repeat("y", maximumIdentityTrustOutput/2+1))
	case "block":
		for {
			time.Sleep(time.Hour)
		}
	default:
		os.Exit(20)
	}
	os.Exit(0)
}

func TestDarwinIdentityPathStateAdmitsExactFixtureAndDetectsMutations(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(t *testing.T, bundle, executable string)
	}{
		{name: "chmod", mutate: func(t *testing.T, _, executable string) {
			if err := os.Chmod(executable, 0o700); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "digest", mutate: func(t *testing.T, _, executable string) {
			if err := os.WriteFile(executable, []byte("changed executable"), 0o755); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "rename replacement", mutate: func(t *testing.T, bundle, _ string) {
			backup := bundle + ".admitted"
			if err := os.Rename(bundle, backup); err != nil {
				t.Fatal(err)
			}
			newExecutable := filepath.Join(bundle, "Contents", "MacOS", "Tailscale")
			if err := os.MkdirAll(filepath.Dir(newExecutable), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(newExecutable, []byte("replacement"), 0o755); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			bundle, executable := makeDarwinIdentityFixture(t)
			state, captured, err := openIdentityDarwinAppPathState(context.Background(), bundle, executable)
			if err != nil {
				t.Fatalf("openIdentityDarwinAppPathState() error = %v", err)
			}
			guard, err := newIdentityAppExecutionGuardFromState(context.Background(), bundle, executable, state, captured, nil)
			if err != nil {
				t.Fatalf("newIdentityAppExecutionGuardFromState() error = %v", err)
			}
			test.mutate(t, bundle, executable)
			if err := guard.Revalidate(context.Background()); !errors.Is(err, errTailscaleAppVerification) {
				t.Fatalf("Revalidate() = %v, want fixed verification error", err)
			}
			if err := guard.Close(); err != nil {
				t.Fatalf("Close() error = %v", err)
			}
		})
	}
}

func TestDarwinIdentityPathStateRejectsMissingTypeCaseSymlinkAndExecuteFailures(t *testing.T) {
	for _, test := range []struct {
		name  string
		build func(t *testing.T, root string) (string, string)
	}{
		{name: "missing app", build: func(t *testing.T, root string) (string, string) {
			bundle := filepath.Join(root, "Tailscale.app")
			return bundle, filepath.Join(bundle, "Contents", "MacOS", "Tailscale")
		}},
		{name: "missing CLI", build: func(t *testing.T, root string) (string, string) {
			bundle := filepath.Join(root, "Tailscale.app")
			executable := filepath.Join(bundle, "Contents", "MacOS", "Tailscale")
			if err := os.MkdirAll(filepath.Dir(executable), 0o755); err != nil {
				t.Fatal(err)
			}
			return bundle, executable
		}},
		{name: "app is file", build: func(t *testing.T, root string) (string, string) {
			bundle := filepath.Join(root, "Tailscale.app")
			if err := os.WriteFile(bundle, []byte("not a bundle"), 0o755); err != nil {
				t.Fatal(err)
			}
			return bundle, filepath.Join(bundle, "Contents", "MacOS", "Tailscale")
		}},
		{name: "CLI is directory", build: func(t *testing.T, root string) (string, string) {
			bundle := filepath.Join(root, "Tailscale.app")
			executable := filepath.Join(bundle, "Contents", "MacOS", "Tailscale")
			if err := os.MkdirAll(executable, 0o755); err != nil {
				t.Fatal(err)
			}
			return bundle, executable
		}},
		{name: "CLI not executable", build: func(t *testing.T, root string) (string, string) {
			bundle := filepath.Join(root, "Tailscale.app")
			executable := filepath.Join(bundle, "Contents", "MacOS", "Tailscale")
			if err := os.MkdirAll(filepath.Dir(executable), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(executable, []byte("cli"), 0o600); err != nil {
				t.Fatal(err)
			}
			return bundle, executable
		}},
		{name: "wrong case", build: func(t *testing.T, root string) (string, string) {
			bundle, executable := makeDarwinIdentityFixtureAt(t, root)
			return filepath.Join(filepath.Dir(bundle), "tailscale.app"), executable
		}},
		{name: "symlinked app", build: func(t *testing.T, root string) (string, string) {
			realBundle, _ := makeDarwinIdentityFixtureAt(t, filepath.Join(root, "real"))
			bundle := filepath.Join(root, "Tailscale.app")
			if err := os.Symlink(realBundle, bundle); err != nil {
				t.Fatal(err)
			}
			return bundle, filepath.Join(bundle, "Contents", "MacOS", "Tailscale")
		}},
		{name: "symlinked CLI", build: func(t *testing.T, root string) (string, string) {
			bundle := filepath.Join(root, "Tailscale.app")
			directory := filepath.Join(bundle, "Contents", "MacOS")
			if err := os.MkdirAll(directory, 0o755); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(root, "real-cli")
			if err := os.WriteFile(target, []byte("cli"), 0o755); err != nil {
				t.Fatal(err)
			}
			executable := filepath.Join(directory, "Tailscale")
			if err := os.Symlink(target, executable); err != nil {
				t.Fatal(err)
			}
			return bundle, executable
		}},
		{name: "symlinked intermediate directory", build: func(t *testing.T, root string) (string, string) {
			bundle := filepath.Join(root, "Tailscale.app")
			realContents := filepath.Join(root, "real-contents")
			executable := filepath.Join(realContents, "MacOS", "Tailscale")
			if err := os.MkdirAll(filepath.Dir(executable), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(executable, []byte("cli"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(bundle, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(realContents, filepath.Join(bundle, "Contents")); err != nil {
				t.Fatal(err)
			}
			return bundle, filepath.Join(bundle, "Contents", "MacOS", "Tailscale")
		}},
		{name: "hard linked CLI", build: func(t *testing.T, root string) (string, string) {
			bundle, executable := makeDarwinIdentityFixtureAt(t, root)
			if err := os.Link(executable, filepath.Join(root, "Tailscale-hardlink")); err != nil {
				t.Fatal(err)
			}
			return bundle, executable
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := canonicalDarwinTestPath(t, t.TempDir())
			bundle, executable := test.build(t, root)
			state, _, err := openIdentityDarwinAppPathState(context.Background(), bundle, executable)
			if state != nil {
				defer func() {
					_ = state.CloseExecutable()
					_ = state.CloseBundle()
				}()
			}
			if err == nil {
				t.Fatal("openIdentityDarwinAppPathState() succeeded")
			}
		})
	}
}

func makeDarwinIdentityFixture(t *testing.T) (string, string) {
	t.Helper()
	return makeDarwinIdentityFixtureAt(t, canonicalDarwinTestPath(t, t.TempDir()))
}

func makeDarwinIdentityFixtureAt(t *testing.T, root string) (string, string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(root, "Tailscale.app")
	executable := filepath.Join(bundle, "Contents", "MacOS", "Tailscale")
	if err := os.MkdirAll(filepath.Dir(executable), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("original executable"), 0o755); err != nil {
		t.Fatal(err)
	}
	return bundle, executable
}

func canonicalDarwinTestPath(t *testing.T, value string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(value)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(resolved)
}
