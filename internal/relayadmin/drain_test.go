package relayadmin

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"
)

func TestServerDrainWaitsForLateMutationAfterServeConnDeadline(t *testing.T) {
	t.Parallel()

	handler := newDrainTestHandler()
	server := &Server{
		Authorize:      func(context.Context, Peer, Operation) bool { return true },
		Handler:        handler,
		Replay:         NewMemoryReplayStore(MemoryReplayConfig{}),
		OperationLimit: 100 * time.Millisecond,
		IOLimit:        time.Second,
	}
	request, err := MarshalRequest("d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1", OperationSetup, SetupRequest{
		PublicName: "relay.example.test", PublicURL: "https://relay.example.test:8443", OwnerCSRPEM: "csr",
	})
	if err != nil {
		t.Fatal(err)
	}
	connection := newDrainServerConn(t, request)
	serveDone := make(chan struct{})
	go func() {
		server.ServeConn(context.Background(), connection, NewPeer(501, []uint32{80}))
		close(serveDone)
	}()
	select {
	case <-handler.entered:
	case <-time.After(time.Second):
		t.Fatal("mutation handler did not start")
	}
	select {
	case <-serveDone:
	case <-time.After(time.Second):
		t.Fatal("ServeConn did not return at its operation deadline")
	}

	timeoutContext, cancelTimeout := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancelTimeout()
	if err := server.Drain(timeoutContext); err != context.DeadlineExceeded {
		t.Fatalf("Drain(timeout) error = %v, want deadline exceeded", err)
	}
	close(handler.release)
	drainContext, cancelDrain := context.WithTimeout(context.Background(), time.Second)
	defer cancelDrain()
	if err := server.Drain(drainContext); err != nil {
		t.Fatalf("Drain(after release) error = %v", err)
	}
}

func TestServerDrainTracksLateStatusResultAcrossIdleGenerations(t *testing.T) {
	t.Parallel()

	handler := newDrainTestHandler()
	server := &Server{
		Authorize:      func(context.Context, Peer, Operation) bool { return true },
		Handler:        handler,
		Replay:         NewMemoryReplayStore(MemoryReplayConfig{}),
		OperationLimit: time.Second,
		IOLimit:        time.Second,
	}
	for iteration := 0; iteration < 100; iteration++ {
		entered, release := handler.nextGeneration()
		request, err := MarshalRequest(fmt.Sprintf("%032x", iteration+1), OperationStatus, StatusRequest{})
		if err != nil {
			t.Fatal(err)
		}
		connection := newDrainServerConn(t, request)
		serveDone := make(chan struct{})
		serveContext, cancelServe := context.WithCancel(context.Background())
		go func() {
			server.ServeConn(serveContext, connection, NewPeer(501, []uint32{80}))
			close(serveDone)
		}()
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatalf("iteration %d handler did not start", iteration)
		}
		cancelServe()
		select {
		case <-serveDone:
		case <-time.After(time.Second):
			t.Fatalf("iteration %d ServeConn did not return", iteration)
		}
		drainDone := make(chan error, 1)
		go func() { drainDone <- server.Drain(context.Background()) }()
		select {
		case err := <-drainDone:
			t.Fatalf("iteration %d Drain returned before late result: %v", iteration, err)
		case <-time.After(time.Millisecond):
		}
		close(release)
		select {
		case err := <-drainDone:
			if err != nil {
				t.Fatalf("iteration %d Drain error = %v", iteration, err)
			}
		case <-time.After(time.Second):
			t.Fatalf("iteration %d Drain did not observe idle generation", iteration)
		}
	}
}

type drainTestHandler struct {
	mu      sync.Mutex
	entered chan struct{}
	release chan struct{}
}

func newDrainTestHandler() *drainTestHandler {
	return &drainTestHandler{entered: make(chan struct{}), release: make(chan struct{})}
}

func (handler *drainTestHandler) nextGeneration() (<-chan struct{}, chan struct{}) {
	handler.mu.Lock()
	handler.entered = make(chan struct{})
	handler.release = make(chan struct{})
	entered := handler.entered
	release := handler.release
	handler.mu.Unlock()
	return entered, release
}

func (handler *drainTestHandler) waitForCancellation(ctx context.Context) error {
	handler.mu.Lock()
	entered := handler.entered
	release := handler.release
	handler.mu.Unlock()
	close(entered)
	<-ctx.Done()
	<-release
	return ctx.Err()
}

func (handler *drainTestHandler) Status(ctx context.Context, _ Peer) (StatusResult, error) {
	err := handler.waitForCancellation(ctx)
	return StatusResult{ProtocolVersion: Version, HelperVersion: "test"}, err
}

func (handler *drainTestHandler) Setup(ctx context.Context, _ Peer, _ Mutation, _ SetupRequest) (OwnerBootstrapResult, error) {
	return OwnerBootstrapResult{}, handler.waitForCancellation(ctx)
}

func (*drainTestHandler) Rotate(context.Context, Peer, Mutation, RotateRequest) (EndpointRotationResult, error) {
	return EndpointRotationResult{}, nil
}

func (*drainTestHandler) Repair(context.Context, Peer, Mutation) (RepairResult, error) {
	return RepairResult{}, nil
}

type drainServerConn struct {
	mu     sync.Mutex
	reader *bytes.Reader
	writes bytes.Buffer
	closed bool
}

func newDrainServerConn(t *testing.T, request []byte) *drainServerConn {
	t.Helper()
	var framed bytes.Buffer
	if err := WriteFrame(&framed, request); err != nil {
		t.Fatal(err)
	}
	return &drainServerConn{reader: bytes.NewReader(framed.Bytes())}
}

func (connection *drainServerConn) Read(value []byte) (int, error) {
	connection.mu.Lock()
	defer connection.mu.Unlock()
	return connection.reader.Read(value)
}

func (connection *drainServerConn) Write(value []byte) (int, error) {
	connection.mu.Lock()
	defer connection.mu.Unlock()
	return connection.writes.Write(value)
}

func (connection *drainServerConn) Close() error {
	connection.mu.Lock()
	connection.closed = true
	connection.mu.Unlock()
	return nil
}

func (*drainServerConn) LocalAddr() net.Addr              { return drainAddr("local") }
func (*drainServerConn) RemoteAddr() net.Addr             { return drainAddr("remote") }
func (*drainServerConn) SetDeadline(time.Time) error      { return nil }
func (*drainServerConn) SetReadDeadline(time.Time) error  { return nil }
func (*drainServerConn) SetWriteDeadline(time.Time) error { return nil }

type drainAddr string

func (address drainAddr) Network() string { return "drain" }
func (address drainAddr) String() string  { return string(address) }
