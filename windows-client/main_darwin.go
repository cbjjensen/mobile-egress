//go:build darwin

package main

import (
	"fmt"
	"os"

	"mobile-egress/windows-client/internal/desktop"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		_, _ = fmt.Fprintln(os.Stdout, desktop.ControllerVersion())
		return
	}
	if desktop.Run() != nil {
		os.Exit(1)
	}
}
