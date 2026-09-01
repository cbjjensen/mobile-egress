package adminservice

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"mobile-egress/internal/relayadmin"
	"mobile-egress/relay/internal/service"
)

func TestDaemonCleanCancellationIsQuiescent(t *testing.T) {
	t.Parallel()

	listener := newQueuedAdminListener()
	daemon := mustNewDaemonForTest(t, listener, newDaemonTestServer(&daemonTestHandler{}), newDaemonTestSupervisor(t), 1)
	ctx, cancel := context.WithCancel(context.Background())
	runDone := startDaemonForTest(ctx, daemon)
	cancel()
	outcome := waitDaemonRun(t, runDone)
	if outcome.err != nil || outcome.result.RestartRequested || !outcome.result.Quiescent {
		t.Fatalf("Run() = %#v, %v, want ordinary quiescent cancellation", outcome.result, outcome.err)
	}
}

func TestDaemonAcceptOrListenerFailureCanStillBeQuiescent(t *testing.T) {
	t.Parallel()

	acceptFailure := errors.New("accept failure")
	daemon := mustNewDaemonForTest(t, &immediateAdminErrorListener{err: acceptFailure},
		newDaemonTestServer(&daemonTestHandler{}), newDaemonTestSupervisor(t), 1)
	result, err := daemon.Run(context.Background())
	if !errors.Is(err, acceptFailure) || result.RestartRequested || !result.Quiescent {
		t.Fatalf("Run() = %#v, %v, want independent quiescence with accept failure", result, err)
	}
}

func TestDaemonDrainOrSupervisorStopFailureIsNotQuiescent(t *testing.T) {
	t.Parallel()

	t.Run("drain", func(t *testing.T) {
		handler := &daemonTestHandler{
			entered: make(chan struct{}), canceled: make(chan struct{}), release: make(chan struct{}), waitForCancellation: true,
		}
		server := newDaemonTestServer(handler)
		listener := newQueuedAdminListener()
		listener.push(newScriptedAdminConn(t, mustAdminRequest(t,
			"c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1", relayadmin.OperationStatus, relayadmin.StatusRequest{}), -1))
		daemon := mustNewDaemonForTest(t, listener, server, newDaemonTestSupervisor(t), 1)
		daemon.drainLimit = 20 * time.Millisecond
		ctx, cancel := context.WithCancel(context.Background())
		runDone := startDaemonForTest(ctx, daemon)
		<-handler.entered
		cancel()
		<-handler.canceled
		outcome := waitDaemonRun(t, runDone)
		if outcome.err == nil || outcome.result.Quiescent {
			t.Fatalf("Run() = %#v, %v, want non-quiescent drain failure", outcome.result, outcome.err)
		}
		close(handler.release)
		drainContext, cancelDrain := context.WithTimeout(context.Background(), time.Second)
		defer cancelDrain()
		if err := server.Drain(drainContext); err != nil {
			t.Fatalf("test cleanup Drain() error = %v", err)
		}
	})

	t.Run("supervisor stop", func(t *testing.T) {
		closeFailure := errors.New("relay close uncertainty")
		instance := &recordingRelayInstance{
			handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), closeErr: closeFailure,
		}
		supervisor, err := NewSupervisor(SupervisorConfig{
			StateDir: "state", Address: "127.0.0.1:0", Listen: net.Listen,
			Open: func(string) (RelayInstance, error) { return instance, nil },
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := supervisor.Reconcile(context.Background()); err != nil {
			t.Fatal(err)
		}
		listener := newQueuedAdminListener()
		daemon := mustNewDaemonForTest(t, listener, newDaemonTestServer(&daemonTestHandler{}), supervisor, 1)
		ctx, cancel := context.WithCancel(context.Background())
		runDone := startDaemonForTest(ctx, daemon)
		cancel()
		outcome := waitDaemonRun(t, runDone)
		if !errors.Is(outcome.err, closeFailure) || outcome.result.Quiescent {
			t.Fatalf("Run() = %#v, %v, want non-quiescent supervisor stop failure", outcome.result, outcome.err)
		}
	})
}

func TestDaemonRestartRequestedSurvivesLaterCleanupError(t *testing.T) {
	t.Parallel()

	closeFailure := errors.New("relay close uncertainty")
	instance := &recordingRelayInstance{
		handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), closeErr: closeFailure,
	}
	supervisor, err := NewSupervisor(SupervisorConfig{
		StateDir: "state", Address: "127.0.0.1:0", Listen: net.Listen,
		Open: func(string) (RelayInstance, error) { return instance, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := supervisor.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	listener := newQueuedAdminListener()
	connection := newScriptedAdminConn(t, mustAdminRequest(t,
		"c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2", relayadmin.OperationRepair, relayadmin.RepairRequest{}), -1)
	listener.push(connection)
	daemon := mustNewDaemonForTest(t, listener, newDaemonTestServer(&daemonTestHandler{}), supervisor, 1)
	outcome := waitDaemonRun(t, startDaemonForTest(context.Background(), daemon))
	if !errors.Is(outcome.err, closeFailure) || !outcome.result.RestartRequested || outcome.result.Quiescent {
		t.Fatalf("Run() = %#v, %v, want restart=true independent of non-quiescent cleanup", outcome.result, outcome.err)
	}
}

func TestDaemonClosesPeerFailuresWithoutDispatch(t *testing.T) {
	t.Parallel()

	handler := &daemonTestHandler{}
	server := newDaemonTestServer(handler)
	listener := newQueuedAdminListener()
	connection := newScriptedAdminConn(t, mustAdminRequest(t, "b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1", relayadmin.OperationStatus, relayadmin.StatusRequest{}), -1)
	listener.push(connection)
	supervisor := newDaemonTestSupervisor(t)
	daemon, err := NewDaemon(DaemonConfig{
		Listener: listener,
		Peer: func(net.Conn) (relayadmin.Peer, error) {
			return relayadmin.Peer{}, errors.New("peer extraction failed")
		},
		Server: server, Supervisor: supervisor, MaxConnections: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan daemonRunOutcome, 1)
	go runDaemonForTest(ctx, daemon, runDone)
	waitClosedAdminConn(t, connection)
	cancel()
	outcome := waitDaemonRun(t, runDone)
	if outcome.err != nil || outcome.result.RestartRequested || !outcome.result.Quiescent {
		t.Fatalf("Run() = %#v, %v", outcome.result, outcome.err)
	}
	if handler.statusCalls.Load() != 0 {
		t.Fatalf("status calls = %d, want 0", handler.statusCalls.Load())
	}
}

func TestDaemonUnexpectedAcceptFailureTerminalizesAndStopsRelay(t *testing.T) {
	t.Parallel()

	dataInstance := &recordingRelayInstance{handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}
	supervisor, err := NewSupervisor(SupervisorConfig{
		StateDir: "state", Address: "127.0.0.1:0", Listen: net.Listen,
		Open: func(string) (RelayInstance, error) { return dataInstance, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := supervisor.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	acceptFailure := errors.New("accept failure")
	listener := &immediateAdminErrorListener{err: acceptFailure}
	var peerCalls atomic.Int32
	daemon, err := NewDaemon(DaemonConfig{
		Listener: listener,
		Peer: func(net.Conn) (relayadmin.Peer, error) {
			peerCalls.Add(1)
			return relayadmin.NewPeer(501, []uint32{80}), nil
		},
		Server: newDaemonTestServer(&daemonTestHandler{}), Supervisor: supervisor, MaxConnections: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, runErr := daemon.Run(context.Background())
	if !errors.Is(runErr, acceptFailure) || result.RestartRequested || !result.Quiescent {
		t.Fatalf("Run() = %#v, %v, want accept failure", result, runErr)
	}
	if !supervisor.isTerminal() || supervisor.Snapshot().RelayRunning {
		t.Fatal("accept failure did not terminalize the relay")
	}
	if dataInstance.closeCount() != 1 || listener.closeCalls.Load() != 1 || peerCalls.Load() != 0 {
		t.Fatalf("relay/listener/peer cleanup = %d/%d/%d, want 1/1/0",
			dataInstance.closeCount(), listener.closeCalls.Load(), peerCalls.Load())
	}
}

func TestDaemonEnforcesWorkerCapBeforePeerExtraction(t *testing.T) {
	t.Parallel()

	handler := &daemonTestHandler{entered: make(chan struct{}), release: make(chan struct{})}
	server := newDaemonTestServer(handler)
	listener := newQueuedAdminListener()
	first := newScriptedAdminConn(t, mustAdminRequest(t, "b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2", relayadmin.OperationStatus, relayadmin.StatusRequest{}), -1)
	second := newScriptedAdminConn(t, mustAdminRequest(t, "b3b3b3b3b3b3b3b3b3b3b3b3b3b3b3b3", relayadmin.OperationStatus, relayadmin.StatusRequest{}), -1)
	var peerCalls atomic.Int32
	supervisor := newDaemonTestSupervisor(t)
	daemon, err := NewDaemon(DaemonConfig{
		Listener: listener,
		Peer: func(net.Conn) (relayadmin.Peer, error) {
			peerCalls.Add(1)
			return relayadmin.NewPeer(501, []uint32{80}), nil
		},
		Server: server, Supervisor: supervisor, MaxConnections: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan daemonRunOutcome, 1)
	go runDaemonForTest(ctx, daemon, runDone)
	listener.push(first)
	select {
	case <-handler.entered:
	case <-time.After(time.Second):
		t.Fatal("first handler did not start")
	}
	listener.push(second)
	waitClosedAdminConn(t, second)
	if peerCalls.Load() != 1 || handler.statusCalls.Load() != 1 {
		t.Fatalf("peer/status calls = %d/%d, want 1/1", peerCalls.Load(), handler.statusCalls.Load())
	}
	close(handler.release)
	waitClosedAdminConn(t, first)
	cancel()
	outcome := waitDaemonRun(t, runDone)
	if outcome.err != nil || outcome.result.RestartRequested || !outcome.result.Quiescent {
		t.Fatalf("Run() = %#v, %v", outcome.result, outcome.err)
	}
}

func TestDaemonCancellationTerminalizesThenDrainsLateDispatchBeforeStoppingRelay(t *testing.T) {
	t.Parallel()

	dataInstance := &recordingRelayInstance{handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}
	listenCalls := atomic.Int32{}
	supervisor, err := NewSupervisor(SupervisorConfig{
		StateDir: "state", Address: "127.0.0.1:0",
		Listen: func(network, address string) (net.Listener, error) {
			listenCalls.Add(1)
			return net.Listen(network, address)
		},
		Open: func(string) (RelayInstance, error) { return dataInstance, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := supervisor.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	lateReconcile := make(chan error, 1)
	handler := &daemonTestHandler{
		entered: make(chan struct{}), canceled: make(chan struct{}), release: make(chan struct{}),
		waitForCancellation: true,
		late:                func() { lateReconcile <- supervisor.Reconcile(context.Background()) },
	}
	server := newDaemonTestServer(handler)
	listener := newQueuedAdminListener()
	connection := newScriptedAdminConn(t, mustAdminRequest(t, "b4b4b4b4b4b4b4b4b4b4b4b4b4b4b4b4", relayadmin.OperationStatus, relayadmin.StatusRequest{}), -1)
	listener.push(connection)
	daemon, err := NewDaemon(DaemonConfig{
		Listener: listener,
		Peer:     func(net.Conn) (relayadmin.Peer, error) { return relayadmin.NewPeer(501, []uint32{80}), nil },
		Server:   server, Supervisor: supervisor, MaxConnections: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan daemonRunOutcome, 1)
	go runDaemonForTest(ctx, daemon, runDone)
	select {
	case <-handler.entered:
	case <-time.After(time.Second):
		t.Fatal("handler did not start")
	}
	cancel()
	select {
	case <-handler.canceled:
	case <-time.After(time.Second):
		t.Fatal("handler did not observe cancellation")
	}
	waitForAdminCondition(t, supervisor.isTerminal, "daemon terminal supervisor gate")
	if dataInstance.closeCount() != 0 {
		t.Fatal("data relay closed before admin dispatch drain")
	}
	select {
	case premature := <-runDone:
		t.Fatalf("Run returned before late dispatch drained: %#v", premature)
	case <-time.After(20 * time.Millisecond):
	}
	close(handler.release)
	if err := <-lateReconcile; !errors.Is(err, errSupervisorTerminated) {
		t.Fatalf("late Reconcile error = %v, want terminal", err)
	}
	outcome := waitDaemonRun(t, runDone)
	if outcome.err != nil || outcome.result.RestartRequested || !outcome.result.Quiescent {
		t.Fatalf("Run() = %#v, %v", outcome.result, outcome.err)
	}
	if dataInstance.closeCount() != 1 || listenCalls.Load() != 1 {
		t.Fatalf("relay Close/listen calls = %d/%d, want 1/1", dataInstance.closeCount(), listenCalls.Load())
	}
}

func TestDaemonTerminalizationSealsRelayAdmissionBeforeAdminDrain(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "Relay")
	if _, err := service.Initialize(context.Background(), service.InitOptions{
		StateDir: stateDir, PublicName: "127.0.0.1", PublicURL: "https://127.0.0.1:8443",
	}); err != nil {
		t.Fatal(err)
	}
	realInstance, err := service.Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	var targetCalls atomic.Int32
	dataInstance := &recordingRelayInstance{
		tls: realInstance.TLSConfig(), closeFn: realInstance.Close,
		handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			targetCalls.Add(1)
			writer.WriteHeader(http.StatusNoContent)
		}),
	}
	dataAddress := make(chan string, 1)
	supervisor, err := NewSupervisor(SupervisorConfig{
		StateDir: stateDir, Address: "127.0.0.1:0",
		Listen: func(network, address string) (net.Listener, error) {
			listener, listenErr := net.Listen(network, address)
			if listenErr == nil {
				dataAddress <- listener.Addr().String()
			}
			return listener, listenErr
		},
		Open: func(string) (RelayInstance, error) { return dataInstance, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := supervisor.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}

	adminHandler := &daemonTestHandler{
		entered: make(chan struct{}), canceled: make(chan struct{}), release: make(chan struct{}),
		waitForCancellation: true,
	}
	listener := newQueuedAdminListener()
	listener.push(newScriptedAdminConn(t, mustAdminRequest(t,
		"b9b9b9b9b9b9b9b9b9b9b9b9b9b9b9b9", relayadmin.OperationStatus, relayadmin.StatusRequest{}), -1))
	daemon := mustNewDaemonForTest(t, listener, newDaemonTestServer(adminHandler), supervisor, 1)
	ctx, cancel := context.WithCancel(context.Background())
	runDone := startDaemonForTest(ctx, daemon)
	<-adminHandler.entered
	cancel()
	<-adminHandler.canceled
	waitForAdminCondition(t, supervisor.isTerminal, "daemon terminal relay gate")

	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		MinVersion: tls.VersionTLS13, InsecureSkipVerify: true, // test-only ephemeral relay identity
	}}}
	response, requestErr := client.Get("https://" + <-dataAddress + "/after-terminal")
	if requestErr != nil {
		close(adminHandler.release)
		t.Fatalf("request through terminalized listener error = %v", requestErr)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable {
		close(adminHandler.release)
		t.Fatalf("terminalized relay status = %d, want %d", response.StatusCode, http.StatusServiceUnavailable)
	}
	if targetCalls.Load() != 0 {
		close(adminHandler.release)
		t.Fatalf("target relay handler admitted %d post-terminal requests", targetCalls.Load())
	}
	if dataInstance.closeCount() != 0 {
		close(adminHandler.release)
		t.Fatal("relay instance closed before admin drain completed")
	}

	close(adminHandler.release)
	outcome := waitDaemonRun(t, runDone)
	if outcome.err != nil || outcome.result.RestartRequested || !outcome.result.Quiescent {
		t.Fatalf("Run() = %#v, %v", outcome.result, outcome.err)
	}
	if dataInstance.closeCount() != 1 {
		t.Fatalf("relay Close calls = %d, want 1", dataInstance.closeCount())
	}
}

func TestDaemonReportsDrainTimeoutWithoutClaimingQuiescence(t *testing.T) {
	t.Parallel()

	handler := &daemonTestHandler{
		entered: make(chan struct{}), canceled: make(chan struct{}), release: make(chan struct{}), waitForCancellation: true,
	}
	server := newDaemonTestServer(handler)
	listener := newQueuedAdminListener()
	listener.push(newScriptedAdminConn(t, mustAdminRequest(t, "b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5", relayadmin.OperationStatus, relayadmin.StatusRequest{}), -1))
	supervisor := newDaemonTestSupervisor(t)
	daemon, err := NewDaemon(DaemonConfig{
		Listener: listener,
		Peer:     func(net.Conn) (relayadmin.Peer, error) { return relayadmin.NewPeer(501, []uint32{80}), nil },
		Server:   server, Supervisor: supervisor, MaxConnections: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	daemon.drainLimit = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan daemonRunOutcome, 1)
	go runDaemonForTest(ctx, daemon, runDone)
	<-handler.entered
	cancel()
	<-handler.canceled
	outcome := waitDaemonRun(t, runDone)
	if outcome.err == nil || outcome.result.RestartRequested || outcome.result.Quiescent {
		t.Fatalf("Run() = %#v, %v, want explicit drain error", outcome.result, outcome.err)
	}
	close(handler.release)
	drainContext, cancelDrain := context.WithTimeout(context.Background(), time.Second)
	defer cancelDrain()
	if err := server.Drain(drainContext); err != nil {
		t.Fatalf("cleanup Drain() error = %v", err)
	}
}

func TestDaemonRepairRestartRequiresFullyFlushedNewOrCachedSuccess(t *testing.T) {
	t.Parallel()

	t.Run("new success", func(t *testing.T) {
		handler := &daemonTestHandler{}
		server := newDaemonTestServer(handler)
		listener := newQueuedAdminListener()
		connection := newScriptedAdminConn(t, mustAdminRequest(t, "b6b6b6b6b6b6b6b6b6b6b6b6b6b6b6b6", relayadmin.OperationRepair, relayadmin.RepairRequest{}), -1)
		listener.push(connection)
		daemon := mustNewDaemonForTest(t, listener, server, newDaemonTestSupervisor(t), 2)
		outcome := waitDaemonRun(t, startDaemonForTest(context.Background(), daemon))
		if outcome.err != nil || !outcome.result.RestartRequested || !outcome.result.Quiescent || handler.repairCalls.Load() != 1 || !connection.isClosed() {
			t.Fatalf("Run/result/calls/closed = %#v/%v/%d/%t", outcome.result, outcome.err, handler.repairCalls.Load(), connection.isClosed())
		}
	})

	t.Run("partial then cached success", func(t *testing.T) {
		handler := &daemonTestHandler{}
		server := newDaemonTestServer(handler)
		listener := newQueuedAdminListener()
		request := mustAdminRequest(t, "b7b7b7b7b7b7b7b7b7b7b7b7b7b7b7b7", relayadmin.OperationRepair, relayadmin.RepairRequest{})
		partial := newScriptedAdminConn(t, request, 5)
		full := newScriptedAdminConn(t, request, -1)
		daemon := mustNewDaemonForTest(t, listener, server, newDaemonTestSupervisor(t), 2)
		runDone := startDaemonForTest(context.Background(), daemon)
		listener.push(partial)
		waitClosedAdminConn(t, partial)
		if _, err := relayadmin.ReadFrameExact(bytes.NewReader(partial.outputBytes())); err == nil {
			t.Fatal("partial response unexpectedly formed a complete frame")
		}
		listener.push(full)
		outcome := waitDaemonRun(t, runDone)
		if outcome.err != nil || !outcome.result.RestartRequested || !outcome.result.Quiescent || handler.repairCalls.Load() != 1 || !full.isClosed() {
			t.Fatalf("Run/result/calls/closed = %#v/%v/%d/%t", outcome.result, outcome.err, handler.repairCalls.Load(), full.isClosed())
		}
		responseRaw, err := relayadmin.ReadFrameExact(bytes.NewReader(full.outputBytes()))
		if err != nil {
			t.Fatalf("cached response frame error = %v", err)
		}
		response := mustAdminResponse(t, responseRaw)
		result, ok := response.Result.(relayadmin.RepairResult)
		if !response.OK || !ok || !result.Ready || !result.Restarting {
			t.Fatalf("cached repair response = %#v", response)
		}
	})

	t.Run("ordinary status", func(t *testing.T) {
		handler := &daemonTestHandler{}
		server := newDaemonTestServer(handler)
		listener := newQueuedAdminListener()
		connection := newScriptedAdminConn(t, mustAdminRequest(t, "b8b8b8b8b8b8b8b8b8b8b8b8b8b8b8b8", relayadmin.OperationStatus, relayadmin.StatusRequest{}), -1)
		listener.push(connection)
		daemon := mustNewDaemonForTest(t, listener, server, newDaemonTestSupervisor(t), 2)
		ctx, cancel := context.WithCancel(context.Background())
		runDone := startDaemonForTest(ctx, daemon)
		waitClosedAdminConn(t, connection)
		cancel()
		outcome := waitDaemonRun(t, runDone)
		if outcome.err != nil || outcome.result.RestartRequested || !outcome.result.Quiescent {
			t.Fatalf("Run() = %#v, %v", outcome.result, outcome.err)
		}
	})
}

type daemonRunOutcome struct {
	result RunResult
	err    error
}

func runDaemonForTest(ctx context.Context, daemon *Daemon, result chan<- daemonRunOutcome) {
	value, err := daemon.Run(ctx)
	result <- daemonRunOutcome{result: value, err: err}
}

func startDaemonForTest(ctx context.Context, daemon *Daemon) <-chan daemonRunOutcome {
	result := make(chan daemonRunOutcome, 1)
	go runDaemonForTest(ctx, daemon, result)
	return result
}

func waitDaemonRun(t *testing.T, result <-chan daemonRunOutcome) daemonRunOutcome {
	t.Helper()
	select {
	case outcome := <-result:
		return outcome
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for Daemon.Run")
		return daemonRunOutcome{}
	}
}

func mustNewDaemonForTest(t *testing.T, listener net.Listener, server *relayadmin.Server, supervisor *Supervisor, maximum int) *Daemon {
	t.Helper()
	daemon, err := NewDaemon(DaemonConfig{
		Listener: listener,
		Peer:     func(net.Conn) (relayadmin.Peer, error) { return relayadmin.NewPeer(501, []uint32{80}), nil },
		Server:   server, Supervisor: supervisor, MaxConnections: maximum,
	})
	if err != nil {
		t.Fatal(err)
	}
	return daemon
}

func newDaemonTestSupervisor(t *testing.T) *Supervisor {
	t.Helper()
	supervisor, err := NewSupervisor(SupervisorConfig{
		StateDir: "state", Address: "127.0.0.1:0", Listen: net.Listen,
		Open: func(string) (RelayInstance, error) { return nil, errors.New("unused relay open") },
	})
	if err != nil {
		t.Fatal(err)
	}
	return supervisor
}

func newDaemonTestServer(handler relayadmin.Handler) *relayadmin.Server {
	return &relayadmin.Server{
		Authorize: func(context.Context, relayadmin.Peer, relayadmin.Operation) bool { return true },
		Handler:   handler, Replay: relayadmin.NewMemoryReplayStore(relayadmin.MemoryReplayConfig{}),
		OperationLimit: time.Second, IOLimit: time.Second,
	}
}

type daemonTestHandler struct {
	statusCalls         atomic.Int32
	repairCalls         atomic.Int32
	entered             chan struct{}
	canceled            chan struct{}
	release             chan struct{}
	enterOnce           sync.Once
	cancelOnce          sync.Once
	waitForCancellation bool
	late                func()
}

func (handler *daemonTestHandler) Status(ctx context.Context, _ relayadmin.Peer) (relayadmin.StatusResult, error) {
	handler.statusCalls.Add(1)
	if handler.entered != nil {
		handler.enterOnce.Do(func() { close(handler.entered) })
	}
	if handler.waitForCancellation {
		<-ctx.Done()
		if handler.canceled != nil {
			handler.cancelOnce.Do(func() { close(handler.canceled) })
		}
	}
	if handler.release != nil {
		<-handler.release
	}
	if handler.late != nil {
		handler.late()
	}
	if err := ctx.Err(); err != nil {
		return relayadmin.StatusResult{}, err
	}
	return relayadmin.StatusResult{ProtocolVersion: relayadmin.Version, HelperVersion: "test"}, nil
}

func (*daemonTestHandler) Setup(context.Context, relayadmin.Peer, relayadmin.Mutation, relayadmin.SetupRequest) (relayadmin.OwnerBootstrapResult, error) {
	return relayadmin.OwnerBootstrapResult{}, nil
}

func (*daemonTestHandler) Rotate(context.Context, relayadmin.Peer, relayadmin.Mutation, relayadmin.RotateRequest) (relayadmin.EndpointRotationResult, error) {
	return relayadmin.EndpointRotationResult{}, nil
}

func (handler *daemonTestHandler) Repair(context.Context, relayadmin.Peer, relayadmin.Mutation) (relayadmin.RepairResult, error) {
	handler.repairCalls.Add(1)
	return relayadmin.RepairResult{Ready: true, Restarting: true}, nil
}

type queuedAdminListener struct {
	connections chan net.Conn
	closed      chan struct{}
	closeOnce   sync.Once
}

func newQueuedAdminListener() *queuedAdminListener {
	return &queuedAdminListener{connections: make(chan net.Conn, 16), closed: make(chan struct{})}
}

func (listener *queuedAdminListener) push(connection net.Conn) { listener.connections <- connection }

func (listener *queuedAdminListener) Accept() (net.Conn, error) {
	select {
	case <-listener.closed:
		return nil, net.ErrClosed
	case connection := <-listener.connections:
		return connection, nil
	}
}

func (listener *queuedAdminListener) Close() error {
	listener.closeOnce.Do(func() { close(listener.closed) })
	return nil
}

func (*queuedAdminListener) Addr() net.Addr { return daemonTestAddr("admin") }

type scriptedAdminConn struct {
	mu         sync.Mutex
	reader     *bytes.Reader
	output     bytes.Buffer
	writeLimit int
	written    int
	closed     chan struct{}
	closeOnce  sync.Once
}

func newScriptedAdminConn(t *testing.T, request []byte, writeLimit int) *scriptedAdminConn {
	t.Helper()
	var framed bytes.Buffer
	if err := relayadmin.WriteFrame(&framed, request); err != nil {
		t.Fatal(err)
	}
	return &scriptedAdminConn{
		reader: bytes.NewReader(framed.Bytes()), writeLimit: writeLimit, closed: make(chan struct{}),
	}
}

func (connection *scriptedAdminConn) Read(value []byte) (int, error) {
	connection.mu.Lock()
	defer connection.mu.Unlock()
	return connection.reader.Read(value)
}

func (connection *scriptedAdminConn) Write(value []byte) (int, error) {
	connection.mu.Lock()
	defer connection.mu.Unlock()
	if connection.writeLimit >= 0 {
		remaining := connection.writeLimit - connection.written
		if remaining <= 0 {
			return 0, io.ErrClosedPipe
		}
		if remaining < len(value) {
			written, _ := connection.output.Write(value[:remaining])
			connection.written += written
			return written, io.ErrClosedPipe
		}
	}
	written, err := connection.output.Write(value)
	connection.written += written
	return written, err
}

func (connection *scriptedAdminConn) Close() error {
	connection.closeOnce.Do(func() { close(connection.closed) })
	return nil
}

func (*scriptedAdminConn) LocalAddr() net.Addr              { return daemonTestAddr("local") }
func (*scriptedAdminConn) RemoteAddr() net.Addr             { return daemonTestAddr("remote") }
func (*scriptedAdminConn) SetDeadline(time.Time) error      { return nil }
func (*scriptedAdminConn) SetReadDeadline(time.Time) error  { return nil }
func (*scriptedAdminConn) SetWriteDeadline(time.Time) error { return nil }

func (connection *scriptedAdminConn) isClosed() bool {
	select {
	case <-connection.closed:
		return true
	default:
		return false
	}
}

func (connection *scriptedAdminConn) outputBytes() []byte {
	connection.mu.Lock()
	defer connection.mu.Unlock()
	return append([]byte(nil), connection.output.Bytes()...)
}

func waitClosedAdminConn(t *testing.T, connection *scriptedAdminConn) {
	t.Helper()
	select {
	case <-connection.closed:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for admin connection close")
	}
}

type daemonTestAddr string

func (address daemonTestAddr) Network() string { return "daemon-test" }
func (address daemonTestAddr) String() string  { return string(address) }

type immediateAdminErrorListener struct {
	err        error
	closeCalls atomic.Int32
}

func (listener *immediateAdminErrorListener) Accept() (net.Conn, error) { return nil, listener.err }
func (listener *immediateAdminErrorListener) Close() error {
	listener.closeCalls.Add(1)
	return nil
}
func (*immediateAdminErrorListener) Addr() net.Addr { return daemonTestAddr("immediate-error") }
