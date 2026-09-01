package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/user"
	"syscall"

	"mobile-egress/relay/internal/adminservice"
)

const (
	darwinRelayAddress        = "127.0.0.1:8443"
	darwinAdminMaxConnections = 32
	daemonUnavailableMessage  = "relay daemon unavailable"
)

var (
	errDaemonUnavailable  = errors.New("relay daemon unavailable")
	errDaemonNonQuiescent = errors.New("relay daemon did not shut down quiescently")
)

type daemonSocket interface {
	Listener() net.Listener
	Close() error
}

type daemonRuntime interface {
	Run(context.Context) (adminservice.RunResult, error)
	Close() error
}

type daemonDependencies struct {
	effectiveUID  func() int
	setUmask      func(int) int
	lookupGroup   func(string) (*user.Group, error)
	notifyContext func(context.Context, ...os.Signal) (context.Context, context.CancelFunc)
	openSocket    func(context.Context, uint32) (daemonSocket, error)
	newPathGuard  func() (adminservice.PreparedPathGuard, error)
	newRuntime    func(adminservice.RuntimeConfig) (daemonRuntime, error)
	peer          adminservice.PeerExtractor
	listenRelay   func(string, string) (net.Listener, error)
	openRelay     func(string) (adminservice.RelayInstance, error)
}

type daemonPlatform func() (adminservice.RunResult, error)

func runDaemon(arguments []string, stderr io.Writer, platform daemonPlatform) int {
	if len(arguments) != 0 {
		fmt.Fprintln(stderr, "relay daemon: arguments are not accepted")
		return 2
	}
	if platform == nil {
		fmt.Fprintln(stderr, daemonUnavailableMessage)
		return 1
	}
	result, err := platform()
	if err != nil || !result.Quiescent {
		fmt.Fprintln(stderr, daemonUnavailableMessage)
		return 1
	}
	return 0
}

func executeDaemon(dependencies daemonDependencies) (adminservice.RunResult, error) {
	if err := validateDaemonDependencies(dependencies); err != nil {
		return adminservice.RunResult{}, err
	}
	if dependencies.effectiveUID() != 0 {
		return adminservice.RunResult{}, errDaemonUnavailable
	}
	dependencies.setUmask(0o077)
	group, err := dependencies.lookupGroup("admin")
	if err != nil || group == nil {
		return adminservice.RunResult{}, errors.Join(errDaemonUnavailable, err)
	}
	adminGID, err := adminservice.ParseCanonicalAdminGID(group.Gid)
	if err != nil {
		return adminservice.RunResult{}, errors.Join(errDaemonUnavailable, err)
	}
	signalContext, stopSignals := dependencies.notifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	if signalContext == nil || stopSignals == nil {
		if stopSignals != nil {
			stopSignals()
		}
		return adminservice.RunResult{}, errDaemonUnavailable
	}
	socket, err := dependencies.openSocket(signalContext, adminGID)
	if err != nil || socket == nil {
		stopSignals()
		return adminservice.RunResult{}, errors.Join(errDaemonUnavailable, err)
	}
	guard, err := dependencies.newPathGuard()
	if err != nil || guard == nil {
		socketErr := socket.Close()
		stopSignals()
		return adminservice.RunResult{}, errors.Join(errDaemonUnavailable, err, socketErr)
	}
	runtime, err := dependencies.newRuntime(adminservice.RuntimeConfig{
		AdminListener: socket.Listener(), Peer: dependencies.peer, AdminGID: adminGID,
		HelperVersion: version, StateDir: adminservice.DarwinRelayStateDir,
		RelayAddress: darwinRelayAddress, PathGuard: guard,
		MaxConnections: darwinAdminMaxConnections,
		ListenRelay:    dependencies.listenRelay, OpenRelay: dependencies.openRelay,
	})
	if err != nil || runtime == nil {
		socketErr := socket.Close()
		stopSignals()
		return adminservice.RunResult{}, errors.Join(errDaemonUnavailable, err, socketErr)
	}
	result, runErr := runtime.Run(signalContext)
	runtimeErr := runtime.Close()
	socketErr := socket.Close()
	stopSignals()
	if !result.Quiescent {
		runErr = errors.Join(runErr, errDaemonNonQuiescent)
	}
	return result, errors.Join(runErr, runtimeErr, socketErr)
}

func validateDaemonDependencies(dependencies daemonDependencies) error {
	if dependencies.effectiveUID == nil || dependencies.setUmask == nil || dependencies.lookupGroup == nil ||
		dependencies.notifyContext == nil || dependencies.openSocket == nil || dependencies.newPathGuard == nil ||
		dependencies.newRuntime == nil || dependencies.peer == nil || dependencies.listenRelay == nil || dependencies.openRelay == nil {
		return errDaemonUnavailable
	}
	return nil
}
