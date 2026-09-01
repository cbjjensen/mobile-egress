package adminservice

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"mobile-egress/internal/relayadmin"
	"mobile-egress/relay/internal/service"
)

func TestRuntimeRejectsInvalidConfigurationBeforeGuardPrepare(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*RuntimeConfig)
	}{
		{name: "nil listener", mutate: func(config *RuntimeConfig) { config.AdminListener = nil }},
		{name: "nil peer", mutate: func(config *RuntimeConfig) { config.Peer = nil }},
		{name: "zero admin gid", mutate: func(config *RuntimeConfig) { config.AdminGID = 0 }},
		{name: "blank helper version", mutate: func(config *RuntimeConfig) { config.HelperVersion = " \t" }},
		{name: "invalid helper version", mutate: func(config *RuntimeConfig) { config.HelperVersion = string([]byte{0xff}) }},
		{name: "blank state dir", mutate: func(config *RuntimeConfig) { config.StateDir = " " }},
		{name: "non-loopback relay", mutate: func(config *RuntimeConfig) { config.RelayAddress = "0.0.0.0:8443" }},
		{name: "missing relay port", mutate: func(config *RuntimeConfig) { config.RelayAddress = "127.0.0.1" }},
		{name: "nil guard", mutate: func(config *RuntimeConfig) { config.PathGuard = nil }},
		{name: "zero connection limit", mutate: func(config *RuntimeConfig) { config.MaxConnections = 0 }},
		{name: "negative connection limit", mutate: func(config *RuntimeConfig) { config.MaxConnections = -1 }},
		{name: "nil relay listener", mutate: func(config *RuntimeConfig) { config.ListenRelay = nil }},
		{name: "nil relay opener", mutate: func(config *RuntimeConfig) { config.OpenRelay = nil }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			guard := &runtimeTestGuard{}
			listener := &runtimeTestListener{}
			config := validRuntimeTestConfig(guard, listener)
			test.mutate(&config)
			dependencies, calls := validRuntimeTestDependencies()
			if runtime, err := newRuntime(config, dependencies); err == nil || runtime != nil {
				t.Fatalf("newRuntime() = %#v, %v, want nil/error", runtime, err)
			}
			if guard.prepareCalls.Load() != 0 || listener.closeCalls.Load() != 0 || len(*calls) != 0 {
				t.Fatalf("invalid configuration effects: prepare=%d close=%d calls=%v",
					guard.prepareCalls.Load(), listener.closeCalls.Load(), *calls)
			}
		})
	}
	if utf8.ValidString(string([]byte{0xff})) {
		t.Fatal("invalid UTF-8 fixture unexpectedly valid")
	}
}

func TestRuntimePreparesGuardBeforeSupervisorOrStateOpen(t *testing.T) {
	t.Parallel()

	guard := &runtimeTestGuard{}
	listener := &runtimeTestListener{}
	config := validRuntimeTestConfig(guard, listener)
	dependencies, calls := validRuntimeTestDependencies()
	guard.onPrepare = func() { *calls = append(*calls, "prepare") }
	if runtime, err := newRuntime(config, dependencies); err != nil || runtime == nil {
		t.Fatalf("newRuntime() = %#v, %v", runtime, err)
	}
	want := []string{"prepare", "supervisor", "callback", "state", "handler", "daemon"}
	if !reflect.DeepEqual(*calls, want) {
		t.Fatalf("construction calls = %v, want %v", *calls, want)
	}
}

func TestRuntimePassesSameGuardAndExactMutationCapacityToState(t *testing.T) {
	t.Parallel()

	guard := &runtimeTestGuard{}
	config := validRuntimeTestConfig(guard, &runtimeTestListener{})
	dependencies, _ := validRuntimeTestDependencies()
	var got service.AdminStateOptions
	dependencies.openState = func(options service.AdminStateOptions) (*service.AdminState, error) {
		got = options
		return &service.AdminState{}, nil
	}
	if runtime, err := newRuntime(config, dependencies); err != nil || runtime == nil {
		t.Fatalf("newRuntime() = %#v, %v", runtime, err)
	}
	if got.PathGuard != guard || got.MutationCapacity != relayadmin.MutationReplayCapacity || got.StateDir != config.StateDir || got.MutationFinished == nil {
		t.Fatalf("OpenAdminState options = %#v", got)
	}
}

func TestRuntimePartialConstructionClosesStateAndJoinsCloseFailure(t *testing.T) {
	t.Parallel()

	for _, stage := range []string{"handler", "daemon"} {
		stage := stage
		t.Run(stage, func(t *testing.T) {
			t.Parallel()
			primary := errors.New(stage + " construction failed")
			closeFailure := errors.New("state close failed")
			dependencies, _ := validRuntimeTestDependencies()
			var closeCalls atomic.Int32
			dependencies.closeState = func(*service.AdminState) error {
				closeCalls.Add(1)
				return closeFailure
			}
			switch stage {
			case "handler":
				dependencies.newHandler = func(HandlerConfig) (*Handler, error) { return nil, primary }
			case "daemon":
				dependencies.newDaemon = func(DaemonConfig) (runtimeDaemon, error) { return nil, primary }
			}
			runtime, err := newRuntime(validRuntimeTestConfig(&runtimeTestGuard{}, &runtimeTestListener{}), dependencies)
			if runtime != nil || !errors.Is(err, primary) || !errors.Is(err, closeFailure) || closeCalls.Load() != 1 {
				t.Fatalf("newRuntime() = %#v, %v, close calls=%d", runtime, err, closeCalls.Load())
			}
		})
	}
}

func TestRuntimeConstructorNeverClosesBorrowedAdminListener(t *testing.T) {
	t.Parallel()

	for _, stage := range []string{"supervisor", "callback", "state", "handler", "daemon"} {
		stage := stage
		t.Run(stage, func(t *testing.T) {
			t.Parallel()
			listener := &runtimeTestListener{}
			dependencies, _ := validRuntimeTestDependencies()
			failure := errors.New(stage + " failed")
			switch stage {
			case "supervisor":
				dependencies.newSupervisor = func(SupervisorConfig) (*Supervisor, error) { return nil, failure }
			case "callback":
				dependencies.newMutationFinished = func(func() *service.AdminState, *Supervisor) (func(relayadmin.ReplayKey), error) {
					return nil, failure
				}
			case "state":
				dependencies.openState = func(service.AdminStateOptions) (*service.AdminState, error) { return nil, failure }
			case "handler":
				dependencies.newHandler = func(HandlerConfig) (*Handler, error) { return nil, failure }
			case "daemon":
				dependencies.newDaemon = func(DaemonConfig) (runtimeDaemon, error) { return nil, failure }
			}
			if runtime, err := newRuntime(validRuntimeTestConfig(&runtimeTestGuard{}, listener), dependencies); runtime != nil || !errors.Is(err, failure) {
				t.Fatalf("newRuntime() = %#v, %v", runtime, err)
			}
			if listener.closeCalls.Load() != 0 {
				t.Fatalf("borrowed listener Close calls = %d, want 0", listener.closeCalls.Load())
			}
		})
	}
}

func TestDarwinRelayStateDirectoryIsExact(t *testing.T) {
	t.Parallel()

	if DarwinRelayStateDir != "/Library/Application Support/ZFNF Mobile Egress/Relay" {
		t.Fatalf("DarwinRelayStateDir = %q", DarwinRelayStateDir)
	}
}

func TestRuntimeWiresStateReplayAuthorizationHandlerAndFiveMinuteLimits(t *testing.T) {
	t.Parallel()

	config := validRuntimeTestConfig(&runtimeTestGuard{}, &runtimeTestListener{})
	config.StateDir = filepath.Join(t.TempDir(), "Relay")
	dependencies := productionRuntimeDependencies()
	var daemonConfig DaemonConfig
	dependencies.newDaemon = func(config DaemonConfig) (runtimeDaemon, error) {
		daemonConfig = config
		return runtimeDaemonFunc(func(context.Context) (RunResult, error) {
			return RunResult{Quiescent: true}, nil
		}), nil
	}
	runtime, err := newRuntime(config, dependencies)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.state.Close() })
	if daemonConfig.Server == nil || daemonConfig.Server.Handler == nil || daemonConfig.Server.Authorize == nil {
		t.Fatalf("daemon server wiring = %#v", daemonConfig.Server)
	}
	if daemonConfig.Server.Replay != runtime.state.ReplayStore() {
		t.Fatal("daemon did not receive the AdminState replay store")
	}
	if daemonConfig.Server.OperationLimit != relayadmin.OperationTimeout || daemonConfig.Server.IOLimit != relayadmin.OperationTimeout {
		t.Fatalf("server limits = %s/%s", daemonConfig.Server.OperationLimit, daemonConfig.Server.IOLimit)
	}
	admin := relayadmin.NewPeer(501, []uint32{config.AdminGID})
	outsider := relayadmin.NewPeer(502, []uint32{81})
	if !daemonConfig.Server.Authorize(context.Background(), admin, relayadmin.OperationSetup) ||
		daemonConfig.Server.Authorize(context.Background(), outsider, relayadmin.OperationSetup) ||
		daemonConfig.Server.Authorize(context.Background(), relayadmin.NewPeer(0, nil), relayadmin.OperationSetup) {
		t.Fatal("server authorization is not the Handler absent-state policy")
	}
}

func TestRuntimeUsesExactHelperVersionAdminGIDPeerAndConnectionLimit(t *testing.T) {
	t.Parallel()

	config := validRuntimeTestConfig(&runtimeTestGuard{}, &runtimeTestListener{})
	config.StateDir = filepath.Join(t.TempDir(), "Relay")
	config.HelperVersion = "linked-1.1.0"
	config.AdminGID = 1234
	config.MaxConnections = 19
	var peerCalls atomic.Int32
	config.Peer = func(net.Conn) (relayadmin.Peer, error) {
		peerCalls.Add(1)
		return relayadmin.NewPeer(700, []uint32{1234}), nil
	}
	dependencies := productionRuntimeDependencies()
	var handlerConfig HandlerConfig
	productionHandler := dependencies.newHandler
	dependencies.newHandler = func(config HandlerConfig) (*Handler, error) {
		handlerConfig = config
		return productionHandler(config)
	}
	var daemonConfig DaemonConfig
	dependencies.newDaemon = func(config DaemonConfig) (runtimeDaemon, error) {
		daemonConfig = config
		return runtimeDaemonFunc(func(context.Context) (RunResult, error) { return RunResult{Quiescent: true}, nil }), nil
	}
	runtime, err := newRuntime(config, dependencies)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.state.Close() })
	peer, err := daemonConfig.Peer(nil)
	if err != nil || peer.UID() != 700 || peerCalls.Load() != 1 {
		t.Fatalf("daemon peer extractor = %#v, %v, calls=%d", peer, err, peerCalls.Load())
	}
	if handlerConfig.HelperVersion != config.HelperVersion || handlerConfig.AdminGID != config.AdminGID ||
		handlerConfig.State != runtime.state || handlerConfig.Supervisor != runtime.supervisor ||
		daemonConfig.Listener != config.AdminListener || daemonConfig.Supervisor != runtime.supervisor ||
		daemonConfig.MaxConnections != config.MaxConnections {
		t.Fatalf("handler/daemon configs = %#v / %#v", handlerConfig, daemonConfig)
	}
}

func TestRuntimeReadyStartupReconcilesExistingRelay(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "Relay")
	createRuntimeReadyState(t, stateDir)
	config := validRuntimeTestConfig(&runtimeTestGuard{}, &runtimeTestListener{})
	config.StateDir = stateDir
	var listenCalls atomic.Int32
	var openCalls atomic.Int32
	instance := &recordingRelayInstance{handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}
	config.ListenRelay = func(network, address string) (net.Listener, error) {
		listenCalls.Add(1)
		return net.Listen(network, address)
	}
	config.OpenRelay = func(string) (RelayInstance, error) {
		openCalls.Add(1)
		return instance, nil
	}
	dependencies := productionRuntimeDependencies()
	var observedRunning atomic.Bool
	dependencies.newDaemon = func(config DaemonConfig) (runtimeDaemon, error) {
		return runtimeDaemonFunc(func(ctx context.Context) (RunResult, error) {
			observedRunning.Store(config.Supervisor.Snapshot().RelayRunning)
			if err := config.Supervisor.Stop(ctx); err != nil {
				return RunResult{}, err
			}
			return RunResult{Quiescent: true}, nil
		}), nil
	}
	runtime, err := newRuntime(config, dependencies)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.state.Close() })
	result, err := runtime.Run(context.Background())
	if err != nil || !result.Quiescent || !observedRunning.Load() || listenCalls.Load() != 1 || openCalls.Load() != 1 {
		t.Fatalf("Run() = %#v, %v, running=%t listen/open=%d/%d", result, err, observedRunning.Load(), listenCalls.Load(), openCalls.Load())
	}
}

func TestRuntimeAbsentAndIncompatibleStartupDoNotOpenRelay(t *testing.T) {
	t.Parallel()

	for _, class := range []string{"absent", "incompatible"} {
		class := class
		t.Run(class, func(t *testing.T) {
			t.Parallel()
			stateDir := filepath.Join(t.TempDir(), "Relay")
			if class == "incompatible" {
				if err := os.MkdirAll(stateDir, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			config := validRuntimeTestConfig(&runtimeTestGuard{}, &runtimeTestListener{})
			config.StateDir = stateDir
			var listenCalls atomic.Int32
			var openCalls atomic.Int32
			config.ListenRelay = func(string, string) (net.Listener, error) {
				listenCalls.Add(1)
				return nil, errors.New("relay listener must not open")
			}
			config.OpenRelay = func(string) (RelayInstance, error) {
				openCalls.Add(1)
				return nil, errors.New("relay state must not open")
			}
			dependencies := productionRuntimeDependencies()
			dependencies.newDaemon = func(DaemonConfig) (runtimeDaemon, error) {
				return runtimeDaemonFunc(func(context.Context) (RunResult, error) { return RunResult{Quiescent: true}, nil }), nil
			}
			runtime, err := newRuntime(config, dependencies)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = runtime.state.Close() })
			result, err := runtime.Run(context.Background())
			if err != nil || !result.Quiescent || listenCalls.Load() != 0 || openCalls.Load() != 0 {
				t.Fatalf("Run() = %#v, %v, listen/open=%d/%d", result, err, listenCalls.Load(), openCalls.Load())
			}
		})
	}
}

func TestRuntimeSnapshotOrReconcileFailureKeepsAdminIPCUsableAndRelayRunningFalse(t *testing.T) {
	t.Parallel()

	t.Run("snapshot", func(t *testing.T) {
		t.Parallel()
		config := validRuntimeTestConfig(&runtimeTestGuard{}, &runtimeTestListener{})
		config.StateDir = filepath.Join(t.TempDir(), "Relay")
		dependencies := productionRuntimeDependencies()
		var daemonConfig DaemonConfig
		var daemonRuns atomic.Int32
		dependencies.newDaemon = func(config DaemonConfig) (runtimeDaemon, error) {
			daemonConfig = config
			return runtimeDaemonFunc(func(context.Context) (RunResult, error) {
				daemonRuns.Add(1)
				return RunResult{Quiescent: true}, nil
			}), nil
		}
		runtime, err := newRuntime(config, dependencies)
		if err != nil {
			t.Fatal(err)
		}
		if err := runtime.state.Close(); err != nil {
			t.Fatal(err)
		}
		result, err := runtime.Run(context.Background())
		if err != nil || !result.Quiescent || daemonRuns.Load() != 1 || daemonConfig.Server == nil || runtime.supervisor.Snapshot().RelayRunning {
			t.Fatalf("Run() = %#v, %v, daemon=%d server=%p relay=%t", result, err, daemonRuns.Load(), daemonConfig.Server, runtime.supervisor.Snapshot().RelayRunning)
		}
	})

	t.Run("reconcile", func(t *testing.T) {
		t.Parallel()
		stateDir := filepath.Join(t.TempDir(), "Relay")
		createRuntimeReadyState(t, stateDir)
		config := validRuntimeTestConfig(&runtimeTestGuard{}, &runtimeTestListener{})
		config.StateDir = stateDir
		reconcileFailure := errors.New("bind failed")
		config.ListenRelay = func(string, string) (net.Listener, error) { return nil, reconcileFailure }
		dependencies := productionRuntimeDependencies()
		var daemonConfig DaemonConfig
		var daemonRuns atomic.Int32
		dependencies.newDaemon = func(config DaemonConfig) (runtimeDaemon, error) {
			daemonConfig = config
			return runtimeDaemonFunc(func(context.Context) (RunResult, error) {
				daemonRuns.Add(1)
				return RunResult{Quiescent: true}, nil
			}), nil
		}
		runtime, err := newRuntime(config, dependencies)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = runtime.state.Close() })
		result, err := runtime.Run(context.Background())
		status, statusErr := daemonConfig.Server.Handler.Status(context.Background(), relayadmin.NewPeer(501, nil))
		if err != nil || !result.Quiescent || daemonRuns.Load() != 1 || statusErr != nil || status.RelayRunning || runtime.supervisor.Snapshot().RelayRunning {
			t.Fatalf("Run/status = %#v/%v, %#v/%v daemon=%d relay=%t", result, err, status, statusErr, daemonRuns.Load(), runtime.supervisor.Snapshot().RelayRunning)
		}
	})
}

func TestRuntimeMutationFinishedReconcilesOnlyCoherentReadyState(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "Relay")
	config := validRuntimeTestConfig(&runtimeTestGuard{}, &runtimeTestListener{})
	config.StateDir = stateDir
	var listenCalls atomic.Int32
	instance := &recordingRelayInstance{handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}
	config.ListenRelay = func(network, address string) (net.Listener, error) {
		listenCalls.Add(1)
		return net.Listen(network, address)
	}
	config.OpenRelay = func(string) (RelayInstance, error) { return instance, nil }
	dependencies := productionRuntimeDependencies()
	productionOpen := dependencies.openState
	var mutationFinished func(relayadmin.ReplayKey)
	dependencies.openState = func(options service.AdminStateOptions) (*service.AdminState, error) {
		mutationFinished = options.MutationFinished
		return productionOpen(options)
	}
	dependencies.newDaemon = func(DaemonConfig) (runtimeDaemon, error) {
		return runtimeDaemonFunc(func(context.Context) (RunResult, error) { return RunResult{Quiescent: true}, nil }), nil
	}
	runtime, err := newRuntime(config, dependencies)
	if err != nil {
		t.Fatal(err)
	}
	mutationFinished(relayadmin.ReplayKey{})
	if listenCalls.Load() != 0 {
		t.Fatal("absent state mutation callback opened relay")
	}
	completeRuntimeSetup(t, runtime.state, "d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1")
	if listenCalls.Load() != 1 || !runtime.supervisor.Snapshot().RelayRunning {
		t.Fatalf("ready mutation callback listen=%d running=%t", listenCalls.Load(), runtime.supervisor.Snapshot().RelayRunning)
	}
	stopContext, cancelStop := context.WithTimeout(context.Background(), relayadmin.OperationTimeout)
	if err := runtime.supervisor.Stop(stopContext); err != nil {
		cancelStop()
		t.Fatal(err)
	}
	cancelStop()
	if err := runtime.state.Close(); err != nil {
		t.Fatal(err)
	}
	mutationFinished(relayadmin.ReplayKey{})
	if listenCalls.Load() != 1 {
		t.Fatalf("incoherent state mutation callback listen=%d, want 1", listenCalls.Load())
	}
}

func TestRuntimeCloseBeforeRunClosesStateExactlyOnce(t *testing.T) {
	t.Parallel()

	var closeCalls atomic.Int32
	runtime := newRuntimeWithTestDaemon(t, runtimeTestDaemon{}, func(*service.AdminState) error {
		closeCalls.Add(1)
		return nil
	})
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if closeCalls.Load() != 1 {
		t.Fatalf("state Close calls = %d, want 1", closeCalls.Load())
	}
}

func TestRuntimeCloseWhileRunActiveReturnsNonQuiescentAndRetainsState(t *testing.T) {
	t.Parallel()

	entered := make(chan struct{})
	release := make(chan struct{})
	var closeCalls atomic.Int32
	runtime := newRuntimeWithTestDaemon(t, runtimeDaemonFunc(func(context.Context) (RunResult, error) {
		close(entered)
		<-release
		return RunResult{Quiescent: true}, nil
	}), func(*service.AdminState) error {
		closeCalls.Add(1)
		return nil
	})
	runDone := startRuntimeForTest(context.Background(), runtime)
	<-entered
	if err := runtime.Close(); !errors.Is(err, errRuntimeNonQuiescent) || closeCalls.Load() != 0 {
		t.Fatalf("Close() = %v, state calls=%d", err, closeCalls.Load())
	}
	close(release)
	outcome := waitRuntimeRun(t, runDone)
	if outcome.err != nil || !outcome.result.Quiescent {
		t.Fatalf("Run() = %#v, %v", outcome.result, outcome.err)
	}
	if err := runtime.Close(); err != nil || closeCalls.Load() != 1 {
		t.Fatalf("post-run Close() = %v, state calls=%d", err, closeCalls.Load())
	}
}

func TestRuntimeCloseAfterNonQuiescentRunRetainsStateAndDoesNotRetryDrainOrStop(t *testing.T) {
	t.Parallel()

	var runCalls atomic.Int32
	var closeCalls atomic.Int32
	runtime := newRuntimeWithTestDaemon(t, runtimeDaemonFunc(func(context.Context) (RunResult, error) {
		runCalls.Add(1)
		return RunResult{RestartRequested: true, Quiescent: false}, errors.New("drain failed")
	}), func(*service.AdminState) error {
		closeCalls.Add(1)
		return nil
	})
	result, runErr := runtime.Run(context.Background())
	if runErr == nil || !result.RestartRequested || result.Quiescent {
		t.Fatalf("Run() = %#v, %v", result, runErr)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := runtime.Close(); !errors.Is(err, errRuntimeNonQuiescent) {
			t.Fatalf("Close attempt %d = %v", attempt, err)
		}
	}
	if runCalls.Load() != 1 || closeCalls.Load() != 0 {
		t.Fatalf("daemon Run/state Close calls = %d/%d, want 1/0", runCalls.Load(), closeCalls.Load())
	}
}

func TestRuntimeStateCloseErrorIsStableAcrossRepeatedClose(t *testing.T) {
	t.Parallel()

	closeFailure := errors.New("state close uncertainty")
	var closeCalls atomic.Int32
	runtime := newRuntimeWithTestDaemon(t, runtimeTestDaemon{}, func(*service.AdminState) error {
		closeCalls.Add(1)
		return closeFailure
	})
	first := runtime.Close()
	second := runtime.Close()
	if !errors.Is(first, closeFailure) || first != second || closeCalls.Load() != 1 {
		t.Fatalf("Close() errors = %v/%v, calls=%d", first, second, closeCalls.Load())
	}
}

func TestRuntimeRejectsSecondRunAndRunAfterClose(t *testing.T) {
	t.Parallel()

	runtime := newRuntimeWithTestDaemon(t, runtimeTestDaemon{}, func(*service.AdminState) error { return nil })
	if result, err := runtime.Run(context.Background()); err != nil || !result.Quiescent {
		t.Fatalf("first Run() = %#v, %v", result, err)
	}
	if _, err := runtime.Run(context.Background()); err == nil {
		t.Fatal("second Run() succeeded")
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}

	closed := newRuntimeWithTestDaemon(t, runtimeTestDaemon{}, func(*service.AdminState) error { return nil })
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := closed.Run(context.Background()); err == nil {
		t.Fatal("Run after Close succeeded")
	}
}

func TestRuntimeRepairExitRequiresFlushedServeOutcome(t *testing.T) {
	t.Parallel()

	runtime, listener, request := newRuntimeRepairHarness(t, "e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1")
	connection := newScriptedAdminConn(t, request, -1)
	listener.push(connection)
	outcome := waitRuntimeRun(t, startRuntimeForTest(context.Background(), runtime))
	if outcome.err != nil || !outcome.result.RestartRequested || !outcome.result.Quiescent || !connection.isClosed() {
		t.Fatalf("Run() = %#v, %v, connection closed=%t", outcome.result, outcome.err, connection.isClosed())
	}
	assertRuntimeRepairResponse(t, connection.outputBytes())
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeCachedRepairExitRequiresFlushedServeOutcome(t *testing.T) {
	t.Parallel()

	runtime, listener, request := newRuntimeRepairHarness(t, "e2e2e2e2e2e2e2e2e2e2e2e2e2e2e2e2")
	partial := newScriptedAdminConn(t, request, 5)
	daemon := runtime.daemon.(*Daemon)
	partialOutcome := daemon.server.ServeConn(context.Background(), partial, relayadmin.NewPeer(501, nil))
	if partialOutcome.RepairRestartReady || !partial.isClosed() {
		t.Fatalf("partial ServeConn() = %#v, closed=%t", partialOutcome, partial.isClosed())
	}
	full := newScriptedAdminConn(t, request, -1)
	listener.push(full)
	outcome := waitRuntimeRun(t, startRuntimeForTest(context.Background(), runtime))
	if outcome.err != nil || !outcome.result.RestartRequested || !outcome.result.Quiescent || !full.isClosed() {
		t.Fatalf("cached Run() = %#v, %v, connection closed=%t", outcome.result, outcome.err, full.isClosed())
	}
	assertRuntimeRepairResponse(t, full.outputBytes())
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimePartialRepairWriteDoesNotExitAndCachedRetryCanExit(t *testing.T) {
	t.Parallel()

	runtime, listener, request := newRuntimeRepairHarness(t, "e3e3e3e3e3e3e3e3e3e3e3e3e3e3e3e3")
	runDone := startRuntimeForTest(context.Background(), runtime)
	partial := newScriptedAdminConn(t, request, 5)
	listener.push(partial)
	waitClosedAdminConn(t, partial)
	select {
	case outcome := <-runDone:
		t.Fatalf("Runtime exited after partial response: %#v", outcome)
	default:
	}
	full := newScriptedAdminConn(t, request, -1)
	listener.push(full)
	outcome := waitRuntimeRun(t, runDone)
	if outcome.err != nil || !outcome.result.RestartRequested || !outcome.result.Quiescent || !full.isClosed() {
		t.Fatalf("retry Run() = %#v, %v, connection closed=%t", outcome.result, outcome.err, full.isClosed())
	}
	assertRuntimeRepairResponse(t, full.outputBytes())
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
}

type runtimeTestGuard struct {
	prepareCalls atomic.Int32
	prepareErr   error
	onPrepare    func()
}

func (guard *runtimeTestGuard) Prepare(context.Context) error {
	guard.prepareCalls.Add(1)
	if guard.onPrepare != nil {
		guard.onPrepare()
	}
	return guard.prepareErr
}

func (*runtimeTestGuard) Validate(context.Context) error { return nil }
func (*runtimeTestGuard) Repair(context.Context) error   { return nil }

type runtimeTestListener struct {
	closeCalls atomic.Int32
}

func (*runtimeTestListener) Accept() (net.Conn, error) { return nil, net.ErrClosed }
func (listener *runtimeTestListener) Close() error {
	listener.closeCalls.Add(1)
	return nil
}
func (*runtimeTestListener) Addr() net.Addr { return daemonTestAddr("runtime-test") }

type runtimeTestDaemon struct{}

func (runtimeTestDaemon) Run(context.Context) (RunResult, error) {
	return RunResult{Quiescent: true}, nil
}

type runtimeDaemonFunc func(context.Context) (RunResult, error)

func (function runtimeDaemonFunc) Run(ctx context.Context) (RunResult, error) { return function(ctx) }

type runtimeRunOutcome struct {
	result RunResult
	err    error
}

func startRuntimeForTest(ctx context.Context, runtime *Runtime) <-chan runtimeRunOutcome {
	done := make(chan runtimeRunOutcome, 1)
	go func() {
		result, err := runtime.Run(ctx)
		done <- runtimeRunOutcome{result: result, err: err}
	}()
	return done
}

func waitRuntimeRun(t *testing.T, done <-chan runtimeRunOutcome) runtimeRunOutcome {
	t.Helper()
	select {
	case outcome := <-done:
		return outcome
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for Runtime.Run")
		return runtimeRunOutcome{}
	}
}

func validRuntimeTestConfig(guard PreparedPathGuard, listener net.Listener) RuntimeConfig {
	return RuntimeConfig{
		AdminListener: listener,
		Peer: func(net.Conn) (relayadmin.Peer, error) {
			return relayadmin.NewPeer(501, []uint32{80}), nil
		},
		AdminGID: 80, HelperVersion: "1.1.0", StateDir: "state", RelayAddress: "127.0.0.1:0",
		PathGuard: guard, MaxConnections: 32, ListenRelay: net.Listen,
		OpenRelay: func(string) (RelayInstance, error) { return nil, errors.New("not used") },
	}
}

func validRuntimeTestDependencies() (runtimeDependencies, *[]string) {
	calls := []string{}
	return runtimeDependencies{
		newSupervisor: func(SupervisorConfig) (*Supervisor, error) {
			calls = append(calls, "supervisor")
			return &Supervisor{}, nil
		},
		newMutationFinished: func(func() *service.AdminState, *Supervisor) (func(relayadmin.ReplayKey), error) {
			calls = append(calls, "callback")
			return func(relayadmin.ReplayKey) {}, nil
		},
		openState: func(service.AdminStateOptions) (*service.AdminState, error) {
			calls = append(calls, "state")
			return &service.AdminState{}, nil
		},
		newHandler: func(HandlerConfig) (*Handler, error) {
			calls = append(calls, "handler")
			return &Handler{}, nil
		},
		newDaemon: func(DaemonConfig) (runtimeDaemon, error) {
			calls = append(calls, "daemon")
			return runtimeTestDaemon{}, nil
		},
		closeState: func(*service.AdminState) error { return nil },
	}, &calls
}

func newRuntimeWithTestDaemon(t *testing.T, daemon runtimeDaemon, closeState func(*service.AdminState) error) *Runtime {
	t.Helper()
	dependencies, _ := validRuntimeTestDependencies()
	dependencies.newDaemon = func(DaemonConfig) (runtimeDaemon, error) { return daemon, nil }
	dependencies.closeState = closeState
	runtime, err := newRuntime(validRuntimeTestConfig(&runtimeTestGuard{}, &runtimeTestListener{}), dependencies)
	if err != nil {
		t.Fatal(err)
	}
	return runtime
}

func newRuntimeRepairHarness(t *testing.T, requestID string) (*Runtime, *queuedAdminListener, []byte) {
	t.Helper()
	stateDir := filepath.Join(t.TempDir(), "Relay")
	createRuntimeReadyState(t, stateDir)
	listener := newQueuedAdminListener()
	config := validRuntimeTestConfig(&runtimeTestGuard{}, listener)
	config.StateDir = stateDir
	instance := &recordingRelayInstance{handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}
	config.ListenRelay = net.Listen
	config.OpenRelay = func(string) (RelayInstance, error) { return instance, nil }
	runtime, err := NewRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = runtime.supervisor.Stop(ctx)
		cancel()
		_ = runtime.state.Close()
	})
	request := mustAdminRequest(t, requestID, relayadmin.OperationRepair, relayadmin.RepairRequest{})
	return runtime, listener, request
}

func assertRuntimeRepairResponse(t *testing.T, raw []byte) {
	t.Helper()
	framed, err := relayadmin.ReadFrameExact(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	response := mustAdminResponse(t, framed)
	result, ok := response.Result.(relayadmin.RepairResult)
	if !response.OK || !ok || !result.Ready || !result.Restarting {
		t.Fatalf("repair response = %#v", response)
	}
}

func createRuntimeReadyState(t *testing.T, stateDir string) {
	t.Helper()
	state, err := service.OpenAdminState(service.AdminStateOptions{StateDir: stateDir})
	if err != nil {
		t.Fatal(err)
	}
	completeRuntimeSetup(t, state, "d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0")
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
}

func completeRuntimeSetup(t *testing.T, state *service.AdminState, requestID string) {
	t.Helper()
	key := relayadmin.ReplayKey{
		RequestID: requestID,
		Digest:    sha256.Sum256([]byte("runtime setup " + requestID)),
		Operation: relayadmin.OperationSetup,
	}
	reservation, err := state.ReplayStore().Reserve(context.Background(), key)
	if err != nil || reservation.Decision != relayadmin.ReplayExecute || reservation.Mutation == nil {
		t.Fatalf("setup Reserve() = %#v, %v", reservation, err)
	}
	_, err = reservation.Mutation.Execute(context.Background(), func(ctx context.Context, transaction relayadmin.MutationTransaction) ([]byte, error) {
		result, err := state.BootstrapOwner(ctx, transaction, service.AdminBootstrapOwnerOptions{
			PublicName: "relay.example.test", PublicURL: "https://relay.example.test:8443",
			CSRPEM: newAdminCSR(t, "runtime owner"), AdministrativeOwnerUID: 501,
		})
		if err != nil {
			return nil, err
		}
		return relayadmin.MarshalSuccessResponse(key.RequestID, key.Operation, relayadmin.OwnerBootstrapResult{
			CertificatePEM: result.CertificatePEM, CACertificatePEM: result.CACertificatePEM,
			Serial: result.Serial, Role: string(result.Role),
		})
	})
	if err != nil {
		t.Fatalf("setup Execute() error = %v", err)
	}
}
