//go:build darwin

package tailscale

import (
	"os"
	"os/exec"
)

func configureTailscaleCommand(command *exec.Cmd) {
	if command == nil {
		return
	}
	base := command.Env
	if base == nil {
		base = os.Environ()
	}
	command.Env = mergeTailscaleEnvironment(base)
}
