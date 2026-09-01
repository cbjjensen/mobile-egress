package main

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"os/user"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"

	"mobile-egress/internal/relayadmin"
	"mobile-egress/relay/internal/adminservice"
)

func TestDaemonRejectsEveryArgumentBeforePlatformSideEffects(t *testing.T) {
	t.Parallel()

	tokens := [][]string{
		{"--flag"}, {"--"}, {"positional"}, {"--state-dir", "elsewhere"}, {"--socket=elsewhere"},
		{"--listen", "127.0.0.1:1"}, {""}, {" "}, {"\t"}, {"daemon"},
	}
	for _, tokens := range tokens {
		tokens := append([]string(nil), tokens...)
		t.Run(strings.Join(tokens, "_"), func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			arguments := append([]string{"daemon"}, tokens...)
			status := runWithDaemon(arguments, &stdout, &stderr, func() (adminservice.RunResult, error) {
				calls.Add(1)
				return adminservice.RunResult{}, nil
			})
			if status != 2 || calls.Load() != 0 || stdout.Len() != 0 {
				t.Fatalf("runWithDaemon(%q) = status %d calls %d stdout %q stderr %q",
					arguments, status, calls.Load(), stdout.String(), stderr.String())
			}
		})
	}
}

func TestDaemonRefusesNonRootBeforeUmaskOrLookup(t *testing.T) {
	t.Parallel()

	dependencies, operations := validDaemonTestDependencies()
	dependencies.effectiveUID = func() int {
		*operations = append(*operations, "euid")
		return 501
	}
	result, err := executeDaemon(dependencies.daemonDependencies)
	if err == nil || result != (adminservice.RunResult{}) || !reflect.DeepEqual(*operations, []string{"euid"}) {
		t.Fatalf("executeDaemon() = %#v, %v, operations=%v", result, err, *operations)
	}
}

func TestDaemonSetsUmaskBeforeGroupSignalSocketOrGoroutine(t *testing.T) {
	t.Parallel()

	dependencies, operations := validDaemonTestDependencies()
	socketFailure := errors.New("socket unavailable")
	dependencies.openSocket = func(context.Context, uint32) (daemonSocket, error) {
		*operations = append(*operations, "socket")
		return nil, socketFailure
	}
	_, err := executeDaemon(dependencies.daemonDependencies)
	want := []string{"euid", "umask:077", "lookup:admin", "notify", "socket", "signal-stop"}
	if !errors.Is(err, socketFailure) || !reflect.DeepEqual(*operations, want) {
		t.Fatalf("executeDaemon() error=%v operations=%v, want %v", err, *operations, want)
	}
}

func TestDaemonLooksUpAdminAndRequiresCanonicalNonzeroGID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		group *user.Group
		err   error
	}{
		{name: "lookup error", err: errors.New("lookup failed")},
		{name: "nil group"},
		{name: "empty", group: &user.Group{Gid: ""}},
		{name: "zero", group: &user.Group{Gid: "0"}},
		{name: "leading zero", group: &user.Group{Gid: "080"}},
		{name: "negative", group: &user.Group{Gid: "-1"}},
		{name: "overflow", group: &user.Group{Gid: "4294967296"}},
		{name: "spaces", group: &user.Group{Gid: " 80"}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			dependencies, operations := validDaemonTestDependencies()
			dependencies.lookupGroup = func(name string) (*user.Group, error) {
				*operations = append(*operations, "lookup:"+name)
				return test.group, test.err
			}
			if _, err := executeDaemon(dependencies.daemonDependencies); err == nil {
				t.Fatal("executeDaemon() accepted invalid admin group")
			}
			if !reflect.DeepEqual(*operations, []string{"euid", "umask:077", "lookup:admin"}) {
				t.Fatalf("operations = %v", *operations)
			}
		})
	}
}

func TestDaemonCreatesSignalContextWithInterruptAndSIGTERMBeforeSocket(t *testing.T) {
	t.Parallel()

	dependencies, operations := validDaemonTestDependencies()
	var gotSignals []os.Signal
	dependencies.notifyContext = func(parent context.Context, signals ...os.Signal) (context.Context, context.CancelFunc) {
		*operations = append(*operations, "notify")
		gotSignals = append([]os.Signal(nil), signals...)
		ctx, cancel := context.WithCancel(parent)
		return ctx, func() {
			*operations = append(*operations, "signal-stop")
			cancel()
		}
	}
	dependencies.openSocket = func(context.Context, uint32) (daemonSocket, error) {
		*operations = append(*operations, "socket")
		return nil, errors.New("stop after socket")
	}
	_, _ = executeDaemon(dependencies.daemonDependencies)
	if len(gotSignals) != 2 || gotSignals[0] != os.Interrupt || gotSignals[1] != syscall.SIGTERM {
		t.Fatalf("signals = %#v", gotSignals)
	}
	if !reflect.DeepEqual(*operations, []string{"euid", "umask:077", "lookup:admin", "notify", "socket", "signal-stop"}) {
		t.Fatalf("operations = %v", *operations)
	}
}

func TestDaemonBindsSocketBeforeGuardPrepareOrStateOpen(t *testing.T) {
	t.Parallel()

	dependencies, operations := validDaemonTestDependencies()
	guard := &daemonTestGuard{prepare: func() { *operations = append(*operations, "prepare") }}
	dependencies.newPathGuard = func() (adminservice.PreparedPathGuard, error) {
		*operations = append(*operations, "guard")
		return guard, nil
	}
	dependencies.newRuntime = func(config adminservice.RuntimeConfig) (daemonRuntime, error) {
		*operations = append(*operations, "runtime")
		if err := config.PathGuard.Prepare(context.Background()); err != nil {
			return nil, err
		}
		*operations = append(*operations, "state-open")
		return &daemonTestRuntime{operations: operations}, nil
	}
	result, err := executeDaemon(dependencies.daemonDependencies)
	if err != nil || !result.Quiescent {
		t.Fatalf("executeDaemon() = %#v, %v", result, err)
	}
	wantPrefix := []string{"euid", "umask:077", "lookup:admin", "notify", "socket", "guard", "runtime", "prepare", "state-open"}
	if len(*operations) < len(wantPrefix) || !reflect.DeepEqual((*operations)[:len(wantPrefix)], wantPrefix) {
		t.Fatalf("operations = %v, want prefix %v", *operations, wantPrefix)
	}
}

func TestDaemonPassesExactListenerStateAddressVersionGIDPeerAndLimit(t *testing.T) {
	dependencies, operations := validDaemonTestDependencies()
	oldVersion := version
	version = "linked-v1.1.0"
	t.Cleanup(func() { version = oldVersion })

	var captured adminservice.RuntimeConfig
	dependencies.newRuntime = func(config adminservice.RuntimeConfig) (daemonRuntime, error) {
		*operations = append(*operations, "runtime")
		captured = config
		return &daemonTestRuntime{operations: operations}, nil
	}
	result, err := executeDaemon(dependencies.daemonDependencies)
	if err != nil || !result.Quiescent {
		t.Fatalf("executeDaemon() = %#v, %v", result, err)
	}
	peer, peerErr := captured.Peer(nil)
	_, listenErr := captured.ListenRelay("tcp", "probe")
	_, openErr := captured.OpenRelay("probe")
	if captured.AdminListener != dependencies.testSocket.Listener() || captured.PathGuard != dependencies.testGuard ||
		captured.AdminGID != 80 || captured.HelperVersion != version ||
		captured.StateDir != adminservice.DarwinRelayStateDir || captured.RelayAddress != darwinRelayAddress ||
		captured.MaxConnections != darwinAdminMaxConnections || peerErr != nil || peer.UID() != 501 ||
		!errors.Is(listenErr, errDaemonTestListen) || !errors.Is(openErr, errDaemonTestOpen) {
		t.Fatalf("runtime config = %#v, peer=%#v/%v listen/open=%v/%v", captured, peer, peerErr, listenErr, openErr)
	}
}

func TestDaemonDoesNotExposePrivilegedFlagsOrChangeGeneralUsage(t *testing.T) {
	t.Parallel()

	var usage bytes.Buffer
	writeUsage(&usage)
	want := "usage: relay <bootstrap-owner|rotate-endpoint|serve|--version> [flags]\n"
	if usage.String() != want {
		t.Fatalf("usage = %q, want %q", usage.String(), want)
	}
	for _, forbidden := range []string{"daemon", "state-dir", "socket", "listen", "uid", "gid", "max-connections"} {
		if strings.Contains(usage.String(), forbidden) {
			t.Fatalf("usage advertises privileged input %q: %q", forbidden, usage.String())
		}
	}
}

func TestDaemonOtherPlatformFailsClosedWithoutChangingLegacyCommands(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if status := run([]string{"daemon"}, &stdout, &stderr); status != 1 || stdout.Len() != 0 || stderr.String() != daemonUnavailableMessage+"\n" {
		t.Fatalf("daemon = status %d stdout %q stderr %q", status, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if status := run([]string{"version"}, &stdout, &stderr); status != 0 || strings.TrimSpace(stdout.String()) != version || stderr.Len() != 0 {
		t.Fatalf("version = status %d stdout %q stderr %q", status, stdout.String(), stderr.String())
	}
}

func TestDaemonSIGTERMCleanupOrder(t *testing.T) {
	t.Parallel()

	dependencies, operations := validDaemonTestDependencies()
	dependencies.notifyContext = func(parent context.Context, _ ...os.Signal) (context.Context, context.CancelFunc) {
		*operations = append(*operations, "notify")
		ctx, cancel := context.WithCancel(parent)
		cancel()
		return ctx, func() { *operations = append(*operations, "signal-stop") }
	}
	runtime := &daemonTestRuntime{operations: operations}
	runtime.onRun = func(ctx context.Context) {
		if !errors.Is(ctx.Err(), context.Canceled) {
			t.Errorf("runtime context error = %v, want canceled", ctx.Err())
		}
	}
	dependencies.newRuntime = func(adminservice.RuntimeConfig) (daemonRuntime, error) {
		*operations = append(*operations, "runtime")
		return runtime, nil
	}
	result, err := executeDaemon(dependencies.daemonDependencies)
	want := []string{
		"euid", "umask:077", "lookup:admin", "notify", "socket", "guard", "runtime",
		"run", "runtime-close", "socket-close", "signal-stop",
	}
	if err != nil || !result.Quiescent || !reflect.DeepEqual(*operations, want) {
		t.Fatalf("executeDaemon() = %#v, %v, operations=%v, want %v", result, err, *operations, want)
	}
}

func TestDaemonInvalidSignalContextStopsAvailableSignalDelivery(t *testing.T) {
	t.Parallel()

	dependencies, operations := validDaemonTestDependencies()
	dependencies.notifyContext = func(context.Context, ...os.Signal) (context.Context, context.CancelFunc) {
		*operations = append(*operations, "notify")
		return nil, func() { *operations = append(*operations, "signal-stop") }
	}
	if _, err := executeDaemon(dependencies.daemonDependencies); err == nil {
		t.Fatal("executeDaemon() accepted an invalid signal context")
	}
	want := []string{"euid", "umask:077", "lookup:admin", "notify", "signal-stop"}
	if !reflect.DeepEqual(*operations, want) {
		t.Fatalf("operations = %v, want %v", *operations, want)
	}
}

func TestDaemonCleanRestartReturnsZeroOnlyAfterRuntimeSocketAndSignalCleanup(t *testing.T) {
	t.Parallel()

	dependencies, operations := validDaemonTestDependencies()
	runtime := &daemonTestRuntime{
		operations: operations,
		result:     adminservice.RunResult{RestartRequested: true, Quiescent: true},
	}
	dependencies.newRuntime = func(adminservice.RuntimeConfig) (daemonRuntime, error) {
		*operations = append(*operations, "runtime")
		return runtime, nil
	}
	var stderr bytes.Buffer
	status := runDaemon(nil, &stderr, func() (adminservice.RunResult, error) {
		return executeDaemon(dependencies.daemonDependencies)
	})
	if status != 0 || stderr.Len() != 0 || runtime.closeCalls.Load() != 1 ||
		dependencies.testSocket.closeCalls.Load() != 1 || (*operations)[len(*operations)-1] != "signal-stop" {
		t.Fatalf("runDaemon() = status %d stderr %q operations=%v runtime/socket closes=%d/%d",
			status, stderr.String(), *operations, runtime.closeCalls.Load(), dependencies.testSocket.closeCalls.Load())
	}
}

func TestDaemonRunErrorStillAttemptsRuntimeAndSocketCleanup(t *testing.T) {
	t.Parallel()

	dependencies, operations := validDaemonTestDependencies()
	runFailure := errors.New("runtime run failed")
	runtime := &daemonTestRuntime{
		operations: operations, result: adminservice.RunResult{Quiescent: true}, runErr: runFailure,
	}
	dependencies.newRuntime = func(adminservice.RuntimeConfig) (daemonRuntime, error) {
		*operations = append(*operations, "runtime")
		return runtime, nil
	}
	result, err := executeDaemon(dependencies.daemonDependencies)
	if !errors.Is(err, runFailure) || !result.Quiescent || runtime.closeCalls.Load() != 1 ||
		dependencies.testSocket.closeCalls.Load() != 1 || (*operations)[len(*operations)-1] != "signal-stop" {
		t.Fatalf("executeDaemon() = %#v, %v, operations=%v", result, err, *operations)
	}
}

func TestDaemonNonQuiescentOutcomeRetainsStateButCleansSocketAndFails(t *testing.T) {
	t.Parallel()

	dependencies, operations := validDaemonTestDependencies()
	retainedState := errors.New("runtime retained non-quiescent state")
	runtime := &daemonTestRuntime{
		operations: operations,
		result:     adminservice.RunResult{RestartRequested: true, Quiescent: false},
		closeErr:   retainedState,
	}
	dependencies.newRuntime = func(adminservice.RuntimeConfig) (daemonRuntime, error) {
		*operations = append(*operations, "runtime")
		return runtime, nil
	}
	result, err := executeDaemon(dependencies.daemonDependencies)
	if !errors.Is(err, retainedState) || !errors.Is(err, errDaemonNonQuiescent) ||
		!result.RestartRequested || result.Quiescent || runtime.closeCalls.Load() != 1 ||
		dependencies.testSocket.closeCalls.Load() != 1 || (*operations)[len(*operations)-1] != "signal-stop" {
		t.Fatalf("executeDaemon() = %#v, %v, operations=%v", result, err, *operations)
	}
}

func TestDaemonRuntimeCloseFailureOverridesRestartSuccess(t *testing.T) {
	t.Parallel()

	dependencies, operations := validDaemonTestDependencies()
	closeFailure := errors.New("state close failed")
	runtime := &daemonTestRuntime{
		operations: operations,
		result:     adminservice.RunResult{RestartRequested: true, Quiescent: true},
		closeErr:   closeFailure,
	}
	dependencies.newRuntime = func(adminservice.RuntimeConfig) (daemonRuntime, error) {
		*operations = append(*operations, "runtime")
		return runtime, nil
	}
	result, err := executeDaemon(dependencies.daemonDependencies)
	if !errors.Is(err, closeFailure) || !result.RestartRequested || !result.Quiescent || dependencies.testSocket.closeCalls.Load() != 1 {
		t.Fatalf("executeDaemon() = %#v, %v, operations=%v", result, err, *operations)
	}
}

func TestDaemonSocketOrLockCleanupFailureOverridesRestartSuccess(t *testing.T) {
	t.Parallel()

	dependencies, operations := validDaemonTestDependencies()
	cleanupFailure := errors.New("socket lock cleanup failed")
	dependencies.testSocket.closeErr = cleanupFailure
	runtime := &daemonTestRuntime{
		operations: operations,
		result:     adminservice.RunResult{RestartRequested: true, Quiescent: true},
	}
	dependencies.newRuntime = func(adminservice.RuntimeConfig) (daemonRuntime, error) {
		*operations = append(*operations, "runtime")
		return runtime, nil
	}
	result, err := executeDaemon(dependencies.daemonDependencies)
	if !errors.Is(err, cleanupFailure) || !result.RestartRequested || !result.Quiescent || runtime.closeCalls.Load() != 1 {
		t.Fatalf("executeDaemon() = %#v, %v, operations=%v", result, err, *operations)
	}
}

func TestDaemonConstructionFailureClosesSocketBeforeStoppingSignals(t *testing.T) {
	t.Parallel()

	dependencies, operations := validDaemonTestDependencies()
	constructionFailure := errors.New("runtime construction failed")
	cleanupFailure := errors.New("socket cleanup failed")
	dependencies.testSocket.closeErr = cleanupFailure
	dependencies.newRuntime = func(adminservice.RuntimeConfig) (daemonRuntime, error) {
		*operations = append(*operations, "runtime")
		return nil, constructionFailure
	}
	_, err := executeDaemon(dependencies.daemonDependencies)
	wantSuffix := []string{"runtime", "socket-close", "signal-stop"}
	gotSuffix := (*operations)[len(*operations)-len(wantSuffix):]
	if !errors.Is(err, constructionFailure) || !errors.Is(err, cleanupFailure) || !reflect.DeepEqual(gotSuffix, wantSuffix) {
		t.Fatalf("executeDaemon() error=%v operations=%v", err, *operations)
	}
}

func TestDaemonExecutionJoinsPrimaryRuntimeAndSocketErrors(t *testing.T) {
	t.Parallel()

	dependencies, operations := validDaemonTestDependencies()
	runFailure := errors.New("primary run failure")
	runtimeCloseFailure := errors.New("runtime close failure")
	socketCloseFailure := errors.New("socket close failure")
	dependencies.testSocket.closeErr = socketCloseFailure
	runtime := &daemonTestRuntime{
		operations: operations, result: adminservice.RunResult{Quiescent: true},
		runErr: runFailure, closeErr: runtimeCloseFailure,
	}
	dependencies.newRuntime = func(adminservice.RuntimeConfig) (daemonRuntime, error) {
		*operations = append(*operations, "runtime")
		return runtime, nil
	}
	_, err := executeDaemon(dependencies.daemonDependencies)
	for _, want := range []error{runFailure, runtimeCloseFailure, socketCloseFailure} {
		if !errors.Is(err, want) {
			t.Fatalf("joined error %v does not preserve %v", err, want)
		}
	}
	if runtime.closeCalls.Load() != 1 || dependencies.testSocket.closeCalls.Load() != 1 || (*operations)[len(*operations)-1] != "signal-stop" {
		t.Fatalf("cleanup calls/order = %d/%d %v", runtime.closeCalls.Load(), dependencies.testSocket.closeCalls.Load(), *operations)
	}
}

func TestDaemonStderrNeverContainsInjectedRawErrorsOrPaths(t *testing.T) {
	t.Parallel()

	raw := errors.New("AWS_SECRET_ACCESS_KEY=/raw/private/relay-state PRIVATE KEY")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	status := runWithDaemon([]string{"daemon"}, &stdout, &stderr, func() (adminservice.RunResult, error) {
		return adminservice.RunResult{RestartRequested: true, Quiescent: true}, raw
	})
	if status != 1 || stdout.Len() != 0 || stderr.String() != daemonUnavailableMessage+"\n" {
		t.Fatalf("daemon error = status %d stdout %q stderr %q", status, stdout.String(), stderr.String())
	}
	for _, forbidden := range []string{"AWS_SECRET_ACCESS_KEY", "/raw/private/relay-state", "PRIVATE KEY", raw.Error()} {
		if strings.Contains(stderr.String(), forbidden) {
			t.Fatalf("stderr leaked %q: %q", forbidden, stderr.String())
		}
	}
}

var (
	errDaemonTestListen = errors.New("test relay listen")
	errDaemonTestOpen   = errors.New("test relay open")
)

type daemonTestDependencies struct {
	daemonDependencies
	testSocket *daemonTestSocket
	testGuard  *daemonTestGuard
}

func validDaemonTestDependencies() (daemonTestDependencies, *[]string) {
	operations := []string{}
	socket := &daemonTestSocket{listener: &daemonTestListener{}, operations: &operations}
	guard := &daemonTestGuard{}
	dependencies := daemonTestDependencies{testSocket: socket, testGuard: guard}
	dependencies.daemonDependencies = daemonDependencies{
		effectiveUID: func() int {
			operations = append(operations, "euid")
			return 0
		},
		setUmask: func(mask int) int {
			operations = append(operations, "umask:0"+strings.TrimPrefix(strings.ToLower(strconv.FormatInt(int64(mask), 8)), "0"))
			return 0
		},
		lookupGroup: func(name string) (*user.Group, error) {
			operations = append(operations, "lookup:"+name)
			return &user.Group{Gid: "80"}, nil
		},
		notifyContext: func(parent context.Context, _ ...os.Signal) (context.Context, context.CancelFunc) {
			operations = append(operations, "notify")
			ctx, cancel := context.WithCancel(parent)
			return ctx, func() {
				operations = append(operations, "signal-stop")
				cancel()
			}
		},
		openSocket: func(context.Context, uint32) (daemonSocket, error) {
			operations = append(operations, "socket")
			return socket, nil
		},
		newPathGuard: func() (adminservice.PreparedPathGuard, error) {
			operations = append(operations, "guard")
			return guard, nil
		},
		newRuntime: func(adminservice.RuntimeConfig) (daemonRuntime, error) {
			operations = append(operations, "runtime")
			return &daemonTestRuntime{operations: &operations}, nil
		},
		peer: func(net.Conn) (relayadmin.Peer, error) {
			return relayadmin.NewPeer(501, []uint32{80}), nil
		},
		listenRelay: func(string, string) (net.Listener, error) { return nil, errDaemonTestListen },
		openRelay:   func(string) (adminservice.RelayInstance, error) { return nil, errDaemonTestOpen },
	}
	return dependencies, &operations
}

type daemonTestGuard struct {
	prepare func()
}

func (guard *daemonTestGuard) Prepare(context.Context) error {
	if guard.prepare != nil {
		guard.prepare()
	}
	return nil
}
func (*daemonTestGuard) Validate(context.Context) error { return nil }
func (*daemonTestGuard) Repair(context.Context) error   { return nil }

type daemonTestListener struct{}

func (*daemonTestListener) Accept() (net.Conn, error) { return nil, net.ErrClosed }
func (*daemonTestListener) Close() error              { return nil }
func (*daemonTestListener) Addr() net.Addr            { return daemonTestAddress("admin") }

type daemonTestAddress string

func (address daemonTestAddress) Network() string { return "test" }
func (address daemonTestAddress) String() string  { return string(address) }

type daemonTestSocket struct {
	listener   net.Listener
	operations *[]string
	closeErr   error
	closeCalls atomic.Int32
}

func (socket *daemonTestSocket) Listener() net.Listener { return socket.listener }
func (socket *daemonTestSocket) Close() error {
	socket.closeCalls.Add(1)
	if socket.operations != nil {
		*socket.operations = append(*socket.operations, "socket-close")
	}
	return socket.closeErr
}

type daemonTestRuntime struct {
	operations *[]string
	result     adminservice.RunResult
	runErr     error
	closeErr   error
	onRun      func(context.Context)
	runCalls   atomic.Int32
	closeCalls atomic.Int32
}

func (runtime *daemonTestRuntime) Run(ctx context.Context) (adminservice.RunResult, error) {
	return runtime.run(ctx)
}

func (runtime *daemonTestRuntime) run(ctx context.Context) (adminservice.RunResult, error) {
	runtime.runCalls.Add(1)
	if runtime.operations != nil {
		*runtime.operations = append(*runtime.operations, "run")
	}
	if runtime.onRun != nil {
		runtime.onRun(ctx)
	}
	result := runtime.result
	if result == (adminservice.RunResult{}) {
		result.Quiescent = true
	}
	return result, runtime.runErr
}

func (runtime *daemonTestRuntime) Close() error {
	runtime.closeCalls.Add(1)
	if runtime.operations != nil {
		*runtime.operations = append(*runtime.operations, "runtime-close")
	}
	return runtime.closeErr
}
