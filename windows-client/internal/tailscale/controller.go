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
	output, err := command.Output()
	if err != nil {
		return nil, errors.New("Tailscale command failed")
	}
	return output, nil
}

func (ExecRunner) RunStreaming(ctx context.Context, executable string, observe func([]byte), arguments ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, executable, arguments...)
	configureBackgroundCommand(command)
	output := &streamingOutput{observe: observe}
	command.Stdout = output
	command.Stderr = output
	if err := command.Run(); err != nil {
		return output.Bytes(), fmt.Errorf("Tailscale command failed: %w", err)
	}
	return output.Bytes(), nil
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
	runner                CommandRunner
	funnelApprovalHandler func(string)
}

func NewController(executable string, runner CommandRunner) *Controller {
	return &Controller{executable: executable, runner: runner}
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
	streamingRunner, canStream := controller.runner.(StreamingCommandRunner)
	if !canStream || controller.funnelApprovalHandler == nil {
		_, err := controller.runner.Run(ctx, controller.executable, FunnelArguments()...)
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

	_, err := streamingRunner.RunStreaming(ctx, controller.executable, observe, FunnelArguments()...)
	return err
}

func (controller *Controller) Installed() bool {
	if controller == nil || strings.TrimSpace(controller.executable) == "" {
		return false
	}
	info, err := os.Stat(controller.executable)
	return err == nil && info.Mode().IsRegular()
}

func (controller *Controller) Status(ctx context.Context) (Status, error) {
	if controller == nil || controller.runner == nil || strings.TrimSpace(controller.executable) == "" {
		return Status{}, errors.New("Tailscale executable is unavailable")
	}
	output, err := controller.runner.Run(ctx, controller.executable, "status", "--json")
	if err != nil {
		return Status{}, errors.New("Tailscale status is unavailable")
	}
	status, err := ParseStatus(output)
	if err != nil {
		return Status{}, err
	}
	funnelOutput, funnelErr := controller.runner.Run(ctx, controller.executable, "funnel", "status", "--json")
	if funnelErr != nil {
		return status, nil
	}
	status.FunnelReady, err = ParseFunnelStatus(funnelOutput, status.FQDN)
	if err != nil {
		return Status{}, err
	}
	return status, nil
}

func (controller *Controller) Connect(ctx context.Context) (Status, error) {
	if controller == nil || controller.runner == nil || !controller.Installed() {
		return Status{}, errors.New("Tailscale executable is unavailable")
	}
	if _, err := controller.Status(ctx); err != nil {
		if _, loginErr := controller.runner.Run(ctx, controller.executable, "login"); loginErr != nil {
			return Status{}, errors.New("Tailscale browser login failed or was cancelled")
		}
	}
	if _, err := controller.runner.Run(ctx, controller.executable, UnattendedArguments()...); err != nil {
		return Status{}, errors.New("Tailscale login or unattended setup failed")
	}
	status, err := controller.Status(ctx)
	if err != nil || !status.Online {
		return Status{}, errors.New("Tailscale did not become online")
	}
	return status, nil
}

func (controller *Controller) Enable(ctx context.Context) (Status, error) {
	if controller == nil || controller.runner == nil || strings.TrimSpace(controller.executable) == "" {
		return Status{}, errors.New("Tailscale executable is unavailable")
	}
	if _, err := controller.Status(ctx); err != nil {
		if _, loginErr := controller.runner.Run(ctx, controller.executable, "login"); loginErr != nil {
			return Status{}, errors.New("Tailscale browser login failed or was cancelled")
		}
	}
	if _, err := controller.runner.Run(ctx, controller.executable, UnattendedArguments()...); err != nil {
		return Status{}, errors.New("Tailscale login or unattended setup failed")
	}
	if err := controller.enableFunnel(ctx); err != nil {
		return Status{}, errors.New("Tailscale raw TCP Funnel setup failed")
	}
	status, err := controller.Status(ctx)
	if err != nil || !status.FunnelReady {
		return Status{}, errors.New("Tailscale raw TCP Funnel status did not match the loopback relay")
	}
	return status, nil
}
