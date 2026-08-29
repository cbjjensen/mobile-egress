package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"mobile-egress/relay/internal/service"
)

const defaultStateDir = "/var/lib/mobile-egress"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(arguments []string, stdout, stderr io.Writer) int {
	if len(arguments) == 0 {
		writeUsage(stderr)
		return 2
	}
	switch arguments[0] {
	case "init":
		return runInit(arguments[1:], stdout, stderr)
	case "serve":
		return runServe(arguments[1:], stderr)
	default:
		writeUsage(stderr)
		return 2
	}
}

func runInit(arguments []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("relay init", flag.ContinueOnError)
	flags.SetOutput(stderr)
	stateDir := flags.String("state-dir", defaultStateDir, "persistent relay state directory")
	publicName := flags.String("public-name", "", "public relay DNS name or IP address")
	if err := flags.Parse(arguments); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "relay init: unexpected positional arguments")
		return 2
	}
	capability, err := service.Initialize(context.Background(), service.InitOptions{
		StateDir: *stateDir, PublicName: *publicName,
	})
	if err != nil {
		fmt.Fprintln(stderr, "relay init:", err)
		return 1
	}
	fmt.Fprintln(stdout, "Owner enrollment capability:", capability)
	return 0
}

func runServe(arguments []string, stderr io.Writer) int {
	flags := flag.NewFlagSet("relay serve", flag.ContinueOnError)
	flags.SetOutput(stderr)
	stateDir := flags.String("state-dir", defaultStateDir, "persistent relay state directory")
	listenAddress := flags.String("listen", "0.0.0.0:8443", "TLS listen address")
	if err := flags.Parse(arguments); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "relay serve: unexpected positional arguments")
		return 2
	}

	relay, err := service.Open(*stateDir)
	if err != nil {
		fmt.Fprintln(stderr, "relay serve:", err)
		return 1
	}
	defer relay.Close()

	server := &http.Server{
		Addr:              *listenAddress,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	shutdownContext, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	go func() {
		<-shutdownContext.Done()
		contextWithTimeout, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(contextWithTimeout)
	}()

	if err := relay.Serve(server); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintln(stderr, "relay serve:", err)
		return 1
	}
	return 0
}

func writeUsage(writer io.Writer) {
	fmt.Fprintln(writer, "usage: relay <init|serve> [flags]")
}
