//go:build !darwin

package tailscale

import (
	"errors"
	"fmt"
	"os/exec"
)

func runTailscaleCommandOutput(command *exec.Cmd) ([]byte, error) {
	output, err := command.Output()
	if err != nil {
		return nil, errors.New("Tailscale command failed")
	}
	return output, nil
}

func runTailscaleStreamingCommandOutput(command *exec.Cmd, observe func([]byte)) ([]byte, error) {
	output := &streamingOutput{observe: observe}
	command.Stdout = output
	command.Stderr = output
	if err := command.Run(); err != nil {
		return output.Bytes(), fmt.Errorf("Tailscale command failed: %w", err)
	}
	return output.Bytes(), nil
}
