//go:build darwin

package main

import (
	"os"

	"mobile-egress/windows-client/internal/desktop"
)

func main() {
	if desktop.Run() != nil {
		os.Exit(1)
	}
}
