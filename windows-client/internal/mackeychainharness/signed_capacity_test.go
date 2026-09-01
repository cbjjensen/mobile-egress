//go:build capacityharness

package mackeychainharness

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Mutation caught: running capacity in the unsigned launcher, exporting the
// Owner identity, or copying the secret document into argv/environment/temp
// state instead of attaching the original stdin to the signed process.
func TestSignedCapacityHostBuildsVerifiedBundleAndAttachesOnlySecretStdin(t *testing.T) {
	t.Parallel()

	repository, profile, workspace := harnessFixturePaths(t)
	secretInput := bytes.NewBufferString("SECRET-STDIN-DOCUMENT")
	runner := newCapacityFixtureRunner(t, workspace, secretInput)
	var stdout, stderr bytes.Buffer
	err := RunSignedCapacity(context.Background(), runner, SignedCapacityConfig{
		Signing: Config{
			RepositoryRoot: repository,
			ProfilePath:    profile,
			Identity:       fixtureIdentity,
			Workspace:      workspace,
		},
		Stdin:               secretInput,
		Stdout:              &stdout,
		Stderr:              &stderr,
		PrepareInputHandoff: runner.prepareInputHandoff,
	})
	if err != nil {
		t.Fatal(err)
	}

	if runner.attached.Stdin != secretInput {
		t.Fatal("signed capacity host did not attach the original secret stdin reader")
	}
	if runner.prepareCalls != 1 || !runner.stdinReadAfterReady {
		t.Fatalf("protected handoff calls=%d stdin-after-ready=%t", runner.prepareCalls, runner.stdinReadAfterReady)
	}
	if len(runner.attached.ExtraFiles) != 1 || runner.attached.OnStarted == nil {
		t.Fatal("signed capacity host did not install its fixed child-start input gate")
	}
	if got := strings.Join(runner.attached.Args, " "); got != "run" {
		t.Fatalf("signed capacity argv = %q, want fixed run", got)
	}
	if len(runner.attached.Env) != 0 {
		t.Fatalf("signed capacity command-specific environment = %#v, want empty", runner.attached.Env)
	}
	if runner.attached.GracePeriod < 2*time.Minute {
		t.Fatalf("signed capacity cleanup grace = %v, want at least two minutes", runner.attached.GracePeriod)
	}
	if got := filepath.ToSlash(runner.attached.Name); !strings.HasSuffix(got, "/MobileEgressCapacity.app/Contents/MacOS/mobile-egress-capacity") {
		t.Fatalf("signed capacity executable = %q", got)
	}
	if runner.attached.Dir != repository {
		t.Fatalf("signed capacity working directory = %q, want %q", runner.attached.Dir, repository)
	}

	app := filepath.Join(workspace, "MobileEgressCapacity.app")
	if got := mustReadFile(t, filepath.Join(app, "Contents", "embedded.provisionprofile")); got != "non-secret-profile-fixture" {
		t.Fatalf("embedded profile = %q", got)
	}
	info := mustReadFile(t, filepath.Join(app, "Contents", "Info.plist"))
	if !strings.Contains(info, "<string>"+fixtureBundleID+"</string>") || !strings.Contains(info, "<string>mobile-egress-capacity</string>") {
		t.Fatalf("capacity Info.plist does not bind the production controller identity: %s", info)
	}
	if _, err := os.Stat(filepath.Join(app, "Contents", "_CodeSignature", "fixture-verified")); err != nil {
		t.Fatalf("capacity signature verification artifact: %v", err)
	}
	if !runner.capacityBuild || !runner.capacityBuildTagged {
		t.Fatalf("capacity build observed=%t tagged=%t", runner.capacityBuild, runner.capacityBuildTagged)
	}
	if strings.Contains(strings.Join(runner.attached.Args, " ")+strings.Join(runner.attached.Env, " ")+stdout.String()+stderr.String(), "SECRET-STDIN-DOCUMENT") {
		t.Fatal("signed capacity host disclosed stdin secret outside the attached reader")
	}
	if stdout.String() != capacityReadinessFixture+"{\"phase\":\"complete\",\"attempted\":266,\"open\":257,\"verified\":257,\"closed\":257,\"failure\":\"none\"}\n" || stderr.Len() != 0 {
		t.Fatalf("filtered output stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

// Mutation caught: streaming arbitrary child output directly to the operator,
// which could expose a raw dependency error or injected secret field.
func TestSignedCapacityHostRejectsNonAllowlistedChildOutputWithoutForwardingIt(t *testing.T) {
	t.Parallel()

	repository, profile, workspace := harnessFixturePaths(t)
	secretInput := bytes.NewBufferString("SECRET-STDIN-DOCUMENT")
	runner := newCapacityFixtureRunner(t, workspace, secretInput)
	runner.attachedStdout = "{\"phase\":\"complete\",\"attempted\":266,\"open\":257,\"verified\":257,\"closed\":257,\"failure\":\"none\",\"detail\":\"SECRET-CHILD-OUTPUT\"}\n"
	var stdout, stderr bytes.Buffer
	err := RunSignedCapacity(context.Background(), runner, SignedCapacityConfig{
		Signing: Config{RepositoryRoot: repository, ProfilePath: profile, Identity: fixtureIdentity, Workspace: workspace},
		Stdin:   secretInput, Stdout: &stdout, Stderr: &stderr,
		PrepareInputHandoff: runner.prepareInputHandoff,
	})
	if err == nil {
		t.Fatal("RunSignedCapacity() accepted non-allowlisted child output")
	}
	if stdout.String() != capacityReadinessFixture || stderr.Len() != 0 || strings.Contains(stdout.String()+stderr.String(), "SECRET-CHILD-OUTPUT") {
		t.Fatalf("non-allowlisted child output escaped: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

// Mutation caught: passing an unbounded line or partial trailing JSON through
// the signed-host output boundary.
func TestCapacityEventStreamIsLineBoundedAndRequiresCompleteJSON(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		input string
	}{
		{name: "oversized", input: strings.Repeat("x", 513) + "\n"},
		{name: "partial", input: "{\"phase\":\"complete\"}"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var destination bytes.Buffer
			stream := newCapacityEventStream(&destination)
			_, writeErr := io.WriteString(stream, test.input)
			closeErr := stream.Close()
			if writeErr == nil && closeErr == nil {
				t.Fatal("capacity event stream accepted invalid bounded input")
			}
			if destination.Len() != 0 {
				t.Fatalf("capacity event stream forwarded invalid input %q", destination.String())
			}
		})
	}
}

// Mutation caught: killing immediately on cancellation instead of first
// signaling the capacity process and allowing its bounded cleanup to finish.
func TestAttachedProcessCancellationWaitsForGracefulExitBeforeForce(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	process := newFixtureAttachedProcess()
	done := make(chan error, 1)
	go func() { done <- waitForAttachedProcess(ctx, process, time.Second) }()
	cancel()
	select {
	case <-process.signaled:
	case <-time.After(time.Second):
		t.Fatal("attached process was not signaled")
	}
	process.finish(nil)
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("waitForAttachedProcess() error = %v, want canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("attached process did not finish after graceful exit")
	}
	if process.wasKilled() {
		t.Fatal("attached process was forced despite exiting during cleanup grace")
	}
}

// Mutation caught: waiting forever after graceful signaling instead of forcing
// a stuck child only after the configured cleanup grace expires.
func TestAttachedProcessCancellationForcesOnlyAfterGraceExpires(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	process := newFixtureAttachedProcess()
	done := make(chan error, 1)
	go func() { done <- waitForAttachedProcess(ctx, process, 20*time.Millisecond) }()
	cancel()
	select {
	case <-process.killed:
	case <-time.After(time.Second):
		t.Fatal("stuck attached process was not forced after grace")
	}
	process.finish(errors.New("fixture forced exit"))
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("waitForAttachedProcess() error = %v, want canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("forced attached process did not finish")
	}
}

func TestAttachedStartHookFailureStopsChildBeforeReturning(t *testing.T) {
	t.Parallel()

	process := newFixtureAttachedProcess()
	hookErr := errors.New("fixture protected handoff failure")
	done := make(chan error, 1)
	go func() {
		done <- runStartedAttachedProcess(context.Background(), process, time.Second, func() error {
			return hookErr
		})
	}()
	select {
	case <-process.signaled:
	case <-time.After(time.Second):
		t.Fatal("child was not signaled after the start hook failed")
	}
	process.finish(nil)
	select {
	case err := <-done:
		if !errors.Is(err, hookErr) {
			t.Fatalf("runStartedAttachedProcess() = %v, want hook failure", err)
		}
	case <-time.After(time.Second):
		t.Fatal("start-hook failure returned before child cleanup finished")
	}
	if process.wasKilled() {
		t.Fatal("child was killed despite gracefully exiting after handoff failure")
	}
}

type capacityFixtureRunner struct {
	*fixtureRunner
	expectedStdin       io.Reader
	attached            AttachedCommand
	attachedStdout      string
	capacityBuild       bool
	capacityBuildTagged bool
	childStarted        bool
	prepareCalls        int
	stdinReadAfterReady bool
}

const capacityReadinessFixture = "{\"phase\":\"input\",\"attempted\":0,\"open\":0,\"verified\":0,\"closed\":0,\"failure\":\"none\"}\n"

func newCapacityFixtureRunner(t *testing.T, workspace string, stdin io.Reader) *capacityFixtureRunner {
	t.Helper()
	return &capacityFixtureRunner{
		fixtureRunner:  newFixtureRunner(t, workspace),
		expectedStdin:  stdin,
		attachedStdout: "{\"phase\":\"complete\",\"attempted\":266,\"open\":257,\"verified\":257,\"closed\":257,\"failure\":\"none\"}\n",
	}
}

func (runner *capacityFixtureRunner) Run(ctx context.Context, command Command) (CommandResult, error) {
	if filepath.Base(command.Name) == "go" && len(command.Args) > 0 && command.Args[0] == "build" {
		runner.capacityBuild = true
		runner.capacityBuildTagged = argumentValue(command.Args, "-tags") == "capacityharness"
		output := argumentValue(command.Args, "-o")
		if output == "" {
			return CommandResult{}, errors.New("fixture capacity build is missing output")
		}
		if err := os.WriteFile(output, []byte("non-secret capacity executable fixture"), 0o700); err != nil {
			return CommandResult{}, err
		}
		return CommandResult{}, nil
	}
	return runner.fixtureRunner.Run(ctx, command)
}

func (runner *capacityFixtureRunner) RunAttached(_ context.Context, command AttachedCommand) error {
	runner.attached = command
	if command.Stdin != runner.expectedStdin {
		return errors.New("fixture received a copied stdin reader")
	}
	if len(command.ExtraFiles) != 1 || command.OnStarted == nil {
		return errors.New("fixture received no fixed input gate")
	}
	if runner.prepareCalls != 0 {
		return errors.New("fixture input handoff happened before child start")
	}
	runner.childStarted = true
	if err := command.OnStarted(); err != nil {
		return err
	}
	gate := make([]byte, 1)
	if _, err := io.ReadFull(command.ExtraFiles[0], gate); err != nil || gate[0] != capacityInputGateSignal {
		return errors.New("fixture received invalid input gate signal")
	}
	if _, err := io.WriteString(command.Stdout, capacityReadinessFixture); err != nil {
		return err
	}
	runner.stdinReadAfterReady = true
	if _, err := io.ReadAll(command.Stdin); err != nil {
		return err
	}
	_, err := io.WriteString(command.Stdout, runner.attachedStdout)
	return err
}

func (runner *capacityFixtureRunner) prepareInputHandoff() error {
	if !runner.childStarted {
		return errors.New("fixture input handoff preceded child start")
	}
	runner.prepareCalls++
	return nil
}

type fixtureAttachedProcess struct {
	signaled chan struct{}
	killed   chan struct{}
	wait     chan error
	once     sync.Once
	mu       sync.Mutex
	forced   bool
}

func newFixtureAttachedProcess() *fixtureAttachedProcess {
	return &fixtureAttachedProcess{signaled: make(chan struct{}), killed: make(chan struct{}), wait: make(chan error, 1)}
}

func (process *fixtureAttachedProcess) Signal(os.Signal) error {
	process.once.Do(func() { close(process.signaled) })
	return nil
}

func (process *fixtureAttachedProcess) Kill() error {
	process.mu.Lock()
	process.forced = true
	process.mu.Unlock()
	select {
	case <-process.killed:
	default:
		close(process.killed)
	}
	return nil
}

func (process *fixtureAttachedProcess) Wait() error { return <-process.wait }

func (process *fixtureAttachedProcess) finish(err error) { process.wait <- err }

func (process *fixtureAttachedProcess) wasKilled() bool {
	process.mu.Lock()
	defer process.mu.Unlock()
	return process.forced
}
