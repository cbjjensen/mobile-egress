package main

import (
	"context"
	"os"

	"mobile-egress/windows-client/internal/tailscale"
)

func main() {
	executable := os.Getenv("MOBILE_EGRESS_TEST_TAILSCALE_EXE")
	if executable == "" {
		os.Exit(2)
	}
	if _, err := (tailscale.ExecRunner{}).Run(
		context.Background(),
		executable,
		"status",
		"--json",
	); err != nil {
		os.Exit(1)
	}
}
