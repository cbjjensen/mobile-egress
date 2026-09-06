package tailscale

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestControllerStatusReusesVerifiedInstallation(t *testing.T) {
	tracker := &resolverGuardTracker{}
	runner := &resolverTestRunner{}
	controller := newResolverController(tracker.resolver(DarwinStandalone), runner)
	defer controller.Close()
	for i := 0; i < 3; i++ {
		if _, err := controller.Inspect(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if got := tracker.resolutions.Load(); got != 1 {
		t.Fatalf("full verification calls = %d, want 1", got)
	}
	if got := len(runner.argumentSnapshot()); got != 6 {
		t.Fatalf("CLI calls = %d, want 6 fresh status commands", got)
	}
}

type offlineCacheRunner struct{ calls int }

func (runner *offlineCacheRunner) Run(context.Context, string, ...string) ([]byte, error) {
	runner.calls++
	return []byte(`{"BackendState":"Stopped"}`), nil
}

func TestControllerOfflineStatusRetainsVerificationUntilTTL(t *testing.T) {
	tracker := &resolverGuardTracker{}
	runner := &offlineCacheRunner{}
	controller := newResolverController(tracker.resolver(DarwinStandalone), runner)
	defer controller.Close()
	now := time.Unix(1000, 0)
	controller.verification.now = func() time.Time { return now }
	check := func(want int32) {
		t.Helper()
		status, err := controller.Inspect(context.Background())
		if !errors.Is(err, ErrNotOnline) || !status.Installed || status.Online {
			t.Fatalf("offline status = %+v, error = %v", status, err)
		}
		if tracker.resolutions.Load() != want {
			t.Fatalf("full verifications = %d, want %d", tracker.resolutions.Load(), want)
		}
	}
	check(1)
	now = now.Add(59 * time.Second)
	check(1)
	if tracker.closes.Load() != 0 {
		t.Fatal("offline status discarded verified guard")
	}
	now = now.Add(time.Second)
	check(2)
	if runner.calls != 3 || tracker.closes.Load() != 1 {
		t.Fatalf("CLI calls / expired guard closes = %d/%d, want 3/1", runner.calls, tracker.closes.Load())
	}
}

func TestControllerVerificationTTLStartsAtSuccessAndDoesNotSlide(t *testing.T) {
	tracker := &resolverGuardTracker{}
	now := time.Unix(1000, 0)
	resolve := tracker.resolver(DarwinStandalone)
	controller := newResolverController(func(ctx context.Context) (DarwinInstallation, error) {
		now = now.Add(20 * time.Second)
		return resolve(ctx)
	}, &resolverTestRunner{})
	defer controller.Close()
	controller.verification.now = func() time.Time { return now }
	check := func(want int32) {
		t.Helper()
		if _, err := controller.Status(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := tracker.resolutions.Load(); got != want {
			t.Fatalf("verifications = %d, want %d", got, want)
		}
	}
	check(1)
	now = now.Add(59 * time.Second)
	check(1)
	now = now.Add(time.Second)
	check(2)
	if tracker.closes.Load() != 1 {
		t.Fatal("expired guard not closed")
	}
}

func TestControllerChangedCachedGuardRequiresFullVerification(t *testing.T) {
	for _, replacementFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "verified replacement", true: "untrusted replacement"}[replacementFailure], func(t *testing.T) {
			var guards []*resolverTestGuard
			runner := &resolverTestRunner{}
			controller := newResolverController(func(context.Context) (DarwinInstallation, error) {
				if len(guards) > 0 && replacementFailure {
					return DarwinInstallation{}, errTailscaleAppVerification
				}
				guard := &resolverTestGuard{bundlePath: fixedTailscaleBundlePath, executablePath: fixedTailscaleExecutablePath}
				guards = append(guards, guard)
				return resolverTestInstallation(DarwinStandalone, guard), nil
			}, runner)
			defer controller.Close()
			if _, err := controller.Status(context.Background()); err != nil {
				t.Fatal(err)
			}
			guards[0].revalidateErr = errors.New("replaced")
			_, err := controller.Status(context.Background())
			if replacementFailure {
				if !errors.Is(err, errTailscaleAppVerification) || len(runner.argumentSnapshot()) != 2 {
					t.Fatal("unverified replacement dispatched")
				}
			} else if err != nil || len(guards) != 2 || len(runner.argumentSnapshot()) != 4 {
				t.Fatalf("replacement not fully verified: %v", err)
			}
			if guards[0].closeCalls.Load() != 1 {
				t.Fatal("changed guard not closed")
			}
		})
	}
}

func TestControllerActionsAlwaysVerifyAndInvalidate(t *testing.T) {
	for _, name := range []string{"check", "connect", "enable"} {
		for _, fail := range []bool{false, true} {
			t.Run(name+map[bool]string{false: " success", true: " failure"}[fail], func(t *testing.T) {
				tracker := &resolverGuardTracker{}
				runner := &resolverTestRunner{}
				resolve := tracker.resolver(DarwinStandalone)
				failResolve := false
				controller := newResolverController(func(ctx context.Context) (DarwinInstallation, error) {
					if failResolve {
						tracker.resolutions.Add(1)
						return DarwinInstallation{}, errTailscaleAppVerification
					}
					return resolve(ctx)
				}, runner)
				defer controller.Close()
				if _, err := controller.Status(context.Background()); err != nil {
					t.Fatal(err)
				}
				if fail {
					if name == "check" {
						failResolve = true
					} else {
						runner.runErr = errors.New("CLI failed")
					}
				}
				var err error
				switch name {
				case "check":
					err = controller.CheckInstalled(context.Background())
				case "connect":
					_, err = controller.Connect(context.Background())
				case "enable":
					_, err = controller.Enable(context.Background())
				}
				if (err != nil) != fail {
					t.Fatalf("action error = %v", err)
				}
				if tracker.resolutions.Load() != 2 || tracker.live.Load() != 0 {
					t.Fatal("action reused or retained verification")
				}
				failResolve = false
				runner.runErr = nil
				if _, err := controller.Status(context.Background()); err != nil {
					t.Fatal(err)
				}
				if tracker.resolutions.Load() != 3 {
					t.Fatal("post-action status did not verify")
				}
			})
		}
	}
}

func TestControllerFailedStatusDiscardsCachedVerification(t *testing.T) {
	tracker := &resolverGuardTracker{}
	runner := &resolverTestRunner{}
	controller := newResolverController(tracker.resolver(DarwinStandalone), runner)
	defer controller.Close()
	if _, err := controller.Status(context.Background()); err != nil {
		t.Fatal(err)
	}
	runner.runErr = errors.New("daemon unavailable")
	if _, err := controller.Status(context.Background()); err == nil {
		t.Fatal("expected failure")
	}
	if tracker.live.Load() != 0 {
		t.Fatal("failed status retained guard")
	}
	runner.runErr = nil
	if _, err := controller.Status(context.Background()); err != nil {
		t.Fatal(err)
	}
	if tracker.resolutions.Load() != 2 {
		t.Fatal("failed verification reused")
	}
}

func TestControllerConcurrentStatusSharesVerificationAndRevalidatesEveryCLI(t *testing.T) {
	guard := &resolverTestGuard{bundlePath: fixedTailscaleBundlePath, executablePath: fixedTailscaleExecutablePath}
	resolutions := 0
	controller := newResolverController(func(context.Context) (DarwinInstallation, error) {
		resolutions++
		return resolverTestInstallation(DarwinStandalone, guard), nil
	}, &resolverTestRunner{})
	defer controller.Close()
	var calls sync.WaitGroup
	for i := 0; i < 20; i++ {
		calls.Add(1)
		go func() {
			defer calls.Done()
			if _, err := controller.Status(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	calls.Wait()
	if resolutions != 1 {
		t.Fatalf("full verifications = %d, want 1", resolutions)
	}
	// One initial resolution validation, one validation at each of 19 cache
	// entries, and a validation immediately before each of 40 CLI commands.
	if got := guard.revalidations.Load(); got != 60 {
		t.Fatalf("path validations = %d, want 60", got)
	}
}

type cacheBlockingRunner struct {
	resolverTestRunner
	entered chan struct{}
	resume  chan struct{}
	once    sync.Once
}

func (runner *cacheBlockingRunner) Run(ctx context.Context, path string, args ...string) ([]byte, error) {
	runner.once.Do(func() { close(runner.entered); <-runner.resume })
	return runner.resolverTestRunner.Run(ctx, path, args...)
}

func TestControllerCacheWaitCancellationAndCloseWaitForActiveCLI(t *testing.T) {
	tracker := &resolverGuardTracker{}
	runner := &cacheBlockingRunner{entered: make(chan struct{}), resume: make(chan struct{})}
	controller := newResolverController(tracker.resolver(DarwinStandalone), runner)
	active := make(chan error, 1)
	go func() { _, err := controller.Status(context.Background()); active <- err }()
	<-runner.entered
	ctx, cancel := context.WithCancel(context.Background())
	waiter := make(chan error, 1)
	go func() { _, err := controller.Status(ctx); waiter <- err }()
	cancel()
	select {
	case err := <-waiter:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("waiter ignored cancellation")
	}
	closed := make(chan error, 1)
	go func() { closed <- controller.Close() }()
	select {
	case <-closed:
		t.Fatal("closed active guard")
	case <-time.After(20 * time.Millisecond):
	}
	if tracker.closes.Load() != 0 {
		t.Fatal("guard closed during CLI")
	}
	close(runner.resume)
	if err := <-active; err != nil {
		t.Fatal(err)
	}
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	if tracker.closes.Load() != 1 {
		t.Fatal("retained guard leaked")
	}
	if _, err := controller.Status(context.Background()); err == nil {
		t.Fatal("closed controller dispatched")
	}
	if err := controller.Close(); err != nil {
		t.Fatal(err)
	}
}
