//go:build !windows && !darwin

package tailscale

func upArguments() []string {
	return []string{"up"}
}

func upFailureMessage() string {
	return "Tailscale login or setup failed"
}
