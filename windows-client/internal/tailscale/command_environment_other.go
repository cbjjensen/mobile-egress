//go:build !darwin

package tailscale

import "os/exec"

func configureTailscaleCommand(_ *exec.Cmd) {}
