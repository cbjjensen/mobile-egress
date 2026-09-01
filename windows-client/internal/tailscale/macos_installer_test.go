package tailscale

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestDarwinInstallerVerifiesBeforeLaunchAndRequiresFreshFinalStandaloneAssessment(t *testing.T) {
	stage, operations := newModelStagedPackage(t)
	events := []string{}
	observedStage := &installerEventStageOperations{macStageOperations: stage.operations, events: &events}
	stage.operations = observedStage
	session := newCleanupTestSession(installerWaitResult{Reason: installerTerminalNaturalZero, ExitCode: 0})
	findCalls := 0
	installer := newTestDarwinInstaller()
	installer.resolveStage = func(context.Context, *http.Client) (MacRelease, *stagedMacPKG, error) {
		events = append(events, "stage")
		return MacRelease{Version: "1.100.1"}, stage, nil
	}
	installer.VerifyPKG = func(ctx context.Context, got *stagedMacPKG) error {
		events = append(events, "verify")
		if got != stage || got.Revalidate(ctx) != nil || got.Path() == "" {
			return errors.New("unusable retained stage")
		}
		if observedStage.validateParentCalls != 2 {
			return errors.New("unexpected verifier revalidation count")
		}
		return nil
	}
	installer.LaunchInstaller = func(ctx context.Context, got *stagedMacPKG) (installerSession, error) {
		events = append(events, "launch")
		if observedStage.validateParentCalls != 6 {
			return nil, errors.New("two transaction revalidations did not precede launch")
		}
		if got != stage || got.Revalidate(ctx) != nil || got.Path() == "" {
			return nil, errors.New("unusable launch stage")
		}
		return session, nil
	}
	installer.FindInstallation = func(context.Context) (DarwinInstallation, error) {
		findCalls++
		events = append(events, "find")
		return standaloneTestInstallation(&events), nil
	}

	release, err := installer.Install(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if release != (Release{Version: "1.100.1"}) {
		t.Fatalf("release = %#v", release)
	}
	if findCalls != 2 {
		t.Fatalf("FindInstallation calls = %d, want immediate plus fresh final", findCalls)
	}
	if operations.removeFileCalls != 1 || operations.removeDirectoryCalls != 1 {
		t.Fatalf("stage removals = %d/%d", operations.removeFileCalls, operations.removeDirectoryCalls)
	}
	launchIndex := indexOfInstallerEvent(events, "launch")
	if launchIndex < 0 || !reflect.DeepEqual(events[:2], []string{"stage", "verify"}) {
		t.Fatalf("events = %#v", events)
	}
}

func TestDarwinInstallerPollsButOnlyTerminalPlusFinalStandaloneCanSucceed(t *testing.T) {
	stage, _ := newModelStagedPackage(t)
	session := &controlledInstallerSession{
		done: make(chan installerWaitResult, 1),
		stop: func(context.Context) (installerStopResult, error) {
			return installerStopResult{Quiescent: true, Terminal: installerTerminalNaturalZero}, nil
		},
	}
	poll := make(chan time.Time, 1)
	deadline := make(chan time.Time)
	findCalls := 0
	installer := newTestDarwinInstaller()
	installer.resolveStage = func(context.Context, *http.Client) (MacRelease, *stagedMacPKG, error) {
		return MacRelease{Version: "1.100.1"}, stage, nil
	}
	installer.VerifyPKG = func(context.Context, *stagedMacPKG) error { return nil }
	installer.LaunchInstaller = func(context.Context, *stagedMacPKG) (installerSession, error) { return session, nil }
	installer.FindInstallation = func(context.Context) (DarwinInstallation, error) {
		findCalls++
		switch findCalls {
		case 1:
			poll <- time.Unix(1, 0)
			return DarwinInstallation{}, errTailscaleAppVerification
		case 2:
			session.done <- installerWaitResult{Reason: installerTerminalNaturalZero, ExitCode: 0}
			close(session.done)
			return standaloneTestInstallation(nil), nil
		default:
			return standaloneTestInstallation(nil), nil
		}
	}
	installer.after = func(delay time.Duration) <-chan time.Time {
		if delay == installer.PollInterval {
			return poll
		}
		return deadline
	}

	release, err := installer.Install(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if release != (Release{Version: "1.100.1"}) || findCalls != 3 {
		t.Fatalf("Install() = %#v, finds=%d", release, findCalls)
	}
}

func TestDarwinInstallerCancellationUsesFreshCleanupContextAndDoesNotCreateSuccess(t *testing.T) {
	stage, operations := newModelStagedPackage(t)
	operation, cancel := context.WithCancel(context.Background())
	session := &controlledInstallerSession{
		done: make(chan installerWaitResult),
		stop: func(ctx context.Context) (installerStopResult, error) {
			if ctx == operation || ctx.Err() != nil {
				return installerStopResult{}, errors.New("operation context reached Stop")
			}
			return installerStopResult{Quiescent: true, Terminal: installerTerminalNaturalZero}, nil
		},
	}
	installer := newTestDarwinInstaller()
	installer.resolveStage = func(context.Context, *http.Client) (MacRelease, *stagedMacPKG, error) {
		return MacRelease{Version: "1.100.1"}, stage, nil
	}
	installer.VerifyPKG = func(context.Context, *stagedMacPKG) error { return nil }
	installer.LaunchInstaller = func(context.Context, *stagedMacPKG) (installerSession, error) { return session, nil }
	installer.FindInstallation = func(context.Context) (DarwinInstallation, error) {
		cancel()
		return DarwinInstallation{}, errTailscaleAppVerification
	}

	release, err := installer.Install(operation)
	if !errors.Is(err, errDarwinInstallerFailed) || release != (Release{}) {
		t.Fatalf("Install() = %#v, %v", release, err)
	}
	if session.stopCalls != 1 || operations.removeFileCalls != 1 {
		t.Fatalf("cleanup calls = stop %d remove %d", session.stopCalls, operations.removeFileCalls)
	}
}

func TestDarwinInstallerFailsClosedForMalformedDoneAndBusyLease(t *testing.T) {
	t.Run("closed Done cannot be relabeled by Stop", func(t *testing.T) {
		stage, operations := newModelStagedPackage(t)
		done := make(chan installerWaitResult)
		close(done)
		session := &controlledInstallerSession{
			done: done,
			stop: func(context.Context) (installerStopResult, error) {
				return installerStopResult{Quiescent: true, Terminal: installerTerminalNaturalZero}, nil
			},
		}
		installer := newTestDarwinInstaller()
		installer.resolveStage = func(context.Context, *http.Client) (MacRelease, *stagedMacPKG, error) {
			return MacRelease{Version: "1.100.1"}, stage, nil
		}
		installer.VerifyPKG = func(context.Context, *stagedMacPKG) error { return nil }
		installer.LaunchInstaller = func(context.Context, *stagedMacPKG) (installerSession, error) { return session, nil }
		installer.FindInstallation = func(context.Context) (DarwinInstallation, error) {
			return DarwinInstallation{}, errTailscaleAppVerification
		}

		if _, err := installer.Install(context.Background()); !errors.Is(err, errMacCleanupPending) {
			t.Fatalf("Install() = %v", err)
		}
		if operations.removeFileCalls != 0 {
			t.Fatalf("malformed evidence removed stage %d times", operations.removeFileCalls)
		}
		if _, err := installer.Cleanup.Acquire(); !errors.Is(err, errDarwinInstallerBusy) {
			t.Fatalf("Acquire() after retained cleanup = %v", err)
		}
	})

	t.Run("busy loses before staging", func(t *testing.T) {
		installer := newTestDarwinInstaller()
		installer.VerifyPKG = func(context.Context, *stagedMacPKG) error { return nil }
		installer.LaunchInstaller = func(context.Context, *stagedMacPKG) (installerSession, error) { return nil, installerNoDispatchError{} }
		installer.FindInstallation = func(context.Context) (DarwinInstallation, error) {
			return DarwinInstallation{}, errTailscaleAppVerification
		}
		lease, err := installer.Cleanup.Acquire()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = lease.ReleaseBeforeDispatch() })
		resolverCalls := 0
		installer.resolveStage = func(context.Context, *http.Client) (MacRelease, *stagedMacPKG, error) {
			resolverCalls++
			return MacRelease{}, nil, errors.New("unexpected")
		}
		if _, err := installer.Install(context.Background()); !errors.Is(err, errDarwinInstallerBusy) {
			t.Fatalf("Install() = %v", err)
		}
		if resolverCalls != 0 {
			t.Fatalf("busy install reached resolver %d times", resolverCalls)
		}
	})
}

func newTestDarwinInstaller() DarwinInstaller {
	return DarwinInstaller{
		Cleanup:      newInstallerCleanupManager(),
		PollInterval: time.Second,
		PollLimit:    3 * time.Second,
		after:        time.After,
	}
}

type installerEventStageOperations struct {
	macStageOperations
	events              *[]string
	validateParentCalls int
}

func (operations *installerEventStageOperations) ValidateParent(ctx context.Context, parent macStageDirectory) error {
	operations.validateParentCalls++
	*operations.events = append(*operations.events, "revalidate")
	return operations.macStageOperations.ValidateParent(ctx, parent)
}

func indexOfInstallerEvent(events []string, wanted string) int {
	for index, event := range events {
		if event == wanted {
			return index
		}
	}
	return -1
}

type testAppGuard struct {
	events     *[]string
	closeCalls int
}

func (guard *testAppGuard) Revalidate(context.Context) error { return nil }
func (guard *testAppGuard) BundlePath() string               { return fixedTailscaleBundlePath }
func (guard *testAppGuard) ExecutablePath() string           { return fixedTailscaleExecutablePath }
func (guard *testAppGuard) Close() error {
	guard.closeCalls++
	if guard.events != nil {
		*guard.events = append(*guard.events, "close-app")
	}
	return nil
}

func standaloneTestInstallation(events *[]string) DarwinInstallation {
	return DarwinInstallation{
		BundlePath: fixedTailscaleBundlePath,
		Executable: fixedTailscaleExecutablePath,
		BundleID:   "io.tailscale.ipn.macsys",
		Variant:    DarwinStandalone,
		guard:      &testAppGuard{events: events},
	}
}

type controlledInstallerSession struct {
	mu        sync.Mutex
	done      chan installerWaitResult
	stop      func(context.Context) (installerStopResult, error)
	stopCalls int
}

func (session *controlledInstallerSession) Done() <-chan installerWaitResult { return session.done }

func (session *controlledInstallerSession) Stop(ctx context.Context) (installerStopResult, error) {
	session.mu.Lock()
	session.stopCalls++
	stop := session.stop
	session.mu.Unlock()
	if stop != nil {
		return stop(ctx)
	}
	select {
	case <-ctx.Done():
		return installerStopResult{}, ctx.Err()
	case result, ok := <-session.done:
		if !ok {
			return installerStopResult{}, errors.New("missing result")
		}
		return stopResultForWait(result), nil
	}
}
