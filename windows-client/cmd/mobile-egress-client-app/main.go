package main

import (
	"fmt"
	"os"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Println(version)
		return
	}
	if err := checkGUIPrivileges(); err != nil {
		fmt.Fprintln(os.Stderr, "Mobile Egress Client could not start:", err)
		os.Exit(1)
	}
	if err := runApp(); err != nil {
		fmt.Fprintln(os.Stderr, "Mobile Egress Client could not start:", err)
		os.Exit(1)
	}
}
