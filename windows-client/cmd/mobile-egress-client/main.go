package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"

	"mobile-egress/windows-client/internal/nodeservice"
	"mobile-egress/windows-client/internal/sealedconfig"
	"mobile-egress/windows-client/internal/securestore"
)

const maximumEnvelopeFileBytes = 2 << 20

var version = "dev"

type repositoryOpener func(string) (*nodeservice.Repository, error)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, openNodeRepository))
}

func run(arguments []string, stdout, stderr io.Writer, open repositoryOpener) int {
	if len(arguments) == 0 {
		writeUsage(stderr)
		return 2
	}
	if arguments[0] == "--version" || arguments[0] == "version" {
		fmt.Fprintln(stdout, version)
		return 0
	}
	if open == nil {
		fmt.Fprintln(stderr, "mobile-egress-client: secure repository is unavailable")
		return 1
	}
	switch arguments[0] {
	case "bootstrap":
		return runBootstrap(arguments[1:], stdout, stderr, open)
	case "apply-config":
		return runApplyConfiguration(arguments[1:], stdout, stderr, open)
	case "serve":
		return runServe(arguments[1:], stderr, open)
	default:
		writeUsage(stderr)
		return 2
	}
}

func runBootstrap(arguments []string, stdout, stderr io.Writer, open repositoryOpener) int {
	flags := flag.NewFlagSet("mobile-egress-client bootstrap", flag.ContinueOnError)
	flags.SetOutput(stderr)
	stateDir := flags.String("state-dir", defaultStateDirectory(), "protected Client service state directory")
	if err := flags.Parse(arguments); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "mobile-egress-client bootstrap: unexpected positional arguments")
		return 2
	}
	repository, err := open(*stateDir)
	if err != nil {
		fmt.Fprintln(stderr, "mobile-egress-client bootstrap: open protected state:", err)
		return 1
	}
	response, err := repository.Bootstrap(context.Background())
	if err != nil {
		fmt.Fprintln(stderr, "mobile-egress-client bootstrap:", err)
		return 1
	}
	if err := json.NewEncoder(stdout).Encode(response); err != nil {
		fmt.Fprintln(stderr, "mobile-egress-client bootstrap: encode public response:", err)
		return 1
	}
	return 0
}

func runApplyConfiguration(arguments []string, stdout, stderr io.Writer, open repositoryOpener) int {
	flags := flag.NewFlagSet("mobile-egress-client apply-config", flag.ContinueOnError)
	flags.SetOutput(stderr)
	stateDir := flags.String("state-dir", defaultStateDirectory(), "protected Client service state directory")
	envelopeFile := flags.String("envelope-file", "", "path to a sealed configuration envelope")
	if err := flags.Parse(arguments); err != nil {
		return 2
	}
	if flags.NArg() != 0 || *envelopeFile == "" {
		fmt.Fprintln(stderr, "mobile-egress-client apply-config: envelope file is required and positional arguments are not accepted")
		return 2
	}
	envelope, err := readEnvelopeFile(*envelopeFile)
	if err != nil {
		fmt.Fprintln(stderr, "mobile-egress-client apply-config: sealed envelope is invalid")
		return 1
	}
	repository, err := open(*stateDir)
	if err != nil {
		fmt.Fprintln(stderr, "mobile-egress-client apply-config: open protected state:", err)
		return 1
	}
	if err := repository.Apply(context.Background(), envelope); err != nil {
		fmt.Fprintln(stderr, "mobile-egress-client apply-config:", err)
		return 1
	}
	_ = json.NewEncoder(stdout).Encode(map[string]bool{"configured": true})
	return 0
}

func runServe(arguments []string, stderr io.Writer, open repositoryOpener) int {
	flags := flag.NewFlagSet("mobile-egress-client serve", flag.ContinueOnError)
	flags.SetOutput(stderr)
	stateDir := flags.String("state-dir", defaultStateDirectory(), "protected Client service state directory")
	if err := flags.Parse(arguments); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "mobile-egress-client serve: unexpected positional arguments")
		return 2
	}
	repository, err := open(*stateDir)
	if err != nil {
		fmt.Fprintln(stderr, "mobile-egress-client serve: open protected state:", err)
		return 1
	}
	return runNodeService(repository, stderr)
}

func runForegroundNodeService(repository *nodeservice.Repository) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return nodeservice.NewService(repository, nodeservice.DefaultDialer{}).Run(ctx)
}

func openNodeRepository(stateDir string) (*nodeservice.Repository, error) {
	stateDir = filepath.Clean(stateDir)
	if stateDir == "." || stateDir == "" {
		return nil, errors.New("state directory is required")
	}
	store, err := securestore.NewDPAPIStore(filepath.Join(stateDir, "secure"))
	if err != nil {
		return nil, err
	}
	return nodeservice.NewRepository(store), nil
}

func readEnvelopeFile(path string) (sealedconfig.Envelope, error) {
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return sealedconfig.Envelope{}, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maximumEnvelopeFileBytes+1))
	if err != nil || len(raw) == 0 || len(raw) > maximumEnvelopeFileBytes {
		return sealedconfig.Envelope{}, errors.New("sealed envelope file is missing or too large")
	}
	defer clear(raw)
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var envelope sealedconfig.Envelope
	if err := decoder.Decode(&envelope); err != nil {
		return sealedconfig.Envelope{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return sealedconfig.Envelope{}, errors.New("sealed envelope contains trailing JSON")
	}
	return envelope, nil
}

func defaultStateDirectory() string {
	if runtime.GOOS == "windows" {
		if programData := os.Getenv("ProgramData"); programData != "" {
			return filepath.Join(programData, "MobileEgress", "Client")
		}
		return `C:\ProgramData\MobileEgress\Client`
	}
	return "/var/lib/mobile-egress-client"
}

func writeUsage(writer io.Writer) {
	fmt.Fprintln(writer, "usage: mobile-egress-client <bootstrap|apply-config|serve|--version> [flags]")
}
