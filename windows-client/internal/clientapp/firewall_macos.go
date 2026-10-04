package clientapp

import (
	"context"
	"strings"
)

const macFirewallTool = "/usr/libexec/ApplicationFirewall/socketfilterfw"
const macFirewallDaemon = "/Library/Application Support/MobileEgressClient/bin/mobile-egress-client"

// The runner and identity check are injected so policy behavior is testable
// without administrator rights or touching the developer machine's firewall.
func macFirewall(ctx context.Context, port uint16, retry bool, validate func(context.Context) error, run firewallRunner) FirewallStatus {
	result := func(state string) FirewallStatus { return firewallResult(state, "application", port) }
	if validate(ctx) != nil {
		return result("unavailable")
	}
	query := func(args ...string) (string, error) {
		output, err := run(ctx, macFirewallTool, args, []string{"LC_ALL=C", "LANG=C"})
		return strings.TrimSpace(string(output)), err
	}
	globalState := func() string {
		global, err := query("--getglobalstate")
		if err != nil {
			return "unavailable"
		}
		switch global {
		case "Firewall is disabled. (State = 0)":
			return "disabled"
		case "Firewall is enabled. (State = 1)":
		default:
			return "unknown"
		}
		block, err := query("--getblockall")
		if err != nil {
			return "unavailable"
		}
		switch block {
		case "Firewall has block all state set to enabled.":
			return "blocked"
		case "Firewall has block all state set to disabled.":
			return ""
		default:
			return "unknown"
		}
	}
	if state := globalState(); state != "" {
		return result(state)
	}
	if retry {
		if _, err := query("--add", macFirewallDaemon); err != nil {
			return result("unavailable")
		}
		if _, err := query("--unblockapp", macFirewallDaemon); err != nil {
			return result("unavailable")
		}
		// Managed or global policy can override an apparently successful mutation.
		if state := globalState(); state != "" {
			return result(state)
		}
	}
	app, err := query("--getappblocked", macFirewallDaemon)
	if err != nil {
		return result("unavailable")
	}
	// Accept only known complete replies. Unsupported/localized text is unknown.
	switch app {
	case "The application is not blocked", "The application is not blocked.", "Incoming connection to " + macFirewallDaemon + " is permitted.":
		return result("allowed")
	case "The application is blocked", "The application is blocked.":
		return result("blocked")
	default:
		return result("unknown")
	}
}
