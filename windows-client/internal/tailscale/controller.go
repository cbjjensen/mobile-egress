package tailscale

import (
	"context"
	"errors"
	"os/exec"
	"strings"
)

type CommandRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, executable string, arguments ...string) ([]byte, error) {
	output, err := exec.CommandContext(ctx, executable, arguments...).Output()
	if err != nil {
		return nil, errors.New("Tailscale command failed")
	}
	return output, nil
}

type Controller struct {
	executable string
	runner     CommandRunner
}

func NewController(executable string, runner CommandRunner) *Controller {
	return &Controller{executable: executable, runner: runner}
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
	if _, err := controller.runner.Run(ctx, controller.executable, FunnelArguments()...); err != nil {
		return Status{}, errors.New("Tailscale raw TCP Funnel setup failed")
	}
	status, err := controller.Status(ctx)
	if err != nil || !status.FunnelReady {
		return Status{}, errors.New("Tailscale raw TCP Funnel status did not match the loopback relay")
	}
	return status, nil
}
