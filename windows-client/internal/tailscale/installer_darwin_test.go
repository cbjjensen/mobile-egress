//go:build darwin

package tailscale

import (
	"context"
	"errors"
	"os/exec"
	"reflect"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestDarwinInstallerLauncherUsesFixedIdentityAndIndependentWaiter(t *testing.T) {
	inspector := validAppleInstallerTestInspector()
	runner := &installerIdentityTestRunner{}
	guard := &installerStageTestGuard{path: "/private/fixture/Tailscale-1.100.1-macos.pkg"}
	var commandPath string
	var commandArguments []string
	var command *exec.Cmd
	factory := func(path string, arguments ...string) *exec.Cmd {
		commandPath = path
		commandArguments = append([]string(nil), arguments...)
		command = exec.Command("/usr/bin/true")
		return command
	}

	session, err := launchDarwinInstallerWithDependencies(
		context.Background(), guard, inspector, runner, factory,
	)
	if err != nil {
		t.Fatal(err)
	}
	if commandPath != "/usr/bin/open" || !reflect.DeepEqual(commandArguments, []string{
		"-W", "-a", fixedAppleInstallerPath, guard.path,
	}) {
		t.Fatalf("open invocation = %q %#v", commandPath, commandArguments)
	}
	if command == nil || !reflect.DeepEqual(command.Env, newIdentityTrustEnvironment()) {
		t.Fatalf("open environment = %#v", command.Env)
	}
	if runner.calls != 1 || runner.path != "/usr/bin/codesign" || !reflect.DeepEqual(runner.arguments, []string{
		"--verify", "--deep", "--strict", "--verbose=4", "-R", appleInstallerRequirement, fixedAppleInstallerPath,
	}) || !reflect.DeepEqual(runner.environment, newIdentityTrustEnvironment()) || runner.limit != maximumIdentityTrustOutput {
		t.Fatalf("codesign call = %d %q %#v %#v %d", runner.calls, runner.path, runner.arguments, runner.environment, runner.limit)
	}
	if guard.revalidations != 3 {
		t.Fatalf("stage revalidations = %d, want post-identity, pre-open, post-start", guard.revalidations)
	}

	result, ok := <-session.Done()
	if !ok || result != (installerWaitResult{Reason: installerTerminalNaturalZero, ExitCode: 0}) {
		t.Fatalf("Done() = %#v/%t", result, ok)
	}
	const callers = 16
	var wait sync.WaitGroup
	wait.Add(callers)
	for index := 0; index < callers; index++ {
		go func() {
			defer wait.Done()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			stop, stopErr := session.Stop(ctx)
			if stopErr != nil || stop != (installerStopResult{Quiescent: true, Terminal: installerTerminalNaturalZero}) {
				t.Errorf("Stop() = %#v, %v", stop, stopErr)
			}
		}()
	}
	wait.Wait()
}

func TestDarwinInstallerPathPolicyRejectsUntrustedComponents(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*installerPathTestInspector)
	}{
		{name: "wrong case", mutate: func(inspector *installerPathTestInspector) {
			inspector.children["/System/Library/CoreServices"] = []string{"installer.app"}
		}},
		{name: "symlink", mutate: func(inspector *installerPathTestInspector) {
			value := inspector.metadata[fixedAppleInstallerPath]
			value.mode = unix.S_IFLNK | 0o777
			inspector.metadata[fixedAppleInstallerPath] = value
		}},
		{name: "non root owner", mutate: func(inspector *installerPathTestInspector) {
			value := inspector.metadata["/System/Library"]
			value.uid = 501
			inspector.metadata["/System/Library"] = value
		}},
		{name: "group writable", mutate: func(inspector *installerPathTestInspector) {
			value := inspector.metadata["/System"]
			value.mode |= 0o020
			inspector.metadata["/System"] = value
		}},
		{name: "non directory", mutate: func(inspector *installerPathTestInspector) {
			value := inspector.metadata["/System/Library/CoreServices"]
			value.mode = unix.S_IFREG | 0o755
			inspector.metadata["/System/Library/CoreServices"] = value
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			inspector := validAppleInstallerTestInspector()
			test.mutate(inspector)
			if err := validateAppleInstallerPathWithInspector(fixedAppleInstallerPath, inspector); err == nil {
				t.Fatal("untrusted path passed validation")
			}
		})
	}
}

func TestDarwinInstallerLauncherClassifiesNoDispatchAndPostStartUncertainty(t *testing.T) {
	t.Run("direct Start failure is typed no-dispatch", func(t *testing.T) {
		guard := &installerStageTestGuard{path: "/private/fixture/Tailscale-1.100.1-macos.pkg"}
		session, err := launchDarwinInstallerWithDependencies(
			context.Background(), guard, validAppleInstallerTestInspector(), &installerIdentityTestRunner{},
			func(string, ...string) *exec.Cmd { return exec.Command("/definitely/not/a/real/open") },
		)
		var noDispatch installerNoDispatchError
		if session != nil || !errors.As(err, &noDispatch) {
			t.Fatalf("launch = %#v, %v", session, err)
		}
	})

	t.Run("post Start revalidation returns owned session", func(t *testing.T) {
		guard := &installerStageTestGuard{path: "/private/fixture/Tailscale-1.100.1-macos.pkg", failAt: 3}
		session, err := launchDarwinInstallerWithDependencies(
			context.Background(), guard, validAppleInstallerTestInspector(), &installerIdentityTestRunner{},
			func(string, ...string) *exec.Cmd {
				return exec.Command("/usr/bin/true")
			},
		)
		if session == nil || err == nil {
			t.Fatalf("post-Start launch = %#v, %v", session, err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if _, stopErr := session.Stop(ctx); stopErr != nil {
			t.Fatal(stopErr)
		}
	})
}

type installerPathTestInspector struct {
	children map[string][]string
	metadata map[string]installerDarwinPathMetadata
}

func validAppleInstallerTestInspector() *installerPathTestInspector {
	return &installerPathTestInspector{
		children: map[string][]string{
			"/":                            {"System"},
			"/System":                      {"Library"},
			"/System/Library":              {"CoreServices"},
			"/System/Library/CoreServices": {"Installer.app"},
			"/System/Library/CoreServices/Installer.app": {},
		},
		metadata: map[string]installerDarwinPathMetadata{
			"/":                            {mode: unix.S_IFDIR | 0o755},
			"/System":                      {mode: unix.S_IFDIR | 0o755},
			"/System/Library":              {mode: unix.S_IFDIR | 0o755},
			"/System/Library/CoreServices": {mode: unix.S_IFDIR | 0o755},
			"/System/Library/CoreServices/Installer.app": {mode: unix.S_IFDIR | 0o755},
		},
	}
}

func (inspector *installerPathTestInspector) ReadDirectory(path string) ([]string, error) {
	children, ok := inspector.children[path]
	if !ok {
		return nil, errors.New("missing directory")
	}
	return append([]string(nil), children...), nil
}

func (inspector *installerPathTestInspector) Lstat(path string) (installerDarwinPathMetadata, error) {
	metadata, ok := inspector.metadata[path]
	if !ok {
		return installerDarwinPathMetadata{}, errors.New("missing metadata")
	}
	return metadata, nil
}

type installerIdentityTestRunner struct {
	calls       int
	path        string
	arguments   []string
	environment []string
	limit       int
	err         error
}

func (runner *installerIdentityTestRunner) Run(_ context.Context, path string, arguments, environment []string, limit int) ([]byte, error) {
	runner.calls++
	runner.path = path
	runner.arguments = append([]string(nil), arguments...)
	runner.environment = append([]string(nil), environment...)
	runner.limit = limit
	return nil, runner.err
}

type installerStageTestGuard struct {
	path          string
	revalidations int
	failAt        int
}

func (guard *installerStageTestGuard) Path() string { return guard.path }
func (guard *installerStageTestGuard) Revalidate(context.Context) error {
	guard.revalidations++
	if guard.failAt != 0 && guard.revalidations == guard.failAt {
		return errors.New("changed stage")
	}
	return nil
}
