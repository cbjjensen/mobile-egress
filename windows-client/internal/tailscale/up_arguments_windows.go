//go:build windows

package tailscale

func upArguments() []string {
	return []string{"up", "--unattended=true"}
}

func upFailureMessage() string {
	return "Tailscale login or unattended setup failed"
}
