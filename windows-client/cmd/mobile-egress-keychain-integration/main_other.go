//go:build !darwin

package main

import (
	"fmt"
	"os"
)

func main() {
	_, _ = fmt.Fprintln(os.Stderr, "signed macOS Keychain integration harness is available only on macOS")
	os.Exit(1)
}
