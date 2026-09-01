//go:build capacityharness

package mackeychainharness

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"time"
)

func (ExecRunner) RunAttached(ctx context.Context, command AttachedCommand) error {
	if command.Stdin == nil || command.Stdout == nil || command.Stderr == nil || command.GracePeriod <= 0 ||
		len(command.ExtraFiles) != 1 || command.ExtraFiles[0] == nil || command.OnStarted == nil {
		return errors.New("attached command streams and cleanup grace are required")
	}
	process := exec.Command(command.Name, command.Args...)
	process.Dir = command.Dir
	process.Env = append(os.Environ(), command.Env...)
	process.Stdin = command.Stdin
	process.Stdout = command.Stdout
	process.Stderr = command.Stderr
	process.ExtraFiles = command.ExtraFiles
	if err := process.Start(); err != nil {
		return err
	}
	return runStartedAttachedProcess(ctx, execAttachedProcess{command: process}, command.GracePeriod, command.OnStarted)
}

func runStartedAttachedProcess(ctx context.Context, attached attachedProcess, grace time.Duration, onStarted func() error) error {
	if err := ctx.Err(); err != nil {
		stopAttachedProcess(attached, grace)
		return err
	}
	if err := onStarted(); err != nil {
		stopAttachedProcess(attached, grace)
		return err
	}
	return waitForAttachedProcess(ctx, attached, grace)
}

func stopAttachedProcess(process attachedProcess, grace time.Duration) {
	stopCtx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = waitForAttachedProcess(stopCtx, process, grace)
}

type execAttachedProcess struct {
	command *exec.Cmd
}

func (process execAttachedProcess) Signal(signal os.Signal) error {
	return process.command.Process.Signal(signal)
}

func (process execAttachedProcess) Kill() error {
	return process.command.Process.Kill()
}

func (process execAttachedProcess) Wait() error {
	return process.command.Wait()
}
