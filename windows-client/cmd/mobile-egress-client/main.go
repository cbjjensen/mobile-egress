package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"mobile-egress/windows-client/internal/clientapp"
	"mobile-egress/windows-client/internal/nodeservice"
)

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
	case "bootstrap", "apply-config":
		fmt.Fprintln(stderr, "Mobile Egress 2 uses direct phone pairing. Install or repair the Client app and configure its endpoint; legacy relay configuration is unsupported.")
		return 2
	case "serve":
		return runServe(arguments[1:], stderr, open)
	default:
		writeUsage(stderr)
		return 2
	}
}

func runServe(arguments []string, stderr io.Writer, open repositoryOpener) int {
	flags := flag.NewFlagSet("mobile-egress-client serve", flag.ContinueOnError)
	flags.SetOutput(stderr)
	stateDir := flags.String("state-dir", "", "protected Client service state directory")
	_ = flags.Bool("standalone", false, "compatibility flag; all Clients use direct phone pairing")
	if err := flags.Parse(arguments); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "mobile-egress-client serve: unexpected positional arguments")
		return 2
	}
	if *stateDir == "" {
		*stateDir = standaloneStateDirectory()
	}
	repository, err := open(*stateDir)
	if err != nil {
		fmt.Fprintln(stderr, "mobile-egress-client serve: open protected state:", err)
		return 1
	}
	return runNodeService(func(ctx context.Context) error {
		return runStandalone(ctx, repository, *stateDir)
	}, stderr)
}

func runForegroundNodeService(run func(context.Context) error) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return run(ctx)
}

func runStandalone(ctx context.Context, repository *nodeservice.Repository, stateDir string) error {
	listener, err := clientapp.ListenLocal(stateDir)
	if err != nil {
		return err
	}
	platform := runtime.GOOS
	if platform == "darwin" {
		platform = "macos"
	}
	manager := nodeservice.NewDirect(repository, platform, runtime.GOARCH, version)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 2)
	go func() { done <- runRecoverableRuntime(ctx, manager.Run, 2*time.Second) }()
	go func() { done <- clientapp.Serve(ctx, listener, clientapp.WithHostFirewall(manager)) }()
	err = <-done
	cancel()
	other := <-done
	if err != nil {
		return err
	}
	return other
}

// Local management remains available while an occupied port or unavailable
// store prevents serving. The manager retains actionable status, and a local
// configuration change or repair is picked up by the next bounded retry.
func runRecoverableRuntime(ctx context.Context, run func(context.Context) error, retry time.Duration) error {
	for ctx.Err() == nil {
		_ = run(ctx)
		if ctx.Err() != nil {
			return nil
		}
		timer := time.NewTimer(retry)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
	return nil
}

func openNodeRepository(stateDir string) (*nodeservice.Repository, error) {
	stateDir = filepath.Clean(stateDir)
	if stateDir == "." || stateDir == "" {
		return nil, errors.New("state directory is required")
	}
	store, err := openServiceStore(stateDir)
	if err != nil {
		return nil, err
	}
	return nodeservice.NewRepository(store), nil
}

func standaloneStateDirectory() string {
	if runtime.GOOS == "darwin" {
		return "/Library/Application Support/MobileEgressClient"
	}
	if programData := os.Getenv("ProgramData"); programData != "" {
		return filepath.Join(programData, "MobileEgressClient")
	}
	return `C:\ProgramData\MobileEgressClient`
}

func writeUsage(writer io.Writer) {
	fmt.Fprintln(writer, "usage: mobile-egress-client <serve|--version> [flags]; configure and pair through the installed Client app")
}
