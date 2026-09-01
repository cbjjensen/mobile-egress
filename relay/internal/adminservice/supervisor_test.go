package adminservice

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"mobile-egress/relay/internal/service"
)

func TestSupervisorRejectsNonLoopbackAddressBeforeUsingSeams(t *testing.T) {
	t.Parallel()

	called := false
	_, err := NewSupervisor(SupervisorConfig{
		StateDir: "state",
		Address:  "0.0.0.0:8443",
		Listen: func(string, string) (net.Listener, error) {
			called = true
			return nil, errors.New("must not be called")
		},
		Open: func(string) (RelayInstance, error) {
			called = true
			return nil, errors.New("must not be called")
		},
	})
	if err == nil {
		t.Fatal("NewSupervisor accepted a non-loopback address")
	}
	if called {
		t.Fatal("NewSupervisor used injected seams before rejecting address")
	}
}

func TestSupervisorRejectsInjectedNonLoopbackListener(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("net.Listen(non-loopback) error = %v", err)
	}
	recorded := &recordingListener{Listener: listener}
	openCalls := 0
	supervisor, err := NewSupervisor(SupervisorConfig{
		StateDir: "state",
		Address:  "127.0.0.1:8443",
		Listen:   func(string, string) (net.Listener, error) { return recorded, nil },
		Open: func(string) (RelayInstance, error) {
			openCalls++
			return &recordingRelayInstance{handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}, nil
		},
	})
	if err != nil {
		t.Fatalf("NewSupervisor() error = %v", err)
	}
	if err := supervisor.Reconcile(context.Background()); err == nil {
		t.Fatal("Reconcile accepted a non-loopback listener returned by the seam")
	}
	if openCalls != 0 {
		t.Fatalf("Open calls = %d, want 0", openCalls)
	}
	if err := listener.Close(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("rejected listener was not closed: %v", err)
	}
}

func TestSupervisorBindsBeforeOpenAndClosesResourcesOnOpenFailure(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var events []string
	listener := mustLoopbackListener(t)
	recorded := &recordingListener{Listener: listener, onClose: func() {
		mu.Lock()
		events = append(events, "listener-close")
		mu.Unlock()
	}}
	supervisor, err := NewSupervisor(SupervisorConfig{
		StateDir: "state",
		Address:  "127.0.0.1:8443",
		Listen: func(network, address string) (net.Listener, error) {
			mu.Lock()
			events = append(events, "listen:"+network+":"+address)
			mu.Unlock()
			return recorded, nil
		},
		Open: func(path string) (RelayInstance, error) {
			mu.Lock()
			events = append(events, "open:"+path)
			mu.Unlock()
			return nil, errors.New("raw open secret")
		},
	})
	if err != nil {
		t.Fatalf("NewSupervisor() error = %v", err)
	}
	if err := supervisor.Reconcile(context.Background()); err == nil {
		t.Fatal("Reconcile() succeeded despite service-open failure")
	}
	if supervisor.Snapshot().RelayRunning {
		t.Fatal("failed reconcile reported relay running")
	}

	mu.Lock()
	defer mu.Unlock()
	want := []string{"listen:tcp:127.0.0.1:8443", "open:state", "listener-close"}
	if len(events) != len(want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	for index := range want {
		if events[index] != want[index] {
			t.Fatalf("events = %v, want %v", events, want)
		}
	}
}

func TestSupervisorStartStopIsIdempotentAndClosesServiceAfterServe(t *testing.T) {
	t.Parallel()

	listener := mustLoopbackListener(t)
	instance := &recordingRelayInstance{handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}
	listenCalls := 0
	openCalls := 0
	supervisor, err := NewSupervisor(SupervisorConfig{
		StateDir: "state",
		Address:  "127.0.0.1:0",
		Listen: func(string, string) (net.Listener, error) {
			listenCalls++
			return listener, nil
		},
		Open: func(string) (RelayInstance, error) {
			openCalls++
			return instance, nil
		},
	})
	if err != nil {
		t.Fatalf("NewSupervisor() error = %v", err)
	}
	if err := supervisor.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if !supervisor.Snapshot().RelayRunning {
		t.Fatal("started supervisor did not report running")
	}
	if err := supervisor.Reconcile(context.Background()); err != nil {
		t.Fatalf("second Reconcile() error = %v", err)
	}
	if listenCalls != 1 || openCalls != 1 {
		t.Fatalf("idempotent Reconcile called listen/open %d/%d times", listenCalls, openCalls)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := supervisor.Stop(ctx); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if supervisor.Snapshot().RelayRunning {
		t.Fatal("stopped supervisor still reports running")
	}
	if instance.closeCount() != 1 {
		t.Fatalf("service Close calls = %d, want 1", instance.closeCount())
	}
	if err := supervisor.Stop(ctx); err != nil {
		t.Fatalf("second Stop() error = %v", err)
	}
	if instance.closeCount() != 1 {
		t.Fatalf("idempotent Stop service Close calls = %d, want 1", instance.closeCount())
	}
}

func TestSupervisorServesRealRelayTLSOnExplicitLoopbackListener(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "Relay")
	if _, err := service.Initialize(context.Background(), service.InitOptions{
		StateDir: stateDir, PublicName: "127.0.0.1", PublicURL: "https://127.0.0.1:8443",
	}); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	address := make(chan string, 1)
	supervisor, err := NewSupervisor(SupervisorConfig{
		StateDir: stateDir,
		Address:  "127.0.0.1:0",
		Listen: func(network, requested string) (net.Listener, error) {
			listener, err := net.Listen(network, requested)
			if err == nil {
				address <- listener.Addr().String()
			}
			return listener, err
		},
		Open: func(path string) (RelayInstance, error) { return service.Open(path) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := supervisor.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = supervisor.Stop(ctx)
	}()

	caPEM, err := service.CACertificatePEM(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		t.Fatal("failed to load relay CA")
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		MinVersion: tls.VersionTLS13, RootCAs: roots, ServerName: "127.0.0.1",
	}}}
	response, err := client.Get("https://" + <-address + "/healthz")
	if err != nil {
		t.Fatalf("TLS health request error = %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("health status = %d: %s", response.StatusCode, body)
	}
}

func TestSupervisorBindConflictDoesNotOpenService(t *testing.T) {
	t.Parallel()

	occupied := mustLoopbackListener(t)
	openCalls := 0
	supervisor, err := NewSupervisor(SupervisorConfig{
		StateDir: "state", Address: occupied.Addr().String(), Listen: net.Listen,
		Open: func(string) (RelayInstance, error) {
			openCalls++
			return &recordingRelayInstance{handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := supervisor.Reconcile(context.Background()); err == nil {
		t.Fatal("Reconcile() succeeded despite bind conflict")
	}
	if openCalls != 0 || supervisor.Snapshot().RelayRunning {
		t.Fatalf("openCalls/running = %d/%t", openCalls, supervisor.Snapshot().RelayRunning)
	}
}

func TestSupervisorImmediateServeFailureClosesServiceAndCanReconcile(t *testing.T) {
	t.Parallel()

	firstListener := &immediateErrorListener{err: errors.New("immediate serve failure")}
	secondListener := mustLoopbackListener(t)
	firstInstance := &recordingRelayInstance{handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}
	secondInstance := &recordingRelayInstance{handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}
	listenCalls := 0
	openCalls := 0
	supervisor, err := NewSupervisor(SupervisorConfig{
		StateDir: "state", Address: "127.0.0.1:0",
		Listen: func(string, string) (net.Listener, error) {
			listenCalls++
			if listenCalls == 1 {
				return firstListener, nil
			}
			return secondListener, nil
		},
		Open: func(string) (RelayInstance, error) {
			openCalls++
			if openCalls == 1 {
				return firstInstance, nil
			}
			return secondInstance, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := supervisor.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForAdminCondition(t, func() bool {
		return !supervisor.Snapshot().RelayRunning && firstInstance.closeCount() == 1
	}, "immediate Serve failure cleanup")
	if err := supervisor.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile after Serve failure error = %v", err)
	}
	if !supervisor.Snapshot().RelayRunning || listenCalls != 2 || openCalls != 2 {
		t.Fatalf("running/listen/open = %t/%d/%d", supervisor.Snapshot().RelayRunning, listenCalls, openCalls)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := supervisor.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if secondInstance.closeCount() != 1 {
		t.Fatalf("second instance Close calls = %d", secondInstance.closeCount())
	}
}

func TestSupervisorImmediateServeCloseFailureIsStickyAndBlocksReplacement(t *testing.T) {
	t.Parallel()

	closeFailure := errors.New("relay close uncertainty")
	replacementAttempt := errors.New("replacement listener attempted")
	instance := &recordingRelayInstance{
		handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), closeErr: closeFailure,
	}
	var listenCalls atomic.Int32
	var openCalls atomic.Int32
	supervisor, err := NewSupervisor(SupervisorConfig{
		StateDir: "state", Address: "127.0.0.1:0",
		Listen: func(string, string) (net.Listener, error) {
			if listenCalls.Add(1) == 1 {
				return &immediateErrorListener{err: errors.New("immediate serve failure")}, nil
			}
			return nil, replacementAttempt
		},
		Open: func(string) (RelayInstance, error) {
			openCalls.Add(1)
			return instance, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := supervisor.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForAdminCondition(t, func() bool {
		return !supervisor.Snapshot().RelayRunning && instance.closeCount() == 1
	}, "failed immediate-Serve instance close")
	for attempt := 0; attempt < 2; attempt++ {
		if err := supervisor.Reconcile(context.Background()); !errors.Is(err, closeFailure) {
			t.Fatalf("Reconcile attempt %d error = %v, want close uncertainty", attempt, err)
		}
		if err := supervisor.Stop(context.Background()); !errors.Is(err, closeFailure) {
			t.Fatalf("Stop attempt %d error = %v, want close uncertainty", attempt, err)
		}
	}
	if listenCalls.Load() != 1 || openCalls.Load() != 1 || instance.closeCount() != 1 {
		t.Fatalf("listen/open/close = %d/%d/%d, want sticky 1/1/1",
			listenCalls.Load(), openCalls.Load(), instance.closeCount())
	}
}

func TestSupervisorOrdinaryStopCloseFailureIsStickyAndNeverClosesTwice(t *testing.T) {
	t.Parallel()

	closeFailure := errors.New("relay close uncertainty")
	instance := &recordingRelayInstance{
		handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), closeErr: closeFailure,
	}
	var listenCalls atomic.Int32
	var openCalls atomic.Int32
	supervisor, err := NewSupervisor(SupervisorConfig{
		StateDir: "state", Address: "127.0.0.1:0",
		Listen: func(network, address string) (net.Listener, error) {
			listenCalls.Add(1)
			return net.Listen(network, address)
		},
		Open: func(string) (RelayInstance, error) {
			openCalls.Add(1)
			return instance, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := supervisor.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := supervisor.Stop(context.Background()); !errors.Is(err, closeFailure) {
			t.Fatalf("Stop attempt %d error = %v, want close uncertainty", attempt, err)
		}
	}
	if supervisor.currentRun() == nil {
		t.Fatal("Stop forgot the finalized run after Close uncertainty")
	}
	if err := supervisor.Reconcile(context.Background()); !errors.Is(err, closeFailure) {
		t.Fatalf("Reconcile() error = %v, want close uncertainty", err)
	}
	if listenCalls.Load() != 1 || openCalls.Load() != 1 || instance.closeCount() != 1 {
		t.Fatalf("listen/open/close = %d/%d/%d, want sticky 1/1/1",
			listenCalls.Load(), openCalls.Load(), instance.closeCount())
	}
}

func TestSupervisorPublishesStoppedBeforeDrainingAndClosesServiceAfterHandler(t *testing.T) {
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
	entered := make(chan struct{})
	release := make(chan struct{})
	instance := &recordingRelayInstance{
		tls: realInstance.TLSConfig(), closeFn: realInstance.Close,
		handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			close(entered)
			<-release
			writer.WriteHeader(http.StatusNoContent)
		}),
	}
	address := make(chan string, 1)
	supervisor, err := NewSupervisor(SupervisorConfig{
		StateDir: stateDir, Address: "127.0.0.1:0",
		Listen: func(network, requested string) (net.Listener, error) {
			listener, err := net.Listen(network, requested)
			if err == nil {
				address <- listener.Addr().String()
			}
			return listener, err
		},
		Open: func(string) (RelayInstance, error) { return instance, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := supervisor.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		MinVersion: tls.VersionTLS13, InsecureSkipVerify: true, // test-only ephemeral relay identity
	}}}
	requestDone := make(chan error, 1)
	go func() {
		response, err := client.Get("https://" + <-address + "/block")
		if err == nil {
			_ = response.Body.Close()
		}
		requestDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not start")
	}
	stopDone := make(chan error, 1)
	go func() { stopDone <- supervisor.Stop(context.Background()) }()
	waitForAdminCondition(t, func() bool { return !supervisor.Snapshot().RelayRunning }, "stopped snapshot")
	if instance.closeCount() != 0 {
		t.Fatal("service closed while handler was active")
	}
	close(release)
	if err := <-requestDone; err != nil {
		t.Fatalf("blocking request error = %v", err)
	}
	if err := <-stopDone; err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if instance.closeCount() != 1 {
		t.Fatalf("service Close calls = %d, want 1", instance.closeCount())
	}
}

func TestSupervisorStopDeadlineLeavesInstanceOpenUntilNonCooperativeHandlerExits(t *testing.T) {
	t.Parallel()

	supervisor, instance, entered, release, address := newBlockingTLSSupervisor(t)
	requestDone := startBlockingTLSRequest(t, address, entered)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	stopDone := make(chan error, 1)
	go func() { stopDone <- supervisor.Stop(ctx) }()
	select {
	case err := <-stopDone:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Stop() error = %v, want deadline exceeded", err)
		}
	case <-time.After(time.Second):
		close(release)
		t.Fatal("Stop() exceeded its deadline while a handler ignored cancellation")
	}
	if supervisor.Snapshot().RelayRunning {
		t.Fatal("timed-out Stop still reports relay running")
	}
	if instance.closeCount() != 0 {
		t.Fatal("timed-out Stop closed the relay instance while its handler was active")
	}
	if supervisor.currentRun() == nil {
		t.Fatal("timed-out Stop forgot the still-draining relay run")
	}

	close(release)
	<-requestDone
	waitForAdminCondition(t, func() bool { return instance.closeCount() == 1 }, "relay close after handler release")
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Second)
	defer cleanupCancel()
	if err := supervisor.Stop(cleanupCtx); err != nil {
		t.Fatalf("Stop() cleanup error = %v", err)
	}
	if instance.closeCount() != 1 {
		t.Fatalf("relay Close calls = %d, want exactly 1", instance.closeCount())
	}
}

func TestSupervisorReconcileWaitForDrainingRunIsBoundedByContext(t *testing.T) {
	t.Parallel()

	supervisor, instance, entered, release, address := newBlockingTLSSupervisor(t)
	requestDone := startBlockingTLSRequest(t, address, entered)
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer stopCancel()
	if err := supervisor.Stop(stopCtx); !errors.Is(err, context.DeadlineExceeded) {
		close(release)
		t.Fatalf("Stop() error = %v, want deadline exceeded", err)
	}

	reconcileCtx, reconcileCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer reconcileCancel()
	started := time.Now()
	if err := supervisor.Reconcile(reconcileCtx); !errors.Is(err, context.DeadlineExceeded) {
		close(release)
		t.Fatalf("Reconcile() error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		close(release)
		t.Fatalf("Reconcile() exceeded bounded wait: %v", elapsed)
	}
	if instance.closeCount() != 0 {
		close(release)
		t.Fatal("bounded Reconcile closed the instance while its handler was active")
	}
	close(release)
	<-requestDone
	waitForAdminCondition(t, func() bool { return instance.closeCount() == 1 }, "draining relay close")
}

func TestSupervisorTerminateDuringCurrentRunWaitPreventsNewListenOrOpen(t *testing.T) {
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
	entered := make(chan struct{})
	release := make(chan struct{})
	first := &recordingRelayInstance{
		tls: realInstance.TLSConfig(), closeFn: realInstance.Close,
		handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			close(entered)
			<-release
		}),
	}
	listenCalls := 0
	openCalls := 0
	address := make(chan string, 1)
	supervisor, err := NewSupervisor(SupervisorConfig{
		StateDir: stateDir, Address: "127.0.0.1:0",
		Listen: func(network, requested string) (net.Listener, error) {
			listenCalls++
			listener, listenErr := net.Listen(network, requested)
			if listenErr == nil {
				address <- listener.Addr().String()
			}
			return listener, listenErr
		},
		Open: func(string) (RelayInstance, error) {
			openCalls++
			if openCalls == 1 {
				return first, nil
			}
			return &recordingRelayInstance{handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := supervisor.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	requestDone := startBlockingTLSRequest(t, address, entered)
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer stopCancel()
	if err := supervisor.Stop(stopCtx); !errors.Is(err, context.DeadlineExceeded) {
		close(release)
		t.Fatalf("Stop() error = %v, want deadline exceeded", err)
	}

	reconcileDone := make(chan error, 1)
	go func() { reconcileDone <- supervisor.Reconcile(context.Background()) }()
	time.Sleep(20 * time.Millisecond)
	if listenCalls != 1 || openCalls != 1 {
		close(release)
		t.Fatalf("Reconcile bypassed the draining run: listen/open = %d/%d", listenCalls, openCalls)
	}
	supervisor.terminate()
	close(release)
	<-requestDone
	select {
	case err := <-reconcileDone:
		if !errors.Is(err, errSupervisorTerminated) {
			t.Fatalf("Reconcile() error = %v, want terminal shutdown", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Reconcile remained blocked after the old run drained")
	}
	cleanupContext, cancelCleanup := context.WithTimeout(context.Background(), time.Second)
	defer cancelCleanup()
	if err := supervisor.Stop(cleanupContext); err != nil {
		t.Fatalf("cleanup Stop() error = %v", err)
	}
	if listenCalls != 1 || openCalls != 1 {
		t.Fatalf("terminalized Reconcile started a replacement: listen/open = %d/%d", listenCalls, openCalls)
	}
	if first.closeCount() != 1 {
		t.Fatalf("first relay Close calls = %d, want 1", first.closeCount())
	}
}

func TestSupervisorTerminalNotificationInterruptsPriorRunWaitAndReleasesLifecycle(t *testing.T) {
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
	entered := make(chan struct{})
	release := make(chan struct{})
	instance := &recordingRelayInstance{
		tls: realInstance.TLSConfig(), closeFn: realInstance.Close,
		handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			close(entered)
			<-release
		}),
	}
	var listenCalls atomic.Int32
	var openCalls atomic.Int32
	address := make(chan string, 1)
	supervisor, err := NewSupervisor(SupervisorConfig{
		StateDir: stateDir, Address: "127.0.0.1:0",
		Listen: func(network, requested string) (net.Listener, error) {
			listenCalls.Add(1)
			listener, listenErr := net.Listen(network, requested)
			if listenErr == nil {
				address <- listener.Addr().String()
			}
			return listener, listenErr
		},
		Open: func(string) (RelayInstance, error) {
			openCalls.Add(1)
			return instance, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := supervisor.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	requestDone := startBlockingTLSRequest(t, address, entered)
	stopContext, cancelStop := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelStop()
	if err := supervisor.Stop(stopContext); !errors.Is(err, context.DeadlineExceeded) {
		close(release)
		t.Fatalf("initial Stop() error = %v, want deadline exceeded", err)
	}

	waitContext := newDoneCallBarrierContext(2)
	reconcileDone := make(chan error, 1)
	go func() { reconcileDone <- supervisor.Reconcile(waitContext) }()
	waitSignal(t, waitContext.reached, "Reconcile current.done select")
	supervisor.terminate()
	select {
	case err := <-reconcileDone:
		if !errors.Is(err, errSupervisorTerminated) {
			close(release)
			t.Fatalf("Reconcile() error = %v, want terminal shutdown", err)
		}
	case <-time.After(250 * time.Millisecond):
		close(release)
		<-requestDone
		<-reconcileDone
		t.Fatal("terminalization did not interrupt the prior-run wait")
	}
	if instance.closeCount() != 0 {
		close(release)
		t.Fatal("terminal notification closed the instance before its admitted handler returned")
	}

	close(release)
	<-requestDone
	cleanupContext, cancelCleanup := context.WithTimeout(context.Background(), time.Second)
	defer cancelCleanup()
	if err := supervisor.Stop(cleanupContext); err != nil {
		t.Fatalf("bounded cleanup Stop() error = %v", err)
	}
	if listenCalls.Load() != 1 || openCalls.Load() != 1 || instance.closeCount() != 1 {
		t.Fatalf("listen/open/close = %d/%d/%d, want 1/1/1",
			listenCalls.Load(), openCalls.Load(), instance.closeCount())
	}
}

func TestSupervisorTerminalShutdownRejectsLateReconcile(t *testing.T) {
	t.Parallel()

	listenCalls := 0
	openCalls := 0
	var instances []*recordingRelayInstance
	supervisor, err := NewSupervisor(SupervisorConfig{
		StateDir: "state", Address: "127.0.0.1:0",
		Listen: func(network, address string) (net.Listener, error) {
			listenCalls++
			return net.Listen(network, address)
		},
		Open: func(string) (RelayInstance, error) {
			openCalls++
			instance := &recordingRelayInstance{handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}
			instances = append(instances, instance)
			return instance, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := supervisor.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := supervisor.shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown() error = %v", err)
	}
	if supervisor.Snapshot().RelayRunning {
		t.Fatal("terminal shutdown still reports running")
	}
	if err := supervisor.Reconcile(context.Background()); err == nil {
		t.Fatal("late Reconcile succeeded after terminal shutdown")
	}
	if listenCalls != 1 || openCalls != 1 || len(instances) != 1 || instances[0].closeCount() != 1 {
		t.Fatalf("listen/open/instances/close = %d/%d/%d/%d", listenCalls, openCalls, len(instances), instances[0].closeCount())
	}
}

func TestSupervisorTerminalShutdownDuringOpenCleansWithoutPublishing(t *testing.T) {
	t.Parallel()

	listener := &recordingListener{Listener: mustLoopbackListener(t)}
	openEntered := make(chan struct{})
	releaseOpen := make(chan struct{})
	instance := &recordingRelayInstance{handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}
	supervisor, err := NewSupervisor(SupervisorConfig{
		StateDir: "state", Address: "127.0.0.1:0",
		Listen: func(string, string) (net.Listener, error) { return listener, nil },
		Open: func(string) (RelayInstance, error) {
			close(openEntered)
			<-releaseOpen
			return instance, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	reconcileDone := make(chan error, 1)
	go func() { reconcileDone <- supervisor.Reconcile(context.Background()) }()
	select {
	case <-openEntered:
	case <-time.After(time.Second):
		t.Fatal("Open did not start")
	}
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- supervisor.shutdown(context.Background()) }()
	waitForAdminCondition(t, supervisor.isTerminal, "terminal gate during Open")
	close(releaseOpen)
	if err := <-reconcileDone; err == nil {
		t.Fatal("Reconcile published a relay after terminal shutdown began")
	}
	if err := <-shutdownDone; err != nil {
		t.Fatalf("shutdown() error = %v", err)
	}
	if supervisor.Snapshot().RelayRunning || instance.closeCount() != 1 {
		t.Fatalf("running/close = %t/%d", supervisor.Snapshot().RelayRunning, instance.closeCount())
	}
	if err := listener.Listener.Close(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("listener not closed after terminal race: %v", err)
	}
}

func TestSupervisorLifecycleGateHonorsConcurrentCallerDeadlines(t *testing.T) {
	t.Parallel()

	listener := mustLoopbackListener(t)
	openEntered := make(chan struct{})
	releaseOpen := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseOpen) }) }
	instance := &recordingRelayInstance{handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}
	var listenCalls atomic.Int32
	var openCalls atomic.Int32
	supervisor, err := NewSupervisor(SupervisorConfig{
		StateDir: "state", Address: "127.0.0.1:0",
		Listen: func(string, string) (net.Listener, error) {
			listenCalls.Add(1)
			return listener, nil
		},
		Open: func(string) (RelayInstance, error) {
			openCalls.Add(1)
			close(openEntered)
			<-releaseOpen
			return instance, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	firstDone := make(chan error, 1)
	go func() { firstDone <- supervisor.Reconcile(context.Background()) }()
	waitSignal(t, openEntered, "first Reconcile Open")

	type lifecycleResult struct {
		operation string
		err       error
	}
	results := make(chan lifecycleResult, 2)
	stopContext, cancelStop := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelStop()
	reconcileContext, cancelReconcile := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelReconcile()
	go func() { results <- lifecycleResult{operation: "Stop", err: supervisor.Stop(stopContext)} }()
	go func() {
		results <- lifecycleResult{operation: "Reconcile", err: supervisor.Reconcile(reconcileContext)}
	}()

	for completed := 0; completed < 2; completed++ {
		select {
		case result := <-results:
			if !errors.Is(result.err, context.DeadlineExceeded) {
				release()
				t.Fatalf("concurrent %s error = %v, want deadline exceeded", result.operation, result.err)
			}
		case <-time.After(250 * time.Millisecond):
			release()
			<-firstDone
			t.Fatal("lifecycle caller remained blocked after its context deadline")
		}
	}
	if listenCalls.Load() != 1 || openCalls.Load() != 1 {
		release()
		t.Fatalf("timed-out callers acquired resources: listen/open = %d/%d", listenCalls.Load(), openCalls.Load())
	}

	release()
	if err := <-firstDone; err != nil {
		t.Fatalf("first Reconcile() error = %v", err)
	}
	if err := supervisor.Stop(context.Background()); err != nil {
		t.Fatalf("cleanup Stop() error = %v", err)
	}
}

func TestSupervisorOrdinaryStopRemainsRestartable(t *testing.T) {
	t.Parallel()

	listenCalls := 0
	openCalls := 0
	supervisor, err := NewSupervisor(SupervisorConfig{
		StateDir: "state", Address: "127.0.0.1:0",
		Listen: func(network, address string) (net.Listener, error) {
			listenCalls++
			return net.Listen(network, address)
		},
		Open: func(string) (RelayInstance, error) {
			openCalls++
			return &recordingRelayInstance{handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for cycle := 0; cycle < 2; cycle++ {
		if err := supervisor.Reconcile(context.Background()); err != nil {
			t.Fatalf("cycle %d Reconcile() error = %v", cycle, err)
		}
		if err := supervisor.Stop(context.Background()); err != nil {
			t.Fatalf("cycle %d Stop() error = %v", cycle, err)
		}
	}
	if listenCalls != 2 || openCalls != 2 {
		t.Fatalf("listen/open calls = %d/%d, want 2/2", listenCalls, openCalls)
	}
}

func newBlockingTLSSupervisor(t *testing.T) (*Supervisor, *recordingRelayInstance, <-chan struct{}, chan struct{}, <-chan string) {
	t.Helper()
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
	entered := make(chan struct{})
	release := make(chan struct{})
	instance := &recordingRelayInstance{
		tls: realInstance.TLSConfig(), closeFn: realInstance.Close,
		handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			close(entered)
			<-release
		}),
	}
	address := make(chan string, 1)
	supervisor, err := NewSupervisor(SupervisorConfig{
		StateDir: stateDir, Address: "127.0.0.1:0",
		Listen: func(network, requested string) (net.Listener, error) {
			listener, listenErr := net.Listen(network, requested)
			if listenErr == nil {
				address <- listener.Addr().String()
			}
			return listener, listenErr
		},
		Open: func(string) (RelayInstance, error) { return instance, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := supervisor.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	return supervisor, instance, entered, release, address
}

func startBlockingTLSRequest(t *testing.T, address <-chan string, entered <-chan struct{}) <-chan error {
	t.Helper()
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		MinVersion: tls.VersionTLS13, InsecureSkipVerify: true, // test-only ephemeral relay identity
	}}}
	done := make(chan error, 1)
	go func() {
		response, err := client.Get("https://" + <-address + "/block")
		if err == nil {
			_ = response.Body.Close()
		}
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not start")
	}
	return done
}

type recordingListener struct {
	net.Listener
	onClose func()
	once    sync.Once
}

func (listener *recordingListener) Close() error {
	listener.once.Do(func() {
		if listener.onClose != nil {
			listener.onClose()
		}
	})
	return listener.Listener.Close()
}

type recordingRelayInstance struct {
	mu       sync.Mutex
	handler  http.Handler
	tls      *tls.Config
	closed   int
	closeErr error
	closeFn  func() error
}

func (instance *recordingRelayInstance) Handler() http.Handler { return instance.handler }

func (instance *recordingRelayInstance) TLSConfig() *tls.Config {
	if instance.tls != nil {
		return instance.tls
	}
	return &tls.Config{MinVersion: tls.VersionTLS13}
}

func (instance *recordingRelayInstance) Close() error {
	instance.mu.Lock()
	instance.closed++
	closeErr := instance.closeErr
	closeFn := instance.closeFn
	instance.mu.Unlock()
	if closeFn != nil {
		closeErr = errors.Join(closeErr, closeFn())
	}
	return closeErr
}

type immediateErrorListener struct {
	err  error
	once sync.Once
}

type doneCallBarrierContext struct {
	done      <-chan struct{}
	wantCall  int32
	doneCalls atomic.Int32
	reached   chan struct{}
	once      sync.Once
}

func newDoneCallBarrierContext(wantCall int32) *doneCallBarrierContext {
	return &doneCallBarrierContext{done: make(chan struct{}), wantCall: wantCall, reached: make(chan struct{})}
}

func (ctx *doneCallBarrierContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (ctx *doneCallBarrierContext) Done() <-chan struct{} {
	if ctx.doneCalls.Add(1) == ctx.wantCall {
		ctx.once.Do(func() { close(ctx.reached) })
	}
	return ctx.done
}
func (*doneCallBarrierContext) Err() error    { return nil }
func (*doneCallBarrierContext) Value(any) any { return nil }

func (listener *immediateErrorListener) Accept() (net.Conn, error) { return nil, listener.err }
func (listener *immediateErrorListener) Close() error {
	listener.once.Do(func() {})
	return nil
}
func (listener *immediateErrorListener) Addr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8443}
}

func waitForAdminCondition(t *testing.T, condition func() bool, description string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", description)
}

func (instance *recordingRelayInstance) closeCount() int {
	instance.mu.Lock()
	defer instance.mu.Unlock()
	return instance.closed
}

func mustLoopbackListener(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	return listener
}
