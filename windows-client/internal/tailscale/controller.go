package tailscale

import (
	"bytes"
	"context"
	"errors"
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
	executable            string
	resolver              installationResolver
	runner                CommandRunner
	funnelApprovalHandler func(string)
}

type installationResolver func(context.Context) (DarwinInstallation, error)

const resolverInstalledTimeout = 5 * time.Second

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

const maxFunnelApprovalOutput = 64 * 1024

func (controller *Controller) enableFunnel(ctx context.Context) error {
	_, canStream := controller.runner.(StreamingCommandRunner)
	if !canStream || controller.funnelApprovalHandler == nil {
		_, err := controller.run(ctx, FunnelArguments()...)
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
			approvalURL = findFunnelApprovalURL(approvalOutput)
			approvalOpened = approvalURL != ""
		}
		approvalMu.Unlock()
		if approvalURL != "" {
			controller.funnelApprovalHandler(approvalURL)
		}
	}

	_, err := controller.runStreaming(ctx, observe, FunnelArguments()...)
	return err
}

func (controller *Controller) Installed() bool {
	if controller == nil {
		return false
	}
	if controller.resolver != nil {
		ctx, cancel := context.WithTimeout(context.Background(), resolverInstalledTimeout)
		defer cancel()
		installation, err := controller.resolveInstallation(ctx)
		if err != nil {
			return false
		}
		return installation.guard.Close() == nil
	}
	if strings.TrimSpace(controller.executable) == "" {
		return false
	}
	info, err := os.Stat(controller.executable)
	return err == nil && info.Mode().IsRegular()
}

func (controller *Controller) Status(ctx context.Context) (Status, error) {
	if !controller.commandReady() {
		return Status{}, errors.New("Tailscale executable is unavailable")
	}
	output, err := controller.run(ctx, "status", "--json")
	if err != nil {
		if errors.Is(err, errTailscaleAppCleanup) {
			return Status{}, errTailscaleAppCleanup
		}
		return Status{}, errors.New("Tailscale status is unavailable")
	}
	status, err := ParseStatus(output)
	if err != nil {
		return Status{}, err
	}
	funnelOutput, funnelErr := controller.run(ctx, "funnel", "status", "--json")
	if funnelErr != nil {
		if errors.Is(funnelErr, errTailscaleAppCleanup) {
			return Status{}, errTailscaleAppCleanup
		}
		return status, nil
	}
	status.FunnelReady, err = ParseFunnelStatus(funnelOutput, status.FQDN)
	if err != nil {
		return Status{}, err
	}
	return status, nil
}

func (controller *Controller) Connect(ctx context.Context) (Status, error) {
	if !controller.commandReady() || !controller.Installed() {
		return Status{}, errors.New("Tailscale executable is unavailable")
	}
	if _, err := controller.Status(ctx); err != nil {
		if errors.Is(err, errTailscaleAppCleanup) {
			return Status{}, errTailscaleAppCleanup
		}
		if _, loginErr := controller.run(ctx, "login"); loginErr != nil {
			if errors.Is(loginErr, errTailscaleAppCleanup) {
				return Status{}, errTailscaleAppCleanup
			}
			return Status{}, errors.New("Tailscale browser login failed or was cancelled")
		}
	}
	if _, err := controller.run(ctx, upArguments()...); err != nil {
		if errors.Is(err, errTailscaleAppCleanup) {
			return Status{}, errTailscaleAppCleanup
		}
		return Status{}, errors.New(upFailureMessage())
	}
	status, err := controller.Status(ctx)
	if errors.Is(err, errTailscaleAppCleanup) {
		return Status{}, errTailscaleAppCleanup
	}
	if err != nil || !status.Online {
		return Status{}, errors.New("Tailscale did not become online")
	}
	return status, nil
}

func (controller *Controller) Enable(ctx context.Context) (Status, error) {
	if !controller.commandReady() {
		return Status{}, errors.New("Tailscale executable is unavailable")
	}
	if _, err := controller.Status(ctx); err != nil {
		if errors.Is(err, errTailscaleAppCleanup) {
			return Status{}, errTailscaleAppCleanup
		}
		if _, loginErr := controller.run(ctx, "login"); loginErr != nil {
			if errors.Is(loginErr, errTailscaleAppCleanup) {
				return Status{}, errTailscaleAppCleanup
			}
			return Status{}, errors.New("Tailscale browser login failed or was cancelled")
		}
	}
	if _, err := controller.run(ctx, upArguments()...); err != nil {
		if errors.Is(err, errTailscaleAppCleanup) {
			return Status{}, errTailscaleAppCleanup
		}
		return Status{}, errors.New(upFailureMessage())
	}
	if err := controller.enableFunnel(ctx); err != nil {
		if errors.Is(err, errTailscaleAppCleanup) {
			return Status{}, errTailscaleAppCleanup
		}
		return Status{}, errors.New("Tailscale raw TCP Funnel setup failed")
	}
	status, err := controller.Status(ctx)
	if errors.Is(err, errTailscaleAppCleanup) {
		return Status{}, errTailscaleAppCleanup
	}
	if err != nil || !status.FunnelReady {
		return Status{}, errors.New("Tailscale raw TCP Funnel status did not match the loopback relay")
	}
	return status, nil
}

func (controller *Controller) commandReady() bool {
	return controller != nil && controller.runner != nil &&
		(controller.resolver != nil || strings.TrimSpace(controller.executable) != "")
}

func (controller *Controller) run(ctx context.Context, arguments ...string) ([]byte, error) {
	if controller.resolver == nil {
		return controller.runner.Run(ctx, controller.executable, arguments...)
	}
	installation, err := controller.resolveInstallation(ctx)
	if err != nil {
		return nil, err
	}
	output, runErr := controller.runner.Run(ctx, fixedTailscaleExecutablePath, arguments...)
	if installation.guard.Close() != nil {
		return nil, errTailscaleAppCleanup
	}
	return output, runErr
}

func (controller *Controller) runStreaming(ctx context.Context, observe func([]byte), arguments ...string) ([]byte, error) {
	streamingRunner, ok := controller.runner.(StreamingCommandRunner)
	if !ok {
		return nil, errors.New("Tailscale command failed")
	}
	if controller.resolver == nil {
		return streamingRunner.RunStreaming(ctx, controller.executable, observe, arguments...)
	}
	installation, err := controller.resolveInstallation(ctx)
	if err != nil {
		return nil, err
	}
	output, runErr := streamingRunner.RunStreaming(ctx, fixedTailscaleExecutablePath, observe, arguments...)
	if installation.guard.Close() != nil {
		return nil, errTailscaleAppCleanup
	}
	return output, runErr
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
		return DarwinInstallation{}, rejectControllerInstallation(installation, err)
	}
	if !validControllerInstallation(installation) || installation.guard.Revalidate(ctx) != nil || ctx.Err() != nil {
		return DarwinInstallation{}, rejectControllerInstallation(installation, errTailscaleAppVerification)
	}
	return installation, nil
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
