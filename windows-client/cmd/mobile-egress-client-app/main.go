package main

import (
	"fmt"
	"os"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--runtime-mode" {
		fmt.Println(runtimeMode)
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Println(version)
		return
	}
	if err := checkGUIPrivileges(); err != nil {
		showStartupError(err)
		fmt.Fprintln(os.Stderr, "Inevitable Mobile Relay could not start:", err)
		os.Exit(1)
	}
	if err := runApp(); err != nil {
		showStartupError(err)
		fmt.Fprintln(os.Stderr, "Inevitable Mobile Relay could not start:", err)
		os.Exit(1)
	}
}
