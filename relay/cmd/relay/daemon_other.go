//go:build !darwin

package main

import "mobile-egress/relay/internal/adminservice"

func runPlatformDaemon() (adminservice.RunResult, error) {
	return adminservice.RunResult{}, errDaemonUnavailable
}
