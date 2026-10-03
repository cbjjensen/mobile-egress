//go:build !windows

package main

import (
	"context"
	"fmt"
	"io"
)

func runNodeService(run func(context.Context) error, stderr io.Writer) int {
	if err := runForegroundNodeService(run); err != nil {
		fmt.Fprintln(stderr, "mobile-egress-client serve:", err)
		return 1
	}
	return 0
}
