//go:build !windows

package tailscale

import "os/exec"

func configureBackgroundCommand(_ *exec.Cmd) {}
