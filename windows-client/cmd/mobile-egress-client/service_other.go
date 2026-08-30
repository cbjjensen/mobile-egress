//go:build !windows

package main

import (
	"fmt"
	"io"

	"mobile-egress/windows-client/internal/nodeservice"
)

func runNodeService(repository *nodeservice.Repository, stderr io.Writer) int {
	if err := runForegroundNodeService(repository); err != nil {
		fmt.Fprintln(stderr, "mobile-egress-client serve:", err)
		return 1
	}
	return 0
}
