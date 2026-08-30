//go:build !windows

package main

import "io"

func runAsWindowsServiceIfNeeded(_, _ string, _ io.Writer) (bool, int) {
	return false, 0
}
