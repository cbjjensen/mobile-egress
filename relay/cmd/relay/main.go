package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"mobile-egress/pairing"
	"mobile-egress/relay/internal/service"
)

const defaultStateDir = "/var/lib/mobile-egress"

var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(arguments []string, stdout, stderr io.Writer) int {
	if len(arguments) == 0 {
		writeUsage(stderr)
		return 2
	}
	switch arguments[0] {
	case "--version", "version":
		fmt.Fprintln(stdout, version)
		return 0
	case "init":
		return runInit(arguments[1:], stdout, stderr)
	case "bootstrap-owner":
		return runBootstrapOwner(arguments[1:], stdout, stderr)
	case "rotate-endpoint":
		return runRotateEndpoint(arguments[1:], stdout, stderr)
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
	publicURL := flags.String("public-url", "", "public HTTPS relay origin included in pairing bundles")
	if err := flags.Parse(arguments); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "relay init: unexpected positional arguments")
		return 2
	}
	relayURL := *publicURL
	if relayURL == "" && *publicName != "" {
		relayURL = "https://" + net.JoinHostPort(*publicName, "8443")
	}
	origin, err := pairing.RelayOrigin(relayURL)
	if err != nil || origin.Hostname() != *publicName {
		fmt.Fprintln(stderr, "relay init: public URL must be an HTTPS origin for public-name")
		return 2
	}
	capability, err := service.Initialize(context.Background(), service.InitOptions{
		StateDir: *stateDir, PublicName: *publicName, PublicURL: origin.String(),
	})
	if err != nil {
		fmt.Fprintln(stderr, "relay init:", err)
		return 1
	}
	caPEM, err := service.CACertificatePEM(*stateDir)
	if err != nil {
		fmt.Fprintln(stderr, "relay init:", err)
		return 1
	}
	bundle, err := pairing.Encode(pairing.Bundle{
		Version: pairing.Version, RelayURL: origin.String(), CACertificatePEM: string(caPEM),
		Capability: capability, Role: "owner", ExpiresAt: time.Now().UTC().Add(10 * time.Minute),
	})
	if err != nil {
		fmt.Fprintln(stderr, "relay init:", err)
		return 1
	}
	fmt.Fprintln(stdout, "Owner pairing bundle:", bundle)
	return 0
}

func runBootstrapOwner(arguments []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("relay bootstrap-owner", flag.ContinueOnError)
	flags.SetOutput(stderr)
	stateDir := flags.String("state-dir", defaultStateDir, "persistent relay state directory")
	publicName := flags.String("public-name", "", "public relay DNS name or IP address")
	publicURL := flags.String("public-url", "", "public HTTPS relay origin")
	ownerCSRFile := flags.String("owner-csr-file", "", "path to the Owner certificate request")
	if err := flags.Parse(arguments); err != nil {
		return 2
	}
	if flags.NArg() != 0 || *ownerCSRFile == "" {
		fmt.Fprintln(stderr, "relay bootstrap-owner: owner CSR file is required and positional arguments are not accepted")
		return 2
	}
	csrPEM, err := os.ReadFile(*ownerCSRFile)
	if err != nil {
		fmt.Fprintln(stderr, "relay bootstrap-owner: read Owner CSR:", err)
		return 1
	}
	result, err := service.BootstrapOwner(context.Background(), service.BootstrapOwnerOptions{
		StateDir: *stateDir, PublicName: *publicName, PublicURL: *publicURL, CSRPEM: string(csrPEM),
	})
	if err != nil {
		fmt.Fprintln(stderr, "relay bootstrap-owner:", err)
		return 1
	}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		fmt.Fprintln(stderr, "relay bootstrap-owner: encode result:", err)
		return 1
	}
	return 0
}

func runRotateEndpoint(arguments []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("relay rotate-endpoint", flag.ContinueOnError)
	flags.SetOutput(stderr)
	stateDir := flags.String("state-dir", defaultStateDir, "persistent relay state directory")
	publicName := flags.String("public-name", "", "new public relay DNS name or IP address")
	publicURL := flags.String("public-url", "", "new public HTTPS relay origin")
	if err := flags.Parse(arguments); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "relay rotate-endpoint: unexpected positional arguments")
		return 2
	}
	result, err := service.RotateEndpoint(context.Background(), service.RotateEndpointOptions{
		StateDir: *stateDir, PublicName: *publicName, PublicURL: *publicURL,
	})
	if err != nil {
		fmt.Fprintln(stderr, "relay rotate-endpoint:", err)
		return 1
	}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		fmt.Fprintln(stderr, "relay rotate-endpoint: encode result:", err)
		return 1
	}
	return 0
}

func runServe(arguments []string, stderr io.Writer) int {
	flags := flag.NewFlagSet("relay serve", flag.ContinueOnError)
	flags.SetOutput(stderr)
	stateDir := flags.String("state-dir", defaultStateDir, "persistent relay state directory")
	listenAddress := flags.String("listen", "127.0.0.1:8443", "TLS listen address")
	if err := flags.Parse(arguments); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "relay serve: unexpected positional arguments")
		return 2
	}
	if handled, status := runAsWindowsServiceIfNeeded(*stateDir, *listenAddress, stderr); handled {
		return status
	}

	shutdownContext, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	if err := serveRelay(shutdownContext, *stateDir, *listenAddress); err != nil {
		fmt.Fprintln(stderr, "relay serve:", err)
		return 1
	}
	return 0
}

func serveRelay(ctx context.Context, stateDir, listenAddress string) error {
	relay, err := service.Open(stateDir)
	if err != nil {
		return err
	}
	defer relay.Close()

	server := &http.Server{
		Addr:              listenAddress,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		<-ctx.Done()
		contextWithTimeout, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(contextWithTimeout)
	}()

	if err := relay.Serve(server); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func writeUsage(writer io.Writer) {
	fmt.Fprintln(writer, "usage: relay <bootstrap-owner|init|rotate-endpoint|serve|--version> [flags]")
}
