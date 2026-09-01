package main

import (
	"fmt"
	"io"
	"os"

	"mobile-egress/windows-client/internal/macosrelease"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, output io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: mobile-egress-macos-release validate-lock <path> | signing-plan | validate-record <path> <version> <source-commit> <manifest-sha256> <artifact-sha256> <application-identity> <installer-identity>")
	}
	switch args[0] {
	case "validate-lock":
		if len(args) != 2 {
			return fmt.Errorf("validate-lock requires one path")
		}
		file, err := os.Open(args[1])
		if err != nil {
			return err
		}
		defer file.Close()
		entries, err := macosrelease.ParsePinnedToolchain(file)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if _, err := fmt.Fprintf(output, "%s %s\n", entry.Tool, entry.Version); err != nil {
				return err
			}
		}
		return nil
	case "signing-plan":
		if len(args) != 1 {
			return fmt.Errorf("signing-plan does not accept arguments")
		}
		for _, step := range macosrelease.SigningPlan() {
			if _, err := fmt.Fprintln(output, step); err != nil {
				return err
			}
		}
		return nil
	case "validate-record":
		if len(args) != 8 {
			return fmt.Errorf("validate-record requires path, version, source commit, manifest SHA-256, artifact SHA-256, application identity, and installer identity")
		}
		file, err := os.Open(args[1])
		if err != nil {
			return err
		}
		defer file.Close()
		record, err := macosrelease.DecodeVerificationRecord(file)
		if err != nil {
			return err
		}
		return record.Validate(macosrelease.VerificationExpectations{
			ReleaseVersion:      args[2],
			SourceCommit:        args[3],
			NodeManifestSHA256:  args[4],
			ArtifactSHA256:      args[5],
			ApplicationIdentity: args[6],
			InstallerIdentity:   args[7],
		})
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}
