//go:build capacityharness

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"mobile-egress/windows-client/internal/mackeychainharness"
)

type commandDependencies struct {
	runner       mackeychainharness.AttachedRunner
	runKeychain  func(context.Context, mackeychainharness.Runner, mackeychainharness.Config) error
	runCapacity  func(context.Context, mackeychainharness.AttachedRunner, mackeychainharness.SignedCapacityConfig) error
	protectInput func(io.Reader) (capacityInputProtection, error)
}

type capacityInputProtection interface {
	PrepareHandoff() error
	Restore() error
}

type noopCapacityInputProtection struct{}

func (noopCapacityInputProtection) PrepareHandoff() error { return nil }
func (noopCapacityInputProtection) Restore() error        { return nil }

func execute(ctx context.Context, arguments []string, stdin io.Reader, stdout, stderr io.Writer, dependencies commandDependencies) int {
	flags := flag.NewFlagSet("mobile-egress-keychain-integration", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	profile := flags.String("profile", "", "absolute path to a Developer ID distribution provisioning profile")
	identity := flags.String("identity", "", "exact Developer ID Application identity label")
	capacityRun := flags.Bool("capacity-run", false, "run the authenticated capacity acceptance inside the signed host")
	if flags.Parse(arguments) != nil || flags.NArg() != 0 {
		return 2
	}
	repositoryRoot, err := filepath.Abs(".")
	if err != nil {
		if !*capacityRun {
			_, _ = fmt.Fprintln(stderr, "signed macOS integration host failed")
		}
		return 1
	}
	dependencies = dependencies.withDefaults()
	signing := mackeychainharness.Config{
		RepositoryRoot: repositoryRoot,
		ProfilePath:    *profile,
		Identity:       *identity,
	}
	if *capacityRun {
		protectedInput, protectErr := dependencies.protectInput(stdin)
		if protectErr != nil || protectedInput == nil {
			return 1
		}
		restored := false
		defer func() {
			if !restored {
				_ = protectedInput.Restore()
			}
		}()
		err = dependencies.runCapacity(ctx, dependencies.runner, mackeychainharness.SignedCapacityConfig{
			Signing: signing, Stdin: stdin, Stdout: stdout, Stderr: stderr,
			PrepareInputHandoff: protectedInput.PrepareHandoff,
		})
		restoreErr := protectedInput.Restore()
		restored = true
		if err != nil || restoreErr != nil {
			return 1
		}
		return 0
	}
	signing.Output = stdout
	err = dependencies.runKeychain(ctx, dependencies.runner, signing)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func (dependencies commandDependencies) withDefaults() commandDependencies {
	if dependencies.runner == nil {
		dependencies.runner = mackeychainharness.ExecRunner{}
	}
	if dependencies.runKeychain == nil {
		dependencies.runKeychain = mackeychainharness.Run
	}
	if dependencies.runCapacity == nil {
		dependencies.runCapacity = mackeychainharness.RunSignedCapacity
	}
	if dependencies.protectInput == nil {
		dependencies.protectInput = protectCapacityInput
	}
	return dependencies
}
