//go:build darwin && !capacityharness

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"mobile-egress/windows-client/internal/mackeychainharness"
)

func main() {
	profile := flag.String("profile", "", "absolute path to a Developer ID distribution provisioning profile")
	identity := flag.String("identity", "", "exact Developer ID Application identity label")
	flag.Parse()
	repositoryRoot, err := filepath.Abs(".")
	if err == nil {
		err = mackeychainharness.Run(context.Background(), mackeychainharness.ExecRunner{}, mackeychainharness.Config{
			RepositoryRoot: repositoryRoot,
			ProfilePath:    *profile,
			Identity:       *identity,
			Output:         os.Stdout,
		})
	}
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
