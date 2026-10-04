//go:build !windows && !darwin

package clientapp

import (
	"context"
	"errors"
)

// Unsupported hosts never report a firewall exception as configured.
const hostFirewallScope = "port"

func configureHostFirewall(context.Context, uint16) error {
	return errors.New("host firewall is unsupported")
}
func inspectHostFirewall(_ context.Context, port uint16, _ bool) FirewallStatus {
	return firewallResult("unavailable", hostFirewallScope, port)
}
