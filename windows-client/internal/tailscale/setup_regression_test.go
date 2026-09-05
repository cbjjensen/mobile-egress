package tailscale

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestStatusVerifiesApplicationOncePerRefresh(t *testing.T) {
	tracker := &resolverGuardTracker{}
	controller := newResolverController(tracker.resolver(DarwinStandalone), &resolverTestRunner{})
	status, err := controller.Status(context.Background())
	if err != nil || !status.Online || !status.FunnelReady {
		t.Fatalf("status failed: %v", err)
	}
	if tracker.resolutions.Load() != 1 || tracker.closes.Load() != 1 {
		t.Fatal("status repeats full app verification instead of sharing one operation guard")
	}
}

func TestInspectionSeparatesMissingApplicationFromFailedVerification(t *testing.T) {
	for _, failure := range []error{ErrNotInstalled, errTailscaleAppVerification, context.DeadlineExceeded} {
		controller := newResolverController(func(context.Context) (DarwinInstallation, error) { return DarwinInstallation{}, failure }, &resolverTestRunner{})
		status, err := controller.Inspect(context.Background())
		if err == nil || status.Online {
			t.Fatal("failed check was reported as a working connection")
		}
		if errors.Is(err, ErrNotInstalled) != errors.Is(failure, ErrNotInstalled) {
			t.Fatal("verification failure was reported as a missing installation")
		}
	}
}

func TestLoginApprovalURLRejectsUntrustedAndIncompleteOutput(t *testing.T) {
	valid := "https://login.tailscale.com/a/1234abc"
	if got := findLoginApprovalURL([]byte("To authenticate:\n" + valid + "\n")); got != valid {
		t.Fatal("official login URL was not detected")
	}
	for _, value := range []string{valid, "https://login.tailscale.com.evil/a/1234abc\n", "http://login.tailscale.com/a/1234abc\n", "https://login.tailscale.com/a/1234abc?redirect=evil\n"} {
		if findLoginApprovalURL([]byte(value)) != "" {
			t.Fatal("accepted untrusted or incomplete login URL")
		}
	}
}

type loginApprovalRunner struct {
	resolverTestRunner
	opened *bool
}

type partialFunnelRunner struct {
	resolverTestRunner
	opened *bool
}

func (runner *partialFunnelRunner) RunStreaming(_ context.Context, _ string, observe func([]byte), _ ...string) ([]byte, error) {
	observe([]byte("To enable:\nhttps://login.tailscale.com/f/funnel?node=niiy"))
	if *runner.opened {
		return nil, errors.New("opened incomplete Funnel URL")
	}
	observe([]byte("8GTvVs11CNTRL\n"))
	return nil, nil
}
func TestFunnelApprovalWaitsForTheCompleteURL(t *testing.T) {
	opened := false
	runner := &partialFunnelRunner{opened: &opened}
	controller := newResolverController((&resolverGuardTracker{}).resolver(DarwinStandalone), runner)
	controller.SetFunnelApprovalHandler(func(string) { opened = true })
	_, err := controller.operation(context.Background(), func(session *Controller) (Status, error) { return Status{}, session.enableFunnel(context.Background()) })
	if err != nil || !opened {
		t.Fatalf("Funnel browser approval: %v", err)
	}
}
func (runner *loginApprovalRunner) RunStreaming(ctx context.Context, path string, observe func([]byte), args ...string) ([]byte, error) {
	if len(args) == 1 && args[0] == "login" {
		observe([]byte("https://login.tailscale.com/a/1234"))
		if *runner.opened {
			return nil, errors.New("opened incomplete URL")
		}
		observe([]byte("abcd\n"))
		if !*runner.opened {
			return nil, errors.New("browser did not open during login")
		}
		return nil, nil
	}
	return runner.resolverTestRunner.RunStreaming(ctx, path, observe, args...)
}
func TestLoginOpensBrowserBeforeCommandCompletes(t *testing.T) {
	opened := false
	runner := &loginApprovalRunner{opened: &opened}
	controller := newResolverController((&resolverGuardTracker{}).resolver(DarwinStandalone), runner)
	controller.SetFunnelApprovalHandler(func(url string) { opened = strings.HasSuffix(url, "/a/1234abcd") })
	_, err := controller.operation(context.Background(), func(session *Controller) (Status, error) {
		return Status{}, session.runWithApproval(context.Background(), findLoginApprovalURL, "login")
	})
	if err != nil || !opened {
		t.Fatalf("login browser approval: %v", err)
	}
}

func TestStatusFailureDoesNotStartAnotherLogin(t *testing.T) {
	tracker := &resolverGuardTracker{}
	runner := &resolverTestRunner{runErr: errors.New("daemon unavailable")}
	controller := newResolverController(tracker.resolver(DarwinStandalone), runner)
	if _, err := controller.Connect(context.Background()); err == nil {
		t.Fatal("expected status failure")
	}
	if len(runner.argumentSnapshot()) != 1 {
		t.Fatal("status failure triggered a misleading login attempt")
	}
}

type changedAppRunner struct {
	resolverTestRunner
	guard       *resolverTestGuard
	statusReads int
}

func (runner *changedAppRunner) Run(ctx context.Context, path string, args ...string) ([]byte, error) {
	output, err := runner.resolverTestRunner.Run(ctx, path, args...)
	if len(args) > 0 && args[0] == "status" {
		runner.statusReads++
		if runner.statusReads == 2 {
			runner.guard.revalidateErr = errors.New("app changed after final online status")
		}
	}
	return output, err
}

func TestConnectDoesNotIgnoreAnAppChangeAfterOnlineStatus(t *testing.T) {
	guard := &resolverTestGuard{bundlePath: fixedTailscaleBundlePath, executablePath: fixedTailscaleExecutablePath}
	runner := &changedAppRunner{guard: guard}
	controller := newResolverController(func(context.Context) (DarwinInstallation, error) {
		return resolverTestInstallation(DarwinStandalone, guard), nil
	}, runner)
	_, err := controller.Connect(context.Background())
	if !errors.Is(err, errTailscaleAppVerification) {
		t.Fatalf("changed app was not rejected: %v", err)
	}
	if guard.closeCalls.Load() != 1 {
		t.Fatal("operation did not close its app guard")
	}
}
