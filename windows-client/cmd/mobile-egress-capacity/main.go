//go:build capacityharness && (windows || (darwin && cgo && !bindings))

// mobile-egress-capacity is a build-tagged, developer-only authenticated
// acceptance command. Normal and release builds do not discover this package.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"mobile-egress/windows-client/internal/capacityharness"
)

const (
	minimumRunDuration      = time.Minute
	maximumRunDuration      = 30 * time.Minute
	minimumCommandTimeout   = 5 * time.Second
	maximumCommandTimeout   = 2 * time.Minute
	defaultTargetListenPort = 9443
)

type commandDependencies struct {
	owner         capacityharness.OwnerLoader
	control       capacityharness.Control
	dialer        capacityharness.SessionDialer
	verifier      capacityharness.StreamVerifier
	consoleModes  consoleModeOperations
	run           func(context.Context, capacityharness.RunConfig) (capacityharness.Result, *capacityharness.RunError)
	loadTargetTLS func(capacityharness.TargetSecrets) (*tls.Config, error)
	serveTarget   func(context.Context, capacityharness.TargetConfig) error
	waitInputGate func(context.Context, io.Reader) error
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(execute(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr, commandDependencies{}, os.Environ()))
}

func execute(ctx context.Context, arguments []string, stdin io.Reader, stdout, stderr io.Writer, dependencies commandDependencies, environ []string) int {
	dependencies = dependencies.withDefaults()
	if containsForbiddenEnvironment(environ) || len(arguments) == 0 {
		emitCommandFailure(stderr, capacityharness.PhaseInput, capacityharness.FailureInput, capacityharness.Result{})
		return 2
	}
	switch arguments[0] {
	case "run":
		return executeRun(ctx, arguments[1:], stdin, stdout, stderr, dependencies)
	case "target":
		return executeTarget(ctx, arguments[1:], stdin, stdout, stderr, dependencies)
	default:
		emitCommandFailure(stderr, capacityharness.PhaseInput, capacityharness.FailureInput, capacityharness.Result{})
		return 2
	}
}

func executeRun(ctx context.Context, arguments []string, stdin io.Reader, stdout, stderr io.Writer, dependencies commandDependencies) int {
	flags := flag.NewFlagSet("mobile-egress-capacity run", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	duration := flags.Duration("duration", 15*time.Minute, "fixed-topology hold duration")
	phaseTimeout := flags.Duration("phase-timeout", 30*time.Second, "bounded phase timeout")
	cleanupTimeout := flags.Duration("cleanup-timeout", 30*time.Second, "bounded cleanup timeout")
	if flags.Parse(arguments) != nil || flags.NArg() != 0 || !boundedDuration(*duration, minimumRunDuration, maximumRunDuration) ||
		!boundedDuration(*phaseTimeout, minimumCommandTimeout, maximumCommandTimeout) ||
		!boundedDuration(*cleanupTimeout, minimumCommandTimeout, maximumCommandTimeout) {
		emitCommandFailure(stderr, capacityharness.PhaseInput, capacityharness.FailureInput, capacityharness.Result{})
		return 2
	}
	if err := dependencies.waitInputGate(ctx, stdin); err != nil {
		emitCommandFailure(stderr, capacityharness.PhaseInput, capacityharness.FailureInput, capacityharness.Result{})
		return 2
	}
	restore, err := disableConsoleEcho(stdin, dependencies.consoleModes)
	if err != nil {
		emitCommandFailure(stderr, capacityharness.PhaseInput, capacityharness.FailureInput, capacityharness.Result{})
		return 2
	}
	if err := emitInputReadiness(stdout); err != nil {
		restore()
		emitCommandFailure(stderr, capacityharness.PhaseInput, capacityharness.FailureInternal, capacityharness.Result{})
		return 1
	}
	secrets, err := capacityharness.ReadRunSecrets(stdin)
	restore()
	if err != nil {
		emitCommandFailure(stderr, capacityharness.PhaseInput, capacityharness.FailureInput, capacityharness.Result{})
		return 2
	}
	defer secrets.Zero()
	result, runErr := dependencies.run(ctx, capacityharness.RunConfig{
		OwnerLoader: dependencies.owner, Control: dependencies.control, Dialer: dependencies.dialer,
		Verifier: dependencies.verifier, Secrets: secrets, HoldDuration: *duration,
		PhaseTimeout: *phaseTimeout, CleanupTimeout: *cleanupTimeout,
		Emitter: capacityharness.NewJSONEmitter(stdout),
	})
	if runErr != nil {
		emitCommandFailure(stderr, runErr.Phase, runErr.Category, result)
		return 1
	}
	return 0
}

func executeTarget(ctx context.Context, arguments []string, stdin io.Reader, stdout, stderr io.Writer, dependencies commandDependencies) int {
	flags := flag.NewFlagSet("mobile-egress-capacity target", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	listenPort := flags.Uint("listen-port", defaultTargetListenPort, "fixed IPv4 loopback TLS listen port")
	connectionTimeout := flags.Duration("connection-timeout", 30*time.Second, "bounded TLS/auth/echo timeout")
	cleanupTimeout := flags.Duration("cleanup-timeout", 30*time.Second, "bounded cleanup timeout")
	if flags.Parse(arguments) != nil || flags.NArg() != 0 || *listenPort < 1024 || *listenPort > 65535 ||
		!boundedDuration(*connectionTimeout, minimumCommandTimeout, maximumCommandTimeout) ||
		!boundedDuration(*cleanupTimeout, minimumCommandTimeout, maximumCommandTimeout) {
		emitCommandFailure(stderr, capacityharness.PhaseInput, capacityharness.FailureInput, capacityharness.Result{})
		return 2
	}
	restore, err := disableConsoleEcho(stdin, dependencies.consoleModes)
	if err != nil {
		emitCommandFailure(stderr, capacityharness.PhaseInput, capacityharness.FailureInput, capacityharness.Result{})
		return 2
	}
	if err := emitInputReadiness(stdout); err != nil {
		restore()
		emitCommandFailure(stderr, capacityharness.PhaseInput, capacityharness.FailureInternal, capacityharness.Result{})
		return 1
	}
	secrets, err := capacityharness.ReadTargetSecrets(stdin)
	restore()
	if err != nil {
		emitCommandFailure(stderr, capacityharness.PhaseInput, capacityharness.FailureInput, capacityharness.Result{})
		return 2
	}
	defer secrets.Zero()
	tlsConfig, err := dependencies.loadTargetTLS(secrets)
	if err != nil {
		emitCommandFailure(stderr, capacityharness.PhaseTarget, fixedCategory(err, capacityharness.FailureTLS), capacityharness.Result{})
		return 1
	}
	err = dependencies.serveTarget(ctx, capacityharness.TargetConfig{
		Token: secrets.Token, TLSConfig: tlsConfig, ListenPort: uint16(*listenPort),
		ConnectionTimeout: *connectionTimeout, CleanupTimeout: *cleanupTimeout,
		Emitter: capacityharness.NewJSONEmitter(stdout),
	})
	if err != nil {
		emitCommandFailure(stderr, capacityharness.PhaseTarget, fixedCategory(err, capacityharness.FailureInternal), capacityharness.Result{})
		return 1
	}
	return 0
}

func (dependencies commandDependencies) withDefaults() commandDependencies {
	if dependencies.owner == nil {
		dependencies.owner = capacityharness.ProtectedOwnerLoader{}
	}
	if dependencies.control == nil {
		dependencies.control = capacityharness.ProductionControl{}
	}
	if dependencies.dialer == nil {
		dependencies.dialer = capacityharness.ProductionSessionDialer{}
	}
	if dependencies.verifier == nil {
		dependencies.verifier = capacityharness.EchoVerifier{}
	}
	if dependencies.consoleModes == nil {
		dependencies.consoleModes = platformConsoleModeOperations()
	}
	if dependencies.run == nil {
		dependencies.run = capacityharness.Run
	}
	if dependencies.loadTargetTLS == nil {
		dependencies.loadTargetTLS = func(secrets capacityharness.TargetSecrets) (*tls.Config, error) {
			return capacityharness.LoadTargetTLSConfig(secrets, (*x509.CertPool)(nil), time.Now())
		}
	}
	if dependencies.serveTarget == nil {
		dependencies.serveTarget = capacityharness.ServeTarget
	}
	if dependencies.waitInputGate == nil {
		dependencies.waitInputGate = waitForPlatformInputGate
	}
	return dependencies
}

func boundedDuration(value, minimum, maximum time.Duration) bool {
	return value >= minimum && value <= maximum
}

func containsForbiddenEnvironment(environ []string) bool {
	for _, entry := range environ {
		name, _, _ := strings.Cut(entry, "=")
		name = strings.ToUpper(strings.TrimSpace(name))
		if strings.HasPrefix(name, "MOBILE_EGRESS_CAPACITY_") {
			return true
		}
		switch name {
		case "MOBILE_EGRESS_TOKEN", "MOBILE_EGRESS_DESTINATION", "MOBILE_EGRESS_TARGET",
			"MOBILE_EGRESS_RELAY_URL", "MOBILE_EGRESS_IDENTITY", "MOBILE_EGRESS_PRIVATE_KEY",
			"MOBILE_EGRESS_CERTIFICATE":
			return true
		}
	}
	return false
}

type consoleModeOperations interface {
	Get(*os.File) (uint64, error)
	Set(*os.File, uint64) error
	IsNotTerminal(error) bool
}

func disableConsoleEcho(reader io.Reader, operations consoleModeOperations) (func(), error) {
	file, ok := reader.(*os.File)
	if !ok {
		return func() {}, nil
	}
	original, err := operations.Get(file)
	if err != nil {
		if operations.IsNotTerminal(err) {
			return func() {}, nil
		}
		return nil, errors.New("capacity harness could not query console mode")
	}
	if err := operations.Set(file, original&^consoleEchoInputFlag); err != nil {
		return nil, errors.New("capacity harness could not disable console echo")
	}
	return func() { _ = operations.Set(file, original) }, nil
}

func fixedCategory(err error, fallback capacityharness.FailureCategory) capacityharness.FailureCategory {
	var categorized capacityharness.CategorizedError
	if errors.As(err, &categorized) && fixedFailureCategory(categorized.Category) {
		return categorized.Category
	}
	return fallback
}

func fixedFailureCategory(category capacityharness.FailureCategory) bool {
	switch category {
	case capacityharness.FailureInput, capacityharness.FailurePreflight, capacityharness.FailureProvision,
		capacityharness.FailureSession, capacityharness.FailureClientLimit, capacityharness.FailureAgentLimit,
		capacityharness.FailureTLS, capacityharness.FailureAuthentication, capacityharness.FailureEcho,
		capacityharness.FailureProtocol, capacityharness.FailureCanceled, capacityharness.FailureTimeout,
		capacityharness.FailureCleanup, capacityharness.FailureInternal:
		return true
	default:
		return false
	}
}

func emitCommandFailure(writer io.Writer, phase capacityharness.Phase, category capacityharness.FailureCategory, result capacityharness.Result) {
	_ = capacityharness.NewJSONEmitter(writer).Emit(capacityharness.Event{
		Phase: phase, Attempted: result.Attempted, Open: result.Open,
		Verified: result.Verified, Closed: result.Closed, Failure: category,
	})
}

func emitInputReadiness(writer io.Writer) error {
	return capacityharness.NewJSONEmitter(writer).Emit(capacityharness.Event{
		Phase: capacityharness.PhaseInput, Failure: capacityharness.FailureNone,
	})
}
