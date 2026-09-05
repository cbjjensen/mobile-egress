package tailscale

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type CommandRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type StreamingCommandRunner interface {
	RunStreaming(context.Context, string, func([]byte), ...string) ([]byte, error)
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, executable string, arguments ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, executable, arguments...)
	configureBackgroundCommand(command)
	configureTailscaleCommand(command)
	return runTailscaleCommandOutput(command)
}

func (ExecRunner) RunStreaming(ctx context.Context, executable string, observe func([]byte), arguments ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, executable, arguments...)
	configureBackgroundCommand(command)
	configureTailscaleCommand(command)
	return runTailscaleStreamingCommandOutput(command, observe)
}

type streamingOutput struct {
	mu      sync.Mutex
	buffer  bytes.Buffer
	observe func([]byte)
}

func (output *streamingOutput) Write(chunk []byte) (int, error) {
	copyOfChunk := append([]byte(nil), chunk...)
	output.mu.Lock()
	_, _ = output.buffer.Write(copyOfChunk)
	output.mu.Unlock()
	if output.observe != nil {
		output.observe(copyOfChunk)
	}
	return len(chunk), nil
}

func (output *streamingOutput) Bytes() []byte {
	output.mu.Lock()
	defer output.mu.Unlock()
	return append([]byte(nil), output.buffer.Bytes()...)
}

type Controller struct {
	guard                 appExecutionGuard // shared only within one operation, never cached
	executable            string
	resolver              installationResolver
	runner                CommandRunner
	funnelApprovalHandler func(string)
}

type installationResolver func(context.Context) (DarwinInstallation, error)

const resolverInstalledTimeout = 5 * time.Second

var ErrNotInstalled = errors.New("Tailscale is not installed")

// CheckInstalled distinguishes absence from a failed application verification.
func (controller *Controller) CheckInstalled(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if controller == nil {
		return ErrNotInstalled
	}
	if controller.guard != nil {
		return controller.guard.Revalidate(ctx)
	}
	if controller.resolver != nil {
		installation, err := controller.resolveInstallation(ctx)
		if err != nil {
			return err
		}
		if installation.guard.Close() != nil {
			return errTailscaleAppCleanup
		}
		return nil
	}
	if controller.executable == "" {
		return ErrNotInstalled
	}
	info, err := os.Stat(controller.executable)
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotInstalled
	}
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("Tailscale executable could not be checked")
	}
	return nil
}

func (controller *Controller) operation(ctx context.Context, work func(*Controller) (Status, error)) (status Status, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if controller == nil {
		return Status{}, ErrNotInstalled
	}
	if controller.resolver == nil || controller.guard != nil {
		return work(controller)
	}
	installation, err := controller.resolveInstallation(ctx)
	if err != nil {
		return Status{}, err
	}
	defer func() {
		if installation.guard.Close() != nil {
			status = Status{}
			err = errTailscaleAppCleanup
		}
	}()
	session := *controller
	session.guard = installation.guard
	session.executable = fixedTailscaleExecutablePath
	return work(&session)
}

func NewController(executable string, runner CommandRunner) *Controller {
	return &Controller{executable: executable, runner: runner}
}

func newResolverController(resolver installationResolver, runner CommandRunner) *Controller {
	return &Controller{resolver: resolver, runner: runner}
}

func (controller *Controller) SetFunnelApprovalHandler(handler func(string)) {
	if controller != nil {
		controller.funnelApprovalHandler = handler
	}
}

func findFunnelApprovalURL(output []byte) string {
	for _, field := range strings.Fields(string(output)) {
		candidate := strings.Trim(field, `"'()[]<>.,`)
		parsed, err := url.Parse(candidate)
		if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.User != nil || parsed.Fragment != "" {
			continue
		}
		if !strings.EqualFold(parsed.Hostname(), "login.tailscale.com") || parsed.Port() != "" || parsed.Path != "/f/funnel" {
			continue
		}
		if strings.TrimSpace(parsed.Query().Get("node")) == "" {
			continue
		}
		return parsed.String()
	}
	return ""
}

func findLoginApprovalURL(output []byte) string {
	// Do not open a token from a partial streaming chunk.
	lastSeparator := strings.LastIndexAny(string(output), " \t\r\n")
	if lastSeparator < 0 {
		return ""
	}
	for _, candidate := range strings.Fields(string(output[:lastSeparator+1])) {
		parsed, err := url.Parse(candidate)
		if err != nil || parsed.Scheme != "https" || parsed.Host != "login.tailscale.com" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || !strings.HasPrefix(parsed.Path, "/a/") {
			continue
		}
		token := strings.TrimPrefix(parsed.Path, "/a/")
		if token == "" || strings.ContainsFunc(token, func(c rune) bool { return !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') }) {
			continue
		}
		return parsed.String()
	}
	return ""
}

const maxFunnelApprovalOutput = 64 * 1024

func (controller *Controller) enableFunnel(ctx context.Context) error {
	return controller.runWithApproval(ctx, findFunnelApprovalURL, FunnelArguments()...)
}

func (controller *Controller) runWithApproval(ctx context.Context, findApproval func([]byte) string, arguments ...string) error {
	_, canStream := controller.runner.(StreamingCommandRunner)
	if !canStream || controller.funnelApprovalHandler == nil {
		_, err := controller.run(ctx, arguments...)
		return err
	}

	var approvalOutput []byte
	var approvalMu sync.Mutex
	approvalOpened := false
	observe := func(chunk []byte) {
		approvalMu.Lock()
		remaining := maxFunnelApprovalOutput - len(approvalOutput)
		if remaining > len(chunk) {
			remaining = len(chunk)
		}
		if remaining > 0 {
			approvalOutput = append(approvalOutput, chunk[:remaining]...)
		}
		approvalURL := ""
		if !approvalOpened {
			// A pipe read can split inside a login token or Funnel node ID.
			// Wait for a delimiter before opening a URL, once, in the browser.
			if end := bytes.LastIndexAny(approvalOutput, " \t\r\n"); end >= 0 {
				approvalURL = findApproval(approvalOutput[:end+1])
			}
			approvalOpened = approvalURL != ""
		}
		approvalMu.Unlock()
		if approvalURL != "" {
			controller.funnelApprovalHandler(approvalURL)
		}
	}

	_, err := controller.runStreaming(ctx, observe, arguments...)
	return err
}

func (controller *Controller) Installed() bool {
	ctx, cancel := context.WithTimeout(context.Background(), resolverInstalledTimeout)
	defer cancel()
	return controller.CheckInstalled(ctx) == nil
}

func (controller *Controller) Status(ctx context.Context) (Status, error) {
	return controller.operation(ctx, func(session *Controller) (Status, error) { return session.status(ctx) })
}

// Inspect performs one installation/status check for UI consumers.
func (controller *Controller) Inspect(ctx context.Context) (Status, error) {
	if controller == nil {
		return Status{}, ErrNotInstalled
	}
	if controller.resolver == nil {
		if err := controller.CheckInstalled(ctx); err != nil {
			return Status{}, err
		}
	}
	return controller.Status(ctx)
}

func (controller *Controller) status(ctx context.Context) (Status, error) {
	if !controller.commandReady() {
		return Status{}, errors.New("Tailscale executable is unavailable")
	}
	output, err := controller.run(ctx, "status", "--json")
	if err != nil {
		if errors.Is(err, errTailscaleAppCleanup) {
			return Status{}, errTailscaleAppCleanup
		}
		return Status{Installed: true}, commandFailure(ctx, "Tailscale status could not be read. Open Tailscale and check its VPN and system-extension approvals", err)
	}
	status, err := ParseStatus(output)
	if err != nil {
		status.Installed = true
		return status, err
	}
	funnelOutput, funnelErr := controller.run(ctx, "funnel", "status", "--json")
	if funnelErr != nil {
		if errors.Is(funnelErr, errTailscaleAppCleanup) {
			return Status{}, errTailscaleAppCleanup
		}
		return status, commandFailure(ctx, "Tailscale is connected, but Funnel status could not be read", funnelErr)
	}
	status.FunnelReady, err = ParseFunnelStatus(funnelOutput, status.FQDN)
	if err != nil {
		return status, err
	}
	return status, nil
}

func (controller *Controller) Connect(ctx context.Context) (Status, error) {
	return controller.operation(ctx, func(session *Controller) (Status, error) { return session.connect(ctx) })
}

func (controller *Controller) connect(ctx context.Context) (Status, error) {
	if err := controller.CheckInstalled(ctx); err != nil {
		return Status{}, err
	}
	if err := controller.ensureConnected(ctx); err != nil {
		return Status{}, err
	}
	status, err := controller.Status(ctx)
	if err != nil {
		return status, fmt.Errorf("Check Tailscale after connecting: %w", err)
	}
	if !status.Online {
		return Status{}, errors.New("Tailscale did not become online")
	}
	return status, nil
}

func (controller *Controller) Enable(ctx context.Context) (Status, error) {
	return controller.operation(ctx, func(session *Controller) (Status, error) { return session.enable(ctx) })
}

func (controller *Controller) enable(ctx context.Context) (Status, error) {
	if !controller.commandReady() {
		return Status{}, errors.New("Tailscale executable is unavailable")
	}
	if err := controller.ensureConnected(ctx); err != nil {
		return Status{}, err
	}
	if err := controller.enableFunnel(ctx); err != nil {
		if errors.Is(err, errTailscaleAppCleanup) {
			return Status{}, errTailscaleAppCleanup
		}
		return Status{}, commandFailure(ctx, "Tailscale Funnel setup failed. Complete the Funnel browser approval for this device", err)
	}
	status, err := controller.Status(ctx)
	if err != nil {
		return status, fmt.Errorf("Check Tailscale after enabling Funnel: %w", err)
	}
	if !status.FunnelReady {
		return Status{}, errors.New("Tailscale raw TCP Funnel status did not match the loopback relay")
	}
	return status, nil
}

func (controller *Controller) commandReady() bool {
	return controller != nil && controller.runner != nil &&
		(controller.resolver != nil || strings.TrimSpace(controller.executable) != "")
}

func (controller *Controller) run(ctx context.Context, arguments ...string) ([]byte, error) {
	if controller.guard != nil && controller.guard.Revalidate(ctx) != nil {
		return nil, errTailscaleAppVerification
	}
	if controller.resolver != nil && controller.guard == nil {
		return nil, errTailscaleAppVerification
	}
	return controller.runner.Run(ctx, controller.executable, arguments...)
}

func (controller *Controller) runStreaming(ctx context.Context, observe func([]byte), arguments ...string) ([]byte, error) {
	streamingRunner, ok := controller.runner.(StreamingCommandRunner)
	if !ok {
		return nil, errors.New("Tailscale command failed")
	}
	if controller.guard != nil && controller.guard.Revalidate(ctx) != nil {
		return nil, errTailscaleAppVerification
	}
	if controller.resolver != nil && controller.guard == nil {
		return nil, errTailscaleAppVerification
	}
	return streamingRunner.RunStreaming(ctx, controller.executable, observe, arguments...)
}

func (controller *Controller) resolveInstallation(ctx context.Context) (DarwinInstallation, error) {
	if controller == nil || controller.resolver == nil {
		return DarwinInstallation{}, errTailscaleAppVerification
	}
	if ctx == nil {
		ctx = context.Background()
	}
	installation, err := controller.resolver(ctx)
	if err != nil {
		if errors.Is(err, ErrNotInstalled) && installation.guard == nil {
			return DarwinInstallation{}, ErrNotInstalled
		}
		if ctx.Err() != nil && installation.guard == nil {
			return DarwinInstallation{}, ctx.Err()
		}
		return DarwinInstallation{}, rejectControllerInstallation(installation, err)
	}
	if !validControllerInstallation(installation) || installation.guard.Revalidate(ctx) != nil || ctx.Err() != nil {
		return DarwinInstallation{}, rejectControllerInstallation(installation, errTailscaleAppVerification)
	}
	return installation, nil
}

// Never display raw command output; it can contain login URLs or credentials.
func commandFailure(ctx context.Context, stage string, cause error) error {
	if errors.Is(cause, errTailscaleAppVerification) || errors.Is(cause, errTailscaleAppCleanup) {
		return cause
	}
	if ctx.Err() != nil {
		return fmt.Errorf("%s: %w", stage, ctx.Err())
	}
	return errors.New(stage)
}

func validControllerInstallation(installation DarwinInstallation) bool {
	if installation.guard == nil || installation.BundlePath != fixedTailscaleBundlePath ||
		installation.Executable != fixedTailscaleExecutablePath ||
		installation.guard.BundlePath() != fixedTailscaleBundlePath ||
		installation.guard.ExecutablePath() != fixedTailscaleExecutablePath {
		return false
	}
	switch installation.Variant {
	case DarwinStandalone:
		return installation.BundleID == "io.tailscale.ipn.macsys"
	case DarwinAppStore:
		return installation.BundleID == "io.tailscale.ipn.macos"
	default:
		return false
	}
}

func rejectControllerInstallation(installation DarwinInstallation, cause error) error {
	if installation.guard != nil && installation.guard.Close() != nil {
		return errTailscaleAppCleanup
	}
	if errors.Is(cause, errTailscaleAppCleanup) {
		return errTailscaleAppCleanup
	}
	return errTailscaleAppVerification
}

// A failed check is not evidence that the user needs to log in again.
func (controller *Controller) ensureConnected(ctx context.Context) error {
	status, err := controller.Status(ctx)
	if errors.Is(err, errTailscaleAppVerification) || errors.Is(err, errTailscaleAppCleanup) {
		return err
	}
	if err != nil && !status.Online {
		if !errors.Is(err, ErrNotOnline) {
			return err
		}
		if status.BackendState == "NeedsLogin" {
			if err := controller.runWithApproval(ctx, findLoginApprovalURL, "login"); err != nil {
				return commandFailure(ctx, "Tailscale browser login failed or was cancelled. Open Tailscale to finish sign-in and VPN approvals", err)
			}
		}
	}
	if err := controller.runWithApproval(ctx, findLoginApprovalURL, upArguments()...); err != nil {
		return commandFailure(ctx, upFailureMessage(), err)
	}
	return nil
}
