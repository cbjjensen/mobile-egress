//go:build darwin

package clientapp

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const hostFirewallScope = "application"

func runFirewallCommand(ctx context.Context, name string, args, env []string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	command.Env = append(os.Environ(), env...)
	command.WaitDelay = 100 * time.Millisecond
	var output firewallOutput
	command.Stdout, command.Stderr = &output, &output
	err := command.Run()
	return output.data, err
}

func validateFirewallDaemon(ctx context.Context) error {
	if os.Geteuid() != 0 {
		return errors.New("firewall management requires the installed daemon")
	}
	executable, err := os.Executable()
	if err != nil || filepath.Clean(executable) != macFirewallDaemon {
		return errors.New("invalid daemon executable")
	}
	for _, directory := range []string{"/Library", "/Library/Application Support", "/Library/Application Support/MobileEgressClient", "/Library/Application Support/MobileEgressClient/bin"} {
		if err := protectedRootPath(directory, true); err != nil {
			return err
		}
	}
	if err := protectedRootPath(macFirewallDaemon, false); err != nil {
		return err
	}
	info, err := os.Lstat(macFirewallDaemon)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		return errors.New("invalid daemon file")
	}
	_, err = runFirewallCommand(ctx, "/usr/bin/codesign", []string{"--verify", "--strict", "-R", `identifier "com.zfnf.mobile-egress.client" and anchor apple generic`, macFirewallDaemon}, nil)
	return err
}

func inspectHostFirewall(ctx context.Context, port uint16, retry bool) FirewallStatus {
	return macFirewall(ctx, port, retry, validateFirewallDaemon, runFirewallCommand)
}

func configureHostFirewall(ctx context.Context, port uint16) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	status := inspectHostFirewall(ctx, port, true)
	if status.State == "allowed" || status.State == "disabled" {
		return nil
	}
	return errors.New(status.Message)
}
