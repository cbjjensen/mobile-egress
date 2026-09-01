//go:build darwin

package tailscale

import (
	"bytes"
	"errors"
	"io"
	"os/exec"
)

const maximumDarwinCommandOutput = 4 << 20

func runTailscaleCommandOutput(command *exec.Cmd) ([]byte, error) {
	var stdout bytes.Buffer
	budget := newTailscaleCommandOutputBudget(maximumDarwinCommandOutput, func() {
		cancelTailscaleCommand(command)
	})
	command.Stdout = budget.Writer(&stdout, nil)
	command.Stderr = budget.Writer(io.Discard, nil)
	err := command.Run()
	if budget.Exceeded() {
		return nil, errTailscaleCommandOutputLimit
	}
	if err != nil {
		return nil, errors.New("Tailscale command failed")
	}
	return append([]byte(nil), stdout.Bytes()...), nil
}

func runTailscaleStreamingCommandOutput(command *exec.Cmd, observe func([]byte)) ([]byte, error) {
	var output bytes.Buffer
	budget := newTailscaleCommandOutputBudget(maximumDarwinCommandOutput, func() {
		cancelTailscaleCommand(command)
	})
	command.Stdout = budget.Writer(&output, observe)
	command.Stderr = budget.Writer(&output, observe)
	err := command.Run()
	result := append([]byte(nil), output.Bytes()...)
	if budget.Exceeded() {
		return result, errTailscaleCommandOutputLimit
	}
	if err != nil {
		return result, errors.New("Tailscale command failed")
	}
	return result, nil
}

func cancelTailscaleCommand(command *exec.Cmd) {
	if command == nil {
		return
	}
	if command.Cancel != nil {
		_ = command.Cancel()
		return
	}
	if command.Process != nil {
		_ = command.Process.Kill()
	}
}
