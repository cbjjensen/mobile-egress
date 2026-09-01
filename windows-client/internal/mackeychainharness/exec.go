package mackeychainharness

import (
	"bytes"
	"context"
	"os"
	"os/exec"
)

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, command Command) (CommandResult, error) {
	process := exec.CommandContext(ctx, command.Name, command.Args...)
	process.Dir = command.Dir
	process.Env = append(os.Environ(), command.Env...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	process.Stdout = &stdout
	process.Stderr = &stderr
	err := process.Run()
	return CommandResult{Stdout: stdout.String(), Stderr: stderr.String()}, err
}
