package tailscale

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestControllerResolverInstalledUsesFiveSecondFreshGuard(t *testing.T) {
	t.Parallel()

	guard := &resolverTestGuard{
		bundlePath:     fixedTailscaleBundlePath,
		executablePath: fixedTailscaleExecutablePath,
	}
	resolverCalls := 0
	controller := newResolverController(func(ctx context.Context) (DarwinInstallation, error) {
		resolverCalls++
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Fatal("Installed resolver context has no deadline")
		}
		remaining := time.Until(deadline)
		if remaining < 4*time.Second || remaining > 5*time.Second {
			t.Fatalf("Installed resolver deadline is %v away, want fixed five-second bound", remaining)
		}
		return resolverTestInstallation(DarwinStandalone, guard), nil
	}, &resolverTestRunner{})

	if !controller.Installed() {
		t.Fatal("Installed() = false for a verified standalone installation")
	}
	if resolverCalls != 1 || guard.revalidations.Load() != 1 || guard.closeCalls.Load() != 1 {
		t.Fatalf("resolver/revalidate/close calls = %d/%d/%d, want 1/1/1",
			resolverCalls, guard.revalidations.Load(), guard.closeCalls.Load())
	}
}

func TestControllerResolverStatusUsesOneFreshGuardPerCommandForBothVariants(t *testing.T) {
	t.Parallel()

	for _, variant := range []DarwinVariant{DarwinStandalone, DarwinAppStore} {
		variant := variant
		t.Run(resolverVariantName(variant), func(t *testing.T) {
			t.Parallel()
			tracker := &resolverGuardTracker{}
			controller := newResolverController(tracker.resolver(variant), &resolverTestRunner{})

			status, err := controller.Status(context.Background())
			if err != nil || !status.Online || !status.FunnelReady {
				t.Fatalf("Status() = %#v/%v, want online Funnel-ready status", status, err)
			}
			if got := tracker.resolutions.Load(); got != 2 {
				t.Fatalf("resolver calls = %d, want 2", got)
			}
			if got := tracker.closes.Load(); got != 2 {
				t.Fatalf("guard closes = %d, want 2", got)
			}
			if got := tracker.live.Load(); got != 0 {
				t.Fatalf("live guards = %d, want 0", got)
			}
		})
	}
}

func TestControllerResolverEnableUsesFreshGuardForEveryActualCommand(t *testing.T) {
	t.Parallel()

	tracker := &resolverGuardTracker{}
	runner := &resolverTestRunner{}
	controller := newResolverController(tracker.resolver(DarwinStandalone), runner)
	controller.SetFunnelApprovalHandler(func(string) {})

	status, err := controller.Enable(context.Background())
	if err != nil || !status.FunnelReady {
		t.Fatalf("Enable() = %#v/%v, want Funnel-ready status", status, err)
	}
	wantArguments := [][]string{
		{"status", "--json"},
		{"funnel", "status", "--json"},
		append([]string(nil), testPlatformUpArguments...),
		{"funnel", "--bg", "--yes", "--tcp=8443", "tcp://127.0.0.1:8443"},
		{"status", "--json"},
		{"funnel", "status", "--json"},
	}
	if got := runner.argumentSnapshot(); !reflect.DeepEqual(got, wantArguments) {
		t.Fatalf("CLI arguments = %#v, want %#v", got, wantArguments)
	}
	if got := tracker.resolutions.Load(); got != int32(len(wantArguments)) {
		t.Fatalf("resolver calls = %d, want %d", got, len(wantArguments))
	}
	if got := tracker.closes.Load(); got != int32(len(wantArguments)) || tracker.live.Load() != 0 {
		t.Fatalf("guard closes/live = %d/%d, want %d/0", got, tracker.live.Load(), len(wantArguments))
	}
	for _, executable := range runner.executableSnapshot() {
		if executable != fixedTailscaleExecutablePath {
			t.Fatalf("runner executable = %q, want fixed bundled CLI", executable)
		}
	}
}

func TestControllerResolverRejectsUnverifiedResultsBeforeRunner(t *testing.T) {
	t.Parallel()

	wrongPathGuard := &resolverTestGuard{bundlePath: fixedTailscaleBundlePath, executablePath: "/tmp/lookalike"}
	changedGuard := &resolverTestGuard{
		bundlePath: fixedTailscaleBundlePath, executablePath: fixedTailscaleExecutablePath,
		revalidateErr: errors.New("changed"),
	}
	mismatchedGuard := &resolverTestGuard{bundlePath: fixedTailscaleBundlePath, executablePath: fixedTailscaleExecutablePath}
	tests := []struct {
		name      string
		resolve   installationResolver
		guard     *resolverTestGuard
		wantClose int32
	}{
		{
			name: "resolver error",
			resolve: func(context.Context) (DarwinInstallation, error) {
				return DarwinInstallation{}, errors.New("private verifier detail")
			},
		},
		{
			name: "nil guard",
			resolve: func(context.Context) (DarwinInstallation, error) {
				return DarwinInstallation{
					BundlePath: fixedTailscaleBundlePath, Executable: fixedTailscaleExecutablePath,
					BundleID: "io.tailscale.ipn.macsys", Variant: DarwinStandalone,
				}, nil
			},
		},
		{
			name: "wrong guard path",
			resolve: func(context.Context) (DarwinInstallation, error) {
				return resolverTestInstallation(DarwinStandalone, wrongPathGuard), nil
			},
			guard:     wrongPathGuard,
			wantClose: 1,
		},
		{
			name: "changed before dispatch",
			resolve: func(context.Context) (DarwinInstallation, error) {
				return resolverTestInstallation(DarwinStandalone, changedGuard), nil
			},
			guard:     changedGuard,
			wantClose: 1,
		},
		{
			name: "variant and bundle mismatch",
			resolve: func(context.Context) (DarwinInstallation, error) {
				installation := resolverTestInstallation(DarwinStandalone, mismatchedGuard)
				installation.BundleID = "io.tailscale.ipn.macos"
				return installation, nil
			},
			guard:     mismatchedGuard,
			wantClose: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			runner := &resolverTestRunner{}
			controller := newResolverController(test.resolve, runner)
			_, err := controller.Status(context.Background())
			if err == nil || strings.Contains(err.Error(), "private verifier detail") || strings.Contains(err.Error(), "changed") {
				t.Fatalf("Status() error = %v, want fixed redacted error", err)
			}
			if got := len(runner.argumentSnapshot()); got != 0 {
				t.Fatalf("runner calls = %d, want 0", got)
			}
			if test.guard != nil && test.guard.closeCalls.Load() != test.wantClose {
				t.Fatalf("guard close calls = %d, want %d", test.guard.closeCalls.Load(), test.wantClose)
			}
		})
	}
}

func TestControllerResolverClosesAfterRunnerFailureAndCleanupOverridesEveryOutcome(t *testing.T) {
	t.Parallel()

	t.Run("runner failure still closes", func(t *testing.T) {
		guard := &resolverTestGuard{bundlePath: fixedTailscaleBundlePath, executablePath: fixedTailscaleExecutablePath}
		runner := &resolverTestRunner{runErr: errors.New("private process detail")}
		controller := newResolverController(func(context.Context) (DarwinInstallation, error) {
			return resolverTestInstallation(DarwinStandalone, guard), nil
		}, runner)
		_, err := controller.Status(context.Background())
		if err == nil || strings.Contains(err.Error(), "private process detail") {
			t.Fatalf("Status() error = %v, want fixed redacted error", err)
		}
		if guard.closeCalls.Load() != 1 {
			t.Fatalf("guard close calls = %d, want 1", guard.closeCalls.Load())
		}
	})

	for _, runnerErr := range []error{nil, errors.New("private process detail")} {
		guard := &resolverTestGuard{
			bundlePath: fixedTailscaleBundlePath, executablePath: fixedTailscaleExecutablePath,
			closeErr: errors.New("private close detail"),
		}
		runner := &resolverTestRunner{runErr: runnerErr}
		controller := newResolverController(func(context.Context) (DarwinInstallation, error) {
			return resolverTestInstallation(DarwinStandalone, guard), nil
		}, runner)
		_, err := controller.Status(context.Background())
		if !errors.Is(err, errTailscaleAppCleanup) || err.Error() != "Tailscale application verification cleanup failed" {
			t.Fatalf("Status() error = %v, want exact cleanup error", err)
		}
		if controller.Installed() {
			t.Fatal("Installed() = true when guard cleanup fails")
		}
	}
}

func TestControllerResolverPreservesCleanupFailureFromFinalStatus(t *testing.T) {
	t.Parallel()

	for _, operation := range []struct {
		name string
		run  func(*Controller) error
	}{
		{
			name: "connect",
			run: func(controller *Controller) error {
				_, err := controller.Connect(context.Background())
				return err
			},
		},
		{
			name: "enable",
			run: func(controller *Controller) error {
				_, err := controller.Enable(context.Background())
				return err
			},
		},
	} {
		operation := operation
		t.Run(operation.name, func(t *testing.T) {
			t.Parallel()
			var resolutions atomic.Int32
			controller := newResolverController(func(context.Context) (DarwinInstallation, error) {
				guard := &resolverTestGuard{
					bundlePath: fixedTailscaleBundlePath, executablePath: fixedTailscaleExecutablePath,
				}
				if resolutions.Add(1) == 5 {
					guard.closeErr = errors.New("private close detail")
				}
				return resolverTestInstallation(DarwinStandalone, guard), nil
			}, &resolverTestRunner{})

			err := operation.run(controller)
			if !errors.Is(err, errTailscaleAppCleanup) || err.Error() != "Tailscale application verification cleanup failed" {
				t.Fatalf("operation error = %v, want exact cleanup error", err)
			}
		})
	}
}

func TestControllerResolverDoesNotCacheRepeatedOrConcurrentChecks(t *testing.T) {
	t.Parallel()

	tracker := &resolverGuardTracker{}
	controller := newResolverController(tracker.resolver(DarwinAppStore), &resolverTestRunner{})
	for index := 0; index < 100; index++ {
		if !controller.Installed() {
			t.Fatalf("Installed() = false at sequential check %d", index)
		}
	}

	var checks sync.WaitGroup
	for index := 0; index < 20; index++ {
		checks.Add(1)
		go func() {
			defer checks.Done()
			if !controller.Installed() {
				t.Error("concurrent Installed() = false")
			}
		}()
	}
	checks.Wait()

	if got := tracker.resolutions.Load(); got != 120 {
		t.Fatalf("resolver calls = %d, want 120", got)
	}
	if got := tracker.closes.Load(); got != 120 || tracker.live.Load() != 0 {
		t.Fatalf("guard closes/live = %d/%d, want 120/0", got, tracker.live.Load())
	}
}

type resolverTestGuard struct {
	bundlePath     string
	executablePath string
	revalidateErr  error
	closeErr       error
	revalidations  atomic.Int32
	closeCalls     atomic.Int32
	closeOnce      sync.Once
	onClose        func()
}

func (guard *resolverTestGuard) Revalidate(context.Context) error {
	guard.revalidations.Add(1)
	return guard.revalidateErr
}

func (guard *resolverTestGuard) BundlePath() string {
	return guard.bundlePath
}

func (guard *resolverTestGuard) ExecutablePath() string {
	return guard.executablePath
}

func (guard *resolverTestGuard) Close() error {
	guard.closeOnce.Do(func() {
		guard.closeCalls.Add(1)
		if guard.onClose != nil {
			guard.onClose()
		}
	})
	return guard.closeErr
}

type resolverGuardTracker struct {
	resolutions atomic.Int32
	closes      atomic.Int32
	live        atomic.Int32
}

func (tracker *resolverGuardTracker) resolver(variant DarwinVariant) installationResolver {
	return func(context.Context) (DarwinInstallation, error) {
		tracker.resolutions.Add(1)
		tracker.live.Add(1)
		guard := &resolverTestGuard{
			bundlePath: fixedTailscaleBundlePath, executablePath: fixedTailscaleExecutablePath,
			onClose: func() {
				tracker.closes.Add(1)
				tracker.live.Add(-1)
			},
		}
		return resolverTestInstallation(variant, guard), nil
	}
}

func resolverTestInstallation(variant DarwinVariant, guard appExecutionGuard) DarwinInstallation {
	bundleID := "io.tailscale.ipn.macsys"
	if variant == DarwinAppStore {
		bundleID = "io.tailscale.ipn.macos"
	}
	return DarwinInstallation{
		BundlePath: fixedTailscaleBundlePath,
		Executable: fixedTailscaleExecutablePath,
		BundleID:   bundleID,
		Variant:    variant,
		guard:      guard,
	}
}

func resolverVariantName(variant DarwinVariant) string {
	if variant == DarwinStandalone {
		return "standalone"
	}
	return "app-store"
}

type resolverTestRunner struct {
	mu          sync.Mutex
	executables []string
	arguments   [][]string
	runErr      error
}

func (runner *resolverTestRunner) Run(_ context.Context, executable string, arguments ...string) ([]byte, error) {
	runner.record(executable, arguments)
	if runner.runErr != nil {
		return nil, runner.runErr
	}
	return resolverOutput(arguments), nil
}

func (runner *resolverTestRunner) RunStreaming(_ context.Context, executable string, observe func([]byte), arguments ...string) ([]byte, error) {
	runner.record(executable, arguments)
	if runner.runErr != nil {
		return nil, runner.runErr
	}
	output := resolverOutput(arguments)
	if observe != nil {
		observe(output)
	}
	return output, nil
}

func (runner *resolverTestRunner) record(executable string, arguments []string) {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	runner.executables = append(runner.executables, executable)
	runner.arguments = append(runner.arguments, append([]string(nil), arguments...))
}

func (runner *resolverTestRunner) executableSnapshot() []string {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	return append([]string(nil), runner.executables...)
}

func (runner *resolverTestRunner) argumentSnapshot() [][]string {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	result := make([][]string, len(runner.arguments))
	for index := range runner.arguments {
		result[index] = append([]string(nil), runner.arguments[index]...)
	}
	return result
}

func resolverOutput(arguments []string) []byte {
	if reflect.DeepEqual(arguments, []string{"status", "--json"}) {
		return []byte(`{"BackendState":"Running","Self":{"DNSName":"bridge.tail123.ts.net.","Online":true}}`)
	}
	if reflect.DeepEqual(arguments, []string{"funnel", "status", "--json"}) {
		return []byte(`{"TCP":{"8443":{"TCPForward":"127.0.0.1:8443"}},"AllowFunnel":{"bridge.tail123.ts.net:8443":true}}`)
	}
	return nil
}
