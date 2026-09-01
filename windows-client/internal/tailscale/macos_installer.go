package tailscale

import (
	"context"
	"errors"
	"net/http"
	"time"
)

const appleInstallerRequirement = `=anchor apple and identifier "com.apple.installer"`

const (
	defaultMacInstallPollInterval = time.Second
	maximumMacInstallPollDuration = 15 * time.Minute
	installerCleanupLimit         = 10 * time.Second
	installerCleanupRetryInterval = time.Second
)

var (
	errDarwinInstallerBusy   = errors.New("Tailscale installer is already in progress")
	errDarwinInstallerFailed = errors.New("Tailscale installation was cancelled or failed")
)

type installerTerminalReason uint8

const (
	installerTerminalNaturalZero installerTerminalReason = iota + 1
	installerTerminalNaturalNonzero
	installerTerminalExternalSignal
	installerTerminalControllerForced
	installerTerminalMalformed
)

type installerWaitResult struct {
	Reason   installerTerminalReason
	ExitCode int
}

type installerStopResult struct {
	Quiescent bool
	Terminal  installerTerminalReason
}

type installerSession interface {
	Done() <-chan installerWaitResult
	Stop(context.Context) (installerStopResult, error)
}

type DarwinInstaller struct {
	HTTPClient       *http.Client
	VerifyPKG        func(context.Context, *stagedMacPKG) error
	LaunchInstaller  func(context.Context, *stagedMacPKG) (installerSession, error)
	FindInstallation func(context.Context) (DarwinInstallation, error)
	Cleanup          *installerCleanupManager
	PollInterval     time.Duration
	PollLimit        time.Duration

	resolveStage func(context.Context, *http.Client) (MacRelease, *stagedMacPKG, error)
	after        func(time.Duration) <-chan time.Time
}

type installerNoDispatchError struct{}

func (installerNoDispatchError) Error() string { return errDarwinInstallerFailed.Error() }

func (installer DarwinInstaller) Install(ctx context.Context) (Release, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if installer.VerifyPKG == nil || installer.LaunchInstaller == nil || installer.FindInstallation == nil ||
		installer.Cleanup == nil || installer.PollInterval <= 0 || installer.PollLimit <= 0 ||
		installer.PollLimit > maximumMacInstallPollDuration || installer.PollInterval > installer.PollLimit {
		return Release{}, errDarwinInstallerFailed
	}
	resolver := installer.resolveStage
	if resolver == nil {
		resolver = resolveAndStageMacPKG
	}
	after := installer.after
	if after == nil {
		after = time.After
	}

	lease, err := installer.Cleanup.Acquire()
	if err != nil {
		return Release{}, err
	}
	release, stage, stageErr := resolver(ctx, installer.HTTPClient)
	if stage != nil {
		if bindErr := lease.BindStage(stage); bindErr != nil {
			_ = lease.ContinueManagedCleanup()
			return Release{}, errMacCleanupPending
		}
	}
	if stageErr != nil || stage == nil {
		return installer.failBeforeDispatch(lease, errDarwinInstallerFailed)
	}
	if installer.VerifyPKG(ctx, stage) != nil {
		return installer.failBeforeDispatch(lease, errDarwinInstallerFailed)
	}
	// These two transaction-owned guards are intentionally distinct from the
	// verifier's internal path phases and from the native launcher's checks.
	if stage.Revalidate(ctx) != nil || stage.Revalidate(ctx) != nil {
		return installer.failBeforeDispatch(lease, errDarwinInstallerFailed)
	}

	session, launchErr := installer.LaunchInstaller(ctx, stage)
	if session != nil {
		if bindErr := lease.BindSession(session); bindErr != nil {
			_ = lease.ContinueManagedCleanup()
			return Release{}, errMacCleanupPending
		}
	}
	if session == nil {
		var noDispatch installerNoDispatchError
		if launchErr != nil && errors.As(launchErr, &noDispatch) {
			return installer.failBeforeDispatch(lease, errDarwinInstallerFailed)
		}
		_ = lease.ContinueManagedCleanup()
		return Release{}, errMacCleanupPending
	}
	if launchErr != nil {
		return installer.finalize(lease, release.Version, false, errDarwinInstallerFailed)
	}
	done := session.Done()
	if done == nil {
		lease.latchTerminal(installerWaitResult{}, false)
		return installer.finalize(lease, release.Version, false, errDarwinInstallerFailed)
	}

	if _, assessErr := assessDarwinInstallation(ctx, installer.FindInstallation); assessErr != nil {
		return installer.finalize(lease, release.Version, false, assessErr)
	}
	poll := after(installer.PollInterval)
	deadline := after(installer.PollLimit)
	for {
		// Give already-published terminal evidence priority over recurring timer
		// work so a busy poll source cannot indefinitely delay finalization.
		select {
		case terminal, ok := <-done:
			return installer.completeTerminal(ctx, lease, release.Version, terminal, ok)
		default:
		}
		select {
		case terminal, ok := <-done:
			return installer.completeTerminal(ctx, lease, release.Version, terminal, ok)
		case <-ctx.Done():
			return installer.finalize(lease, release.Version, false, errDarwinInstallerFailed)
		case <-deadline:
			return installer.finalize(lease, release.Version, false, errDarwinInstallerFailed)
		case <-poll:
			if _, assessErr := assessDarwinInstallation(ctx, installer.FindInstallation); assessErr != nil {
				return installer.finalize(lease, release.Version, false, assessErr)
			}
			poll = after(installer.PollInterval)
		}
	}
}

func (installer DarwinInstaller) completeTerminal(
	ctx context.Context,
	lease *installerCleanupLease,
	version string,
	terminal installerWaitResult,
	ok bool,
) (Release, error) {
	lease.latchTerminal(terminal, ok)
	valid := ok && validInstallerWaitResult(terminal)
	standalone, assessErr := assessDarwinInstallation(ctx, installer.FindInstallation)
	if assessErr != nil {
		return installer.finalize(lease, version, false, assessErr)
	}
	success := valid && terminal.Reason == installerTerminalNaturalZero && standalone
	return installer.finalize(lease, version, success, errDarwinInstallerFailed)
}

func (installer DarwinInstaller) failBeforeDispatch(
	lease *installerCleanupLease,
	primary error,
) (Release, error) {
	if lease == nil || lease.ReleaseBeforeDispatch() != nil {
		if lease != nil {
			_ = lease.ContinueManagedCleanup()
		}
		return Release{}, errMacCleanupPending
	}
	return Release{}, primary
}

func (installer DarwinInstaller) finalize(
	lease *installerCleanupLease,
	version string,
	success bool,
	primary error,
) (Release, error) {
	cleanupContext, cancel := context.WithTimeout(context.Background(), installerCleanupLimit)
	stop, stopErr := lease.stop(cleanupContext)
	cancel()
	if stopErr != nil || lease.ReleaseAfterNaturalQuiescence(stop) != nil {
		_ = lease.ContinueManagedCleanup()
		return Release{}, errMacCleanupPending
	}
	if success {
		return Release{Version: version}, nil
	}
	if errors.Is(primary, errTailscaleAppCleanup) {
		return Release{}, errTailscaleAppCleanup
	}
	return Release{}, errDarwinInstallerFailed
}

func assessDarwinInstallation(
	ctx context.Context,
	find func(context.Context) (DarwinInstallation, error),
) (bool, error) {
	installation, findErr := find(ctx)
	guard := installation.guard
	if guard == nil {
		if errors.Is(findErr, errTailscaleAppCleanup) {
			return false, errTailscaleAppCleanup
		}
		return false, nil
	}
	valid := findErr == nil && ctx.Err() == nil &&
		installation.BundlePath == fixedTailscaleBundlePath && installation.Executable == fixedTailscaleExecutablePath &&
		guard.BundlePath() == fixedTailscaleBundlePath && guard.ExecutablePath() == fixedTailscaleExecutablePath &&
		guard.Revalidate(ctx) == nil
	standalone := valid && installation.Variant == DarwinStandalone && installation.BundleID == "io.tailscale.ipn.macsys"
	appStore := valid && installation.Variant == DarwinAppStore && installation.BundleID == "io.tailscale.ipn.macos"
	closeErr := guard.Close()
	if closeErr != nil || findErr != nil {
		return false, errTailscaleAppCleanup
	}
	if !standalone && !appStore {
		return false, nil
	}
	return standalone, nil
}

func validInstallerWaitResult(result installerWaitResult) bool {
	switch result.Reason {
	case installerTerminalNaturalZero:
		return result.ExitCode == 0
	case installerTerminalNaturalNonzero:
		return result.ExitCode > 0
	case installerTerminalExternalSignal, installerTerminalControllerForced, installerTerminalMalformed:
		return result.ExitCode == -1
	default:
		return false
	}
}

func stopResultForWait(result installerWaitResult) installerStopResult {
	if !validInstallerWaitResult(result) {
		return installerStopResult{Terminal: installerTerminalMalformed}
	}
	return installerStopResult{
		Quiescent: result.Reason == installerTerminalNaturalZero,
		Terminal:  result.Reason,
	}
}
