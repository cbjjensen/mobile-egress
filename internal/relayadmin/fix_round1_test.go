package relayadmin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMutationReservationIsDurableBeforeHandlerAndCompletionFailureNeverReexecutes(t *testing.T) {
	t.Parallel()

	backend := newDurableStyleBackend()
	store := &durableStyleReplayStore{backend: backend, failCompletion: true}
	var executions atomic.Int32
	var captured Mutation
	handler := newFixRoundHandler()
	handler.setup = func(_ context.Context, _ Peer, mutation Mutation, _ SetupRequest) (OwnerBootstrapResult, error) {
		executions.Add(1)
		captured = mutation
		if state := backend.state(mutation.Key.RequestID); state != durableExecuting {
			t.Errorf("durable state at handler entry = %d, want executing reservation", state)
		}
		if mutation.Transaction == nil || mutation.Transaction.ReplayKey() != mutation.Key {
			t.Errorf("mutation transaction does not expose the reserved replay key")
		}
		return OwnerBootstrapResult{CertificatePEM: "owner", CACertificatePEM: "ca", Serial: "1", Role: "owner"}, nil
	}
	server := newTestServer(handler, store)
	raw, err := MarshalRequest(testRequestID, OperationSetup, SetupRequest{PublicName: "name", PublicURL: "url", OwnerCSRPEM: "csr"})
	if err != nil {
		t.Fatal(err)
	}
	request, err := ParseRequest(raw)
	if err != nil {
		t.Fatal(err)
	}

	response := parseServerResponse(t, exchangeServer(t, server, NewPeer(501, nil), raw))
	if response.OK || response.ErrorCode != ErrorUnavailable {
		t.Fatalf("completion failure response = %#v, want unavailable", response)
	}
	if executions.Load() != 1 {
		t.Fatalf("handler executions = %d, want 1", executions.Load())
	}
	wantKey := ReplayKey{RequestID: request.RequestID, Digest: request.Digest, Operation: request.Operation}
	if captured.Key != wantKey {
		t.Fatalf("handler mutation key = %#v, want %#v", captured.Key, wantKey)
	}
	if backend.state(testRequestID) != durableIndeterminate {
		t.Fatalf("completion failure did not retain an indeterminate reservation")
	}

	reopened := &durableStyleReplayStore{backend: backend}
	reopenedServer := newTestServer(newFixRoundHandler(), reopened)
	retry := parseServerResponse(t, exchangeServer(t, reopenedServer, NewPeer(501, nil), raw))
	if retry.OK || retry.ErrorCode != ErrorBusy {
		t.Fatalf("reopened identical retry = %#v, want busy", retry)
	}
	differentRaw, _ := MarshalRequest(testRequestID, OperationSetup, SetupRequest{PublicName: "different", PublicURL: "url", OwnerCSRPEM: "csr"})
	different := parseServerResponse(t, exchangeServer(t, reopenedServer, NewPeer(501, nil), differentRaw))
	if different.OK || different.ErrorCode != ErrorDuplicateRequest {
		t.Fatalf("reopened different digest = %#v, want duplicate_request", different)
	}
	if executions.Load() != 1 {
		t.Fatal("reopened reservation permitted mutation reexecution")
	}
}

func TestMutationCompletionCancellationRetainsReservationAndSuppressesSuccess(t *testing.T) {
	t.Parallel()

	backend := newDurableStyleBackend()
	commitEntered := make(chan struct{})
	store := &durableStyleReplayStore{backend: backend, waitForCompletionCancellation: true, commitEntered: commitEntered}
	var executions atomic.Int32
	handler := newFixRoundHandler()
	handler.repair = func(context.Context, Peer, Mutation) (RepairResult, error) {
		executions.Add(1)
		return RepairResult{Ready: true}, nil
	}
	server := newTestServer(handler, store)
	server.OperationLimit = 20 * time.Millisecond
	server.IOLimit = time.Second
	raw, _ := MarshalRequest(testRequestID, OperationRepair, RepairRequest{})

	serverSide, clientSide := tcpConnectionPair(t)
	serverDone := make(chan struct{})
	go func() {
		server.ServeConn(context.Background(), serverSide, NewPeer(501, nil))
		close(serverDone)
	}()
	if err := WriteFrame(clientSide, raw); err != nil {
		t.Fatal(err)
	}
	if err := clientSide.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	<-commitEntered
	_ = clientSide.SetReadDeadline(time.Now().Add(time.Second))
	responseRaw, readErr := ReadFrameExact(clientSide)
	if readErr == nil {
		response, parseErr := ParseResponse(responseRaw)
		if parseErr == nil && response.OK {
			t.Fatalf("late success crossed deadline: %#v", response)
		}
	}
	clientSide.Close()
	<-serverDone
	if executions.Load() != 1 || backend.state(testRequestID) != durableIndeterminate {
		t.Fatalf("canceled completion = executions %d, state %d", executions.Load(), backend.state(testRequestID))
	}

	reopened := &durableStyleReplayStore{backend: backend}
	retryServer := newTestServer(newFixRoundHandler(), reopened)
	retry := parseServerResponse(t, exchangeServer(t, retryServer, NewPeer(501, nil), raw))
	if retry.OK || retry.ErrorCode != ErrorBusy {
		t.Fatalf("retry after canceled completion = %#v, want busy", retry)
	}
}

func TestCanceledMutationAbandonRetainsFailClosedReservation(t *testing.T) {
	t.Parallel()

	backend := newDurableStyleBackend()
	store := &durableStyleReplayStore{backend: backend}
	key := replayTestKey(testRequestID, "digest", OperationRotate)
	reservation, err := store.Reserve(context.Background(), key)
	if err != nil || reservation.Decision != ReplayExecute || reservation.Mutation == nil {
		t.Fatalf("Reserve() = (%#v, %v)", reservation, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := reservation.Mutation.Abandon(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("Abandon(canceled) error = %v, want context.Canceled", err)
	}
	reopened := &durableStyleReplayStore{backend: backend}
	got, err := reopened.Reserve(context.Background(), key)
	if err != nil || got.Decision != ReplayDuplicate {
		t.Fatalf("reopened Reserve() = (%#v, %v), want retained duplicate", got, err)
	}
}

func TestServerRequiresEOFBeforeAuthorizationReplayOrDispatch(t *testing.T) {
	t.Parallel()

	var authorizations atomic.Int32
	var executions atomic.Int32
	replay := NewMemoryReplayStore(MemoryReplayConfig{})
	server := newTestServer(countingFixRoundHandler(&executions), replay)
	server.Authorize = func(context.Context, Peer, Operation) bool {
		authorizations.Add(1)
		return true
	}
	server.OperationLimit = 30 * time.Millisecond
	server.IOLimit = time.Second
	raw, _ := MarshalRequest(testRequestID, OperationStatus, StatusRequest{})
	serverSide, clientSide := net.Pipe()
	done := make(chan struct{})
	go func() {
		server.ServeConn(context.Background(), serverSide, NewPeer(501, nil))
		close(done)
	}()
	if err := WriteFrame(clientSide, raw); err != nil {
		t.Fatal(err)
	}
	_ = clientSide.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := ReadFrame(clientSide); !errors.Is(err, io.EOF) {
		t.Fatalf("request without write-half-close response error = %v, want EOF", err)
	}
	clientSide.Close()
	<-done
	if executions.Load() != 0 {
		t.Fatalf("request without EOF dispatched %d handlers", executions.Load())
	}
	if authorizations.Load() != 0 {
		t.Fatalf("request without EOF performed %d authorizations", authorizations.Load())
	}
	parsed, err := ParseRequest(raw)
	if err != nil {
		t.Fatal(err)
	}
	reservation, err := replay.Reserve(context.Background(), ReplayKey{RequestID: parsed.RequestID, Digest: parsed.Digest, Operation: parsed.Operation})
	if err != nil || reservation.Decision != ReplayExecute {
		t.Fatalf("request without EOF reached replay: (%#v, %v)", reservation, err)
	}
	if err := replay.AbandonStatus(context.Background(), ReplayKey{RequestID: parsed.RequestID, Digest: parsed.Digest, Operation: parsed.Operation}); err != nil {
		t.Fatal(err)
	}
}

func TestServerRejectsDelayedSecondFrameBeforeDispatch(t *testing.T) {
	t.Parallel()

	var executions atomic.Int32
	server := newTestServer(countingFixRoundHandler(&executions), NewMemoryReplayStore(MemoryReplayConfig{}))
	server.IOLimit = time.Second
	raw, _ := MarshalRequest(testRequestID, OperationStatus, StatusRequest{})
	serverSide, clientSide := tcpConnectionPair(t)
	done := make(chan struct{})
	go func() {
		server.ServeConn(context.Background(), serverSide, NewPeer(501, nil))
		close(done)
	}()
	if err := WriteFrame(clientSide, raw); err != nil {
		t.Fatal(err)
	}
	<-time.After(20 * time.Millisecond)
	if err := WriteFrame(clientSide, raw); err == nil {
		_ = clientSide.CloseWrite()
	}
	_ = clientSide.SetReadDeadline(time.Now().Add(time.Second))
	_, _ = ReadFrame(clientSide)
	clientSide.Close()
	<-done
	if executions.Load() != 0 {
		t.Fatalf("delayed second frame dispatched %d handlers", executions.Load())
	}
}

func TestClientRejectsUnsupportedHalfCloseBeforeSendingBytes(t *testing.T) {
	t.Parallel()

	var dials atomic.Int32
	bytesReceived := make(chan int, 2)
	client := Client{
		Dial: func(context.Context) (net.Conn, error) {
			dials.Add(1)
			serverSide, clientSide := net.Pipe()
			go func() {
				buffer := make([]byte, 1)
				n, _ := serverSide.Read(buffer)
				bytesReceived <- n
				serverSide.Close()
			}()
			return clientSide, nil
		},
		Random:         bytes.NewReader(bytes.Repeat([]byte{1}, 16)),
		OperationLimit: time.Second,
		IOLimit:        time.Second,
	}
	if _, err := client.Status(context.Background()); !errors.Is(err, ErrTransport) {
		t.Fatalf("Status() error = %v, want ErrTransport", err)
	}
	if dials.Load() != 1 {
		t.Fatalf("unsupported transport dialed %d times, want 1", dials.Load())
	}
	if got := <-bytesReceived; got != 0 {
		t.Fatalf("unsupported transport received %d request bytes", got)
	}
}

func TestServerCancellationInterruptsBlockedReadWhilePeerStaysOpen(t *testing.T) {
	t.Parallel()

	server := newTestServer(countingFixRoundHandler(new(atomic.Int32)), NewMemoryReplayStore(MemoryReplayConfig{}))
	server.IOLimit = time.Second
	serverSide, clientSide := net.Pipe()
	defer clientSide.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		server.ServeConn(ctx, serverSide, NewPeer(501, nil))
		close(done)
	}()
	if _, err := clientSide.Write([]byte{0, 0}); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		clientSide.Close()
		<-done
		t.Fatal("server cancellation did not interrupt blocked frame read")
	}
}

func TestClientCancellationInterruptsBlockedResponseWhilePeerStaysOpen(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	requestEOF := make(chan struct{})
	releasePeer := make(chan struct{})
	peerDone := make(chan struct{})
	go func() {
		defer close(peerDone)
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer connection.Close()
		if _, readErr := ReadFrame(connection); readErr != nil {
			return
		}
		var byte [1]byte
		_, _ = connection.Read(byte[:])
		close(requestEOF)
		<-releasePeer
	}()
	client := Client{
		Dial: func(ctx context.Context) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", listener.Addr().String())
		},
		Random:         bytes.NewReader(bytes.Repeat([]byte{2}, 16)),
		OperationLimit: time.Second,
		IOLimit:        time.Second,
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := client.Status(ctx)
		result <- err
	}()
	<-requestEOF
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Status() error = %v, want context.Canceled", err)
		}
	case <-time.After(100 * time.Millisecond):
		close(releasePeer)
		<-result
		<-peerDone
		t.Fatal("client cancellation did not interrupt blocked response read")
	}
	close(releasePeer)
	<-peerDone
}

func TestServerSuppressesParseErrorsWithoutValidOperation(t *testing.T) {
	t.Parallel()

	server := newTestServer(countingFixRoundHandler(new(atomic.Int32)), NewMemoryReplayStore(MemoryReplayConfig{}))
	tests := []string{
		`{"version":1,"requestId":"` + testRequestID + `","params":{}}`,
		`{"version":1,"requestId":"` + testRequestID + `","operation":"status","operation":"repair","params":{}}`,
		`{"version":1,"requestId":"` + testRequestID + `","operation":"delete","params":{}}`,
	}
	for _, raw := range tests {
		serverSide, clientSide := tcpConnectionPair(t)
		done := make(chan struct{})
		go func() {
			server.ServeConn(context.Background(), serverSide, NewPeer(501, nil))
			close(done)
		}()
		if err := WriteFrame(clientSide, []byte(raw)); err != nil {
			t.Fatal(err)
		}
		if err := clientSide.CloseWrite(); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadFrame(clientSide); !errors.Is(err, io.EOF) {
			t.Fatalf("unsafe operation request received response/error %v, want EOF", err)
		}
		clientSide.Close()
		<-done
	}
}

func TestGenericJSONMarshalRequiresStrictProtocolEncoder(t *testing.T) {
	t.Parallel()

	for _, value := range []any{
		Request{Params: map[string]any{"privateKey": "secret"}},
		Response{Result: map[string]any{"stderr": "secret"}},
	} {
		raw, err := json.Marshal(value)
		if !errors.Is(err, ErrStrictMarshalRequired) {
			t.Fatalf("json.Marshal(%T) = (%q, %v), want ErrStrictMarshalRequired", value, raw, err)
		}
		if bytes.Contains(raw, []byte("secret")) {
			t.Fatalf("json.Marshal(%T) exposed secret bytes: %s", value, raw)
		}
	}
}

func tcpConnectionPair(t *testing.T) (*net.TCPConn, *net.TCPConn) {
	t.Helper()
	listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	accept := make(chan *net.TCPConn, 1)
	acceptErr := make(chan error, 1)
	go func() {
		connection, err := listener.AcceptTCP()
		if err != nil {
			acceptErr <- err
			return
		}
		accept <- connection
	}()
	client, err := net.DialTCP("tcp", nil, listener.Addr().(*net.TCPAddr))
	if err != nil {
		listener.Close()
		t.Fatal(err)
	}
	defer listener.Close()
	select {
	case server := <-accept:
		return server, client
	case err := <-acceptErr:
		client.Close()
		t.Fatal(err)
		return nil, nil
	}
}

type fixRoundHandler struct {
	status func(context.Context, Peer) (StatusResult, error)
	setup  func(context.Context, Peer, Mutation, SetupRequest) (OwnerBootstrapResult, error)
	rotate func(context.Context, Peer, Mutation, RotateRequest) (EndpointRotationResult, error)
	repair func(context.Context, Peer, Mutation) (RepairResult, error)
}

func newFixRoundHandler() *fixRoundHandler {
	return &fixRoundHandler{
		status: func(context.Context, Peer) (StatusResult, error) {
			return StatusResult{}, errors.New("unexpected status")
		},
		setup: func(context.Context, Peer, Mutation, SetupRequest) (OwnerBootstrapResult, error) {
			return OwnerBootstrapResult{}, errors.New("unexpected setup")
		},
		rotate: func(context.Context, Peer, Mutation, RotateRequest) (EndpointRotationResult, error) {
			return EndpointRotationResult{}, errors.New("unexpected rotate")
		},
		repair: func(context.Context, Peer, Mutation) (RepairResult, error) {
			return RepairResult{}, errors.New("unexpected repair")
		},
	}
}

func countingFixRoundHandler(executions *atomic.Int32) Handler {
	handler := newFixRoundHandler()
	handler.status = func(context.Context, Peer) (StatusResult, error) {
		executions.Add(1)
		return StatusResult{}, nil
	}
	handler.setup = func(context.Context, Peer, Mutation, SetupRequest) (OwnerBootstrapResult, error) {
		executions.Add(1)
		return OwnerBootstrapResult{}, nil
	}
	handler.rotate = func(context.Context, Peer, Mutation, RotateRequest) (EndpointRotationResult, error) {
		executions.Add(1)
		return EndpointRotationResult{}, nil
	}
	handler.repair = func(context.Context, Peer, Mutation) (RepairResult, error) {
		executions.Add(1)
		return RepairResult{}, nil
	}
	return handler
}

func (handler *fixRoundHandler) Status(ctx context.Context, peer Peer) (StatusResult, error) {
	return handler.status(ctx, peer)
}

func (handler *fixRoundHandler) Setup(ctx context.Context, peer Peer, mutation Mutation, request SetupRequest) (OwnerBootstrapResult, error) {
	return handler.setup(ctx, peer, mutation, request)
}

func (handler *fixRoundHandler) Rotate(ctx context.Context, peer Peer, mutation Mutation, request RotateRequest) (EndpointRotationResult, error) {
	return handler.rotate(ctx, peer, mutation, request)
}

func (handler *fixRoundHandler) Repair(ctx context.Context, peer Peer, mutation Mutation) (RepairResult, error) {
	return handler.repair(ctx, peer, mutation)
}

type durableStyleState uint8

const (
	durableReserved durableStyleState = iota + 1
	durableExecuting
	durableCompleted
	durableIndeterminate
)

type durableStyleEntry struct {
	key      ReplayKey
	state    durableStyleState
	response []byte
}

type durableStyleBackend struct {
	mu      sync.Mutex
	entries map[string]durableStyleEntry
}

func newDurableStyleBackend() *durableStyleBackend {
	return &durableStyleBackend{entries: make(map[string]durableStyleEntry)}
}

func (backend *durableStyleBackend) state(requestID string) durableStyleState {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return backend.entries[requestID].state
}

type durableStyleReplayStore struct {
	backend                       *durableStyleBackend
	failCompletion                bool
	waitForCompletionCancellation bool
	commitEntered                 chan struct{}
}

func (store *durableStyleReplayStore) Reserve(ctx context.Context, key ReplayKey) (ReplayReservation, error) {
	if err := ctx.Err(); err != nil {
		return ReplayReservation{}, err
	}
	store.backend.mu.Lock()
	defer store.backend.mu.Unlock()
	if entry, ok := store.backend.entries[key.RequestID]; ok {
		if entry.key.Digest != key.Digest || entry.key.Operation != key.Operation {
			return ReplayReservation{Decision: ReplayDuplicate}, nil
		}
		switch entry.state {
		case durableCompleted:
			return ReplayReservation{Decision: ReplayCached, Response: append([]byte(nil), entry.response...)}, nil
		case durableIndeterminate:
			return ReplayReservation{Decision: ReplayBusy}, nil
		default:
			return ReplayReservation{Decision: ReplayDuplicate}, nil
		}
	}
	store.backend.entries[key.RequestID] = durableStyleEntry{key: key, state: durableReserved}
	return ReplayReservation{
		Decision: ReplayExecute,
		Mutation: &durableStyleMutationReservation{store: store, key: key},
	}, nil
}

func (store *durableStyleReplayStore) CompleteStatus(context.Context, ReplayKey, []byte) error {
	return ErrReplayState
}

func (store *durableStyleReplayStore) AbandonStatus(context.Context, ReplayKey) error {
	return ErrReplayState
}

type durableStyleMutationReservation struct {
	store *durableStyleReplayStore
	key   ReplayKey
}

func (reservation *durableStyleMutationReservation) Key() ReplayKey { return reservation.key }

func (reservation *durableStyleMutationReservation) Execute(ctx context.Context, execution MutationExecution) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	backend := reservation.store.backend
	backend.mu.Lock()
	entry := backend.entries[reservation.key.RequestID]
	if entry.state != durableReserved {
		backend.mu.Unlock()
		return nil, ErrReplayState
	}
	entry.state = durableExecuting
	backend.entries[reservation.key.RequestID] = entry
	backend.mu.Unlock()

	response, err := execution(ctx, durableStyleTransaction{key: reservation.key})
	if reservation.store.commitEntered != nil {
		close(reservation.store.commitEntered)
	}
	if reservation.store.waitForCompletionCancellation {
		<-ctx.Done()
		err = ctx.Err()
	}
	if reservation.store.failCompletion && err == nil {
		err = ErrReplayState
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	entry = backend.entries[reservation.key.RequestID]
	if err != nil || ctx.Err() != nil {
		entry.state = durableIndeterminate
		backend.entries[reservation.key.RequestID] = entry
		if err != nil {
			return nil, err
		}
		return nil, ctx.Err()
	}
	entry.state = durableCompleted
	entry.response = append([]byte(nil), response...)
	backend.entries[reservation.key.RequestID] = entry
	return append([]byte(nil), response...), nil
}

func (reservation *durableStyleMutationReservation) Abandon(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	backend := reservation.store.backend
	backend.mu.Lock()
	defer backend.mu.Unlock()
	entry := backend.entries[reservation.key.RequestID]
	if entry.state != durableReserved {
		return ErrReplayState
	}
	delete(backend.entries, reservation.key.RequestID)
	return nil
}

type durableStyleTransaction struct{ key ReplayKey }

func (transaction durableStyleTransaction) ReplayKey() ReplayKey { return transaction.key }
