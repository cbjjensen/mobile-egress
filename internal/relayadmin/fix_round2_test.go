package relayadmin

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestStrictMarshalRejectsOutboundBodiesOverMaximumFrameSize(t *testing.T) {
	t.Parallel()

	oversized := strings.Repeat("x", MaximumFrameSize)
	if raw, err := MarshalRequest("00000000000000000000000000000071", OperationSetup, SetupRequest{
		PublicName: "name", PublicURL: "url", OwnerCSRPEM: oversized,
	}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("MarshalRequest(oversized) = (%d bytes, %v), want ErrInvalidRequest", len(raw), err)
	}
	if raw, err := MarshalSuccessResponse("00000000000000000000000000000072", OperationSetup, OwnerBootstrapResult{
		CertificatePEM: oversized, CACertificatePEM: "ca", Serial: "1", Role: "owner",
	}); !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("MarshalSuccessResponse(oversized) = (%d bytes, %v), want ErrInvalidResponse", len(raw), err)
	}
}

func TestClientRejectsOversizedTypedRequestBeforeDialOrRetry(t *testing.T) {
	t.Parallel()

	var dials atomic.Int32
	client := Client{
		Dial: func(context.Context) (net.Conn, error) {
			dials.Add(1)
			return nil, errors.New("dial must not be reached")
		},
		Random:         bytes.NewReader(bytes.Repeat([]byte{7}, 16)),
		OperationLimit: time.Second,
		IOLimit:        time.Second,
	}
	_, err := client.Setup(context.Background(), SetupRequest{
		PublicName: "name", PublicURL: "url", OwnerCSRPEM: strings.Repeat("x", MaximumFrameSize),
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Setup(oversized) error = %v, want ErrInvalidRequest", err)
	}
	if dials.Load() != 0 {
		t.Fatalf("Setup(oversized) dialed %d times, want zero", dials.Load())
	}
}

func TestOversizedMutationResponseBecomesIndeterminateBeforeReplayCommit(t *testing.T) {
	t.Parallel()

	backend := newDurableStyleBackend()
	executionError := make(chan error, 1)
	store := &durableStyleReplayStore{backend: backend, executionError: executionError}
	handler := newFixRoundHandler()
	handler.setup = func(context.Context, Peer, Mutation, SetupRequest) (OwnerBootstrapResult, error) {
		return OwnerBootstrapResult{
			CertificatePEM:   strings.Repeat("x", MaximumFrameSize),
			CACertificatePEM: "ca",
			Serial:           "1",
			Role:             "owner",
		}, nil
	}
	server := newTestServer(handler, store)
	raw, err := MarshalRequest("00000000000000000000000000000073", OperationSetup, SetupRequest{PublicName: "name", PublicURL: "url", OwnerCSRPEM: "csr"})
	if err != nil {
		t.Fatal(err)
	}
	request, err := ParseRequest(raw)
	if err != nil {
		t.Fatal(err)
	}

	responseRaw, responseErr, _ := exchangeServerOutcome(t, server, raw, nil)
	if responseErr != nil {
		t.Fatalf("oversized mutation response read error = %v, want small operation_failed frame", responseErr)
	}
	response := parseServerResponse(t, responseRaw)
	if response.OK || response.ErrorCode != ErrorOperationFailed {
		t.Fatalf("oversized mutation response = %#v, want operation_failed", response)
	}
	if callbackErr := <-executionError; !errors.Is(callbackErr, ErrMutationIndeterminate) {
		t.Fatalf("MutationExecution error = %v, want ErrMutationIndeterminate", callbackErr)
	}
	if state := backend.state(request.RequestID); state != durableIndeterminate {
		t.Fatalf("oversized mutation transaction disposition = %d, want indeterminate", state)
	}
	reopened := &durableStyleReplayStore{backend: backend}
	reservation, err := reopened.Reserve(context.Background(), ReplayKey{RequestID: request.RequestID, Digest: request.Digest, Operation: request.Operation})
	if err != nil || reservation.Decision != ReplayBusy {
		t.Fatalf("reopened Reserve() = (%#v, %v), want busy", reservation, err)
	}
}

func TestOversizedStatusResponseFallsBackBeforeReplayCompletion(t *testing.T) {
	t.Parallel()

	var executions atomic.Int32
	handler := newFixRoundHandler()
	handler.status = func(context.Context, Peer) (StatusResult, error) {
		executions.Add(1)
		return StatusResult{HelperVersion: strings.Repeat("x", MaximumFrameSize)}, nil
	}
	server := newTestServer(handler, NewMemoryReplayStore(MemoryReplayConfig{}))
	raw, err := MarshalRequest("00000000000000000000000000000074", OperationStatus, StatusRequest{})
	if err != nil {
		t.Fatal(err)
	}

	first := exchangeServer(t, server, NewPeer(501, nil), raw)
	second := exchangeServer(t, server, NewPeer(501, nil), raw)
	response := parseServerResponse(t, first)
	if response.OK || response.ErrorCode != ErrorOperationFailed {
		t.Fatalf("oversized status response = %#v, want operation_failed", response)
	}
	if !bytes.Equal(first, second) || executions.Load() != 1 {
		t.Fatalf("status fallback replay = equal %t, executions %d", bytes.Equal(first, second), executions.Load())
	}
}

func TestMemoryReplayRejectsOversizedResponseBeforeCompletion(t *testing.T) {
	t.Parallel()

	store := NewMemoryReplayStore(MemoryReplayConfig{})
	oversized := bytes.Repeat([]byte("x"), MaximumFrameSize+1)
	statusKey := replayTestKey("00000000000000000000000000000075", "status", OperationStatus)
	status, err := store.Reserve(context.Background(), statusKey)
	if err != nil || status.Decision != ReplayExecute {
		t.Fatalf("Reserve(status) = (%#v, %v)", status, err)
	}
	if err := store.CompleteStatus(context.Background(), statusKey, oversized); !errors.Is(err, ErrReplayState) {
		t.Fatalf("CompleteStatus(oversized) error = %v, want ErrReplayState", err)
	}
	if err := store.AbandonStatus(context.Background(), statusKey); err != nil {
		t.Fatal(err)
	}

	mutationKey := replayTestKey("00000000000000000000000000000076", "mutation", OperationRepair)
	mutation, err := store.Reserve(context.Background(), mutationKey)
	if err != nil || mutation.Decision != ReplayExecute || mutation.Mutation == nil {
		t.Fatalf("Reserve(mutation) = (%#v, %v)", mutation, err)
	}
	if _, err := mutation.Mutation.Execute(context.Background(), func(context.Context, MutationTransaction) ([]byte, error) {
		return oversized, nil
	}); !errors.Is(err, ErrReplayState) {
		t.Fatalf("Mutation.Execute(oversized) error = %v, want ErrReplayState", err)
	}
	retry, err := store.Reserve(context.Background(), mutationKey)
	if err != nil || retry.Decision != ReplayBusy {
		t.Fatalf("Reserve(oversized mutation retry) = (%#v, %v), want busy", retry, err)
	}
}

func TestMutationTransactionCommitsOnlyExplicitValidPublicErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		requestID        string
		result           OwnerBootstrapResult
		handlerErr       error
		wantCode         ErrorCode
		wantState        durableStyleState
		wantReplay       ReplayDecision
		wantExecutionErr bool
	}{
		{
			name:             "raw handler error",
			requestID:        "00000000000000000000000000000061",
			handlerErr:       errors.New("secret /Library/private stderr"),
			wantCode:         ErrorOperationFailed,
			wantState:        durableIndeterminate,
			wantReplay:       ReplayBusy,
			wantExecutionErr: true,
		},
		{
			name:       "valid public error",
			requestID:  "00000000000000000000000000000062",
			handlerErr: &PublicError{Code: ErrorAlreadyInitialized},
			wantCode:   ErrorAlreadyInitialized,
			wantState:  durableCompleted,
			wantReplay: ReplayCached,
		},
		{
			name:             "invalid public error",
			requestID:        "00000000000000000000000000000063",
			handlerErr:       &PublicError{Code: ErrorCode("sqlite_secret")},
			wantCode:         ErrorOperationFailed,
			wantState:        durableIndeterminate,
			wantReplay:       ReplayBusy,
			wantExecutionErr: true,
		},
		{
			name:      "invalid success result",
			requestID: "00000000000000000000000000000064",
			result: OwnerBootstrapResult{
				CertificatePEM:   string([]byte{0xff}),
				CACertificatePEM: "ca",
				Serial:           "1",
				Role:             "owner",
			},
			wantCode:         ErrorOperationFailed,
			wantState:        durableIndeterminate,
			wantReplay:       ReplayBusy,
			wantExecutionErr: true,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			backend := newDurableStyleBackend()
			executionError := make(chan error, 1)
			store := &durableStyleReplayStore{backend: backend, executionError: executionError}
			handler := newFixRoundHandler()
			handler.setup = func(context.Context, Peer, Mutation, SetupRequest) (OwnerBootstrapResult, error) {
				return test.result, test.handlerErr
			}
			server := newTestServer(handler, store)
			raw, err := MarshalRequest(test.requestID, OperationSetup, SetupRequest{PublicName: "name", PublicURL: "url", OwnerCSRPEM: "csr"})
			if err != nil {
				t.Fatal(err)
			}
			request, err := ParseRequest(raw)
			if err != nil {
				t.Fatal(err)
			}

			responseRaw := exchangeServer(t, server, NewPeer(501, nil), raw)
			response := parseServerResponse(t, responseRaw)
			if response.OK || response.ErrorCode != test.wantCode {
				t.Fatalf("mutation response = %#v, want %s", response, test.wantCode)
			}
			if bytes.Contains(responseRaw, []byte("secret")) || bytes.Contains(responseRaw, []byte("Library")) || bytes.Contains(responseRaw, []byte("stderr")) || bytes.Contains(responseRaw, []byte("sqlite")) {
				t.Fatalf("mutation response exposed internal text: %s", responseRaw)
			}
			callbackErr := <-executionError
			if test.wantExecutionErr {
				if !errors.Is(callbackErr, ErrMutationIndeterminate) {
					t.Fatalf("MutationExecution error = %v, want ErrMutationIndeterminate", callbackErr)
				}
				if callbackErr.Error() != ErrMutationIndeterminate.Error() {
					t.Fatalf("MutationExecution error text = %q, want fixed sentinel", callbackErr)
				}
			} else if callbackErr != nil {
				t.Fatalf("determinate public error crossed MutationExecution as %v", callbackErr)
			}
			if state := backend.state(test.requestID); state != test.wantState {
				t.Fatalf("transaction disposition = %d, want %d", state, test.wantState)
			}

			reopened := &durableStyleReplayStore{backend: backend}
			key := ReplayKey{RequestID: request.RequestID, Digest: request.Digest, Operation: request.Operation}
			reservation, err := reopened.Reserve(context.Background(), key)
			if err != nil || reservation.Decision != test.wantReplay {
				t.Fatalf("reopened Reserve() = (%#v, %v), want %d", reservation, err, test.wantReplay)
			}
			if test.wantReplay == ReplayCached && !bytes.Equal(reservation.Response, responseRaw) {
				t.Fatalf("cached determinate response changed: %s versus %s", reservation.Response, responseRaw)
			}
		})
	}
}

func TestServeOutcomeSignalsRestartingRepairOnlyAfterFrameFlushAndClose(t *testing.T) {
	t.Parallel()

	handler := newFixRoundHandler()
	handler.repair = func(context.Context, Peer, Mutation) (RepairResult, error) {
		return RepairResult{Ready: true, Restarting: true}, nil
	}
	server := newTestServer(handler, NewMemoryReplayStore(MemoryReplayConfig{}))
	raw, err := MarshalRequest("00000000000000000000000000000051", OperationRepair, RepairRequest{})
	if err != nil {
		t.Fatal(err)
	}

	responseRaw, responseErr, observed := exchangeServerOutcome(t, server, raw, nil)
	if responseErr != nil {
		t.Fatalf("ReadFrameExact() returned an error: %v", responseErr)
	}
	response := parseServerResponse(t, responseRaw)
	if !response.OK || response.Result != (RepairResult{Ready: true, Restarting: true}) {
		t.Fatalf("repair response = %#v", response)
	}
	if !observed.outcome.RepairRestartReady {
		t.Fatalf("ServeConn() outcome = %#v, want repair restart ready", observed.outcome)
	}
	if !observed.closed {
		t.Fatal("ServeConn() exposed the restart outcome before closing the connection")
	}
}

func TestServeOutcomeDoesNotSignalSuccessfulNonRestartingRepair(t *testing.T) {
	t.Parallel()

	handler := newFixRoundHandler()
	handler.repair = func(context.Context, Peer, Mutation) (RepairResult, error) {
		return RepairResult{Ready: true, Restarting: false}, nil
	}
	server := newTestServer(handler, NewMemoryReplayStore(MemoryReplayConfig{}))
	raw, err := MarshalRequest("00000000000000000000000000000052", OperationRepair, RepairRequest{})
	if err != nil {
		t.Fatal(err)
	}

	_, responseErr, observed := exchangeServerOutcome(t, server, raw, nil)
	if responseErr != nil {
		t.Fatalf("ReadFrameExact() returned an error: %v", responseErr)
	}
	if observed.outcome.RepairRestartReady {
		t.Fatalf("ServeConn() outcome = %#v for non-restarting repair", observed.outcome)
	}
}

func TestServeOutcomeSuppressesPartialWriteThenSignalsCachedRepairRetry(t *testing.T) {
	t.Parallel()

	var executions atomic.Int32
	handler := newFixRoundHandler()
	handler.repair = func(context.Context, Peer, Mutation) (RepairResult, error) {
		executions.Add(1)
		return RepairResult{Ready: true, Restarting: true}, nil
	}
	server := newTestServer(handler, NewMemoryReplayStore(MemoryReplayConfig{}))
	raw, err := MarshalRequest("00000000000000000000000000000053", OperationRepair, RepairRequest{})
	if err != nil {
		t.Fatal(err)
	}

	_, firstReadErr, first := exchangeServerOutcome(t, server, raw, func(connection net.Conn) net.Conn {
		return &partialResponseWriteConn{Conn: connection}
	})
	if firstReadErr == nil {
		t.Fatal("partial response write unexpectedly produced a complete frame")
	}
	if first.outcome.RepairRestartReady {
		t.Fatal("partial response write produced a restart-ready outcome")
	}
	if !first.closed {
		t.Fatal("partial response connection was not closed before outcome observation")
	}

	secondRaw, secondReadErr, second := exchangeServerOutcome(t, server, raw, nil)
	if secondReadErr != nil {
		t.Fatalf("cached ReadFrameExact() returned an error: %v", secondReadErr)
	}
	response := parseServerResponse(t, secondRaw)
	if !response.OK || response.Result != (RepairResult{Ready: true, Restarting: true}) {
		t.Fatalf("cached repair response = %#v", response)
	}
	if !second.outcome.RepairRestartReady || !second.closed {
		t.Fatalf("cached ServeConn() observation = %#v", second)
	}
	if executions.Load() != 1 {
		t.Fatalf("repair handler executions = %d, want one cached retry", executions.Load())
	}
}

func TestCanceledStatusCleanupFreesBoundedInFlightCapacity(t *testing.T) {
	t.Parallel()

	store := NewMemoryReplayStore(MemoryReplayConfig{
		StatusCapacity:   1,
		StatusTTL:        time.Minute,
		MutationCapacity: 1,
		InFlightCapacity: 1,
	})
	handler := newFixRoundHandler()
	handler.status = func(ctx context.Context, _ Peer) (StatusResult, error) {
		<-ctx.Done()
		return StatusResult{}, ctx.Err()
	}
	server := newTestServer(handler, store)
	server.OperationLimit = 10 * time.Millisecond
	server.IOLimit = time.Second

	for _, requestID := range []string{
		"00000000000000000000000000000021",
		"00000000000000000000000000000022",
	} {
		raw, err := MarshalRequest(requestID, OperationStatus, StatusRequest{})
		if err != nil {
			t.Fatal(err)
		}
		serveRequestWithoutResponse(t, server, context.Background(), raw)
	}

	key := replayTestKey("00000000000000000000000000000023", "available", OperationStatus)
	reservation, err := store.Reserve(context.Background(), key)
	if err != nil || reservation.Decision != ReplayExecute {
		t.Fatalf("Reserve(after canceled status calls) = (%#v, %v), want execute", reservation, err)
	}
	if err := store.AbandonStatus(context.Background(), key); err != nil {
		t.Fatal(err)
	}
}

func TestCanceledPreExecutionMutationCleanupFreesBoundedSlots(t *testing.T) {
	t.Parallel()

	inner := NewMemoryReplayStore(MemoryReplayConfig{
		StatusCapacity:   1,
		StatusTTL:        time.Minute,
		MutationCapacity: 1,
		InFlightCapacity: 1,
	})
	gated := &preExecutionGateStore{
		ReplayStore: inner,
		entered:     make(chan struct{}),
		release:     make(chan struct{}),
		done:        make(chan struct{}),
	}
	var executions atomic.Int32
	server := newTestServer(countingFixRoundHandler(&executions), gated)
	server.OperationLimit = time.Second
	server.IOLimit = time.Second
	raw, err := MarshalRequest("00000000000000000000000000000031", OperationSetup, SetupRequest{PublicName: "name", PublicURL: "url", OwnerCSRPEM: "csr"})
	if err != nil {
		t.Fatal(err)
	}

	serverSide, clientSide := tcpConnectionPair(t)
	parent, cancel := context.WithCancel(context.Background())
	serverDone := make(chan struct{})
	go func() {
		server.ServeConn(parent, serverSide, NewPeer(501, nil))
		close(serverDone)
	}()
	if err := WriteFrame(clientSide, raw); err != nil {
		t.Fatal(err)
	}
	if err := clientSide.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	<-gated.entered
	cancel()
	<-serverDone
	close(gated.release)
	<-gated.done
	clientSide.Close()

	if executions.Load() != 0 {
		t.Fatalf("pre-execution cancellation dispatched %d mutations", executions.Load())
	}
	key := replayTestKey("00000000000000000000000000000032", "available", OperationRotate)
	reservation, err := inner.Reserve(context.Background(), key)
	if err != nil || reservation.Decision != ReplayExecute || reservation.Mutation == nil {
		t.Fatalf("Reserve(after canceled pre-execution mutation) = (%#v, %v), want execute", reservation, err)
	}
	if err := reservation.Mutation.Abandon(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestCanceledPostStartMutationRetainsFailClosedSlot(t *testing.T) {
	t.Parallel()

	store := NewMemoryReplayStore(MemoryReplayConfig{
		StatusCapacity:   1,
		StatusTTL:        time.Minute,
		MutationCapacity: 1,
		InFlightCapacity: 1,
	})
	started := make(chan struct{})
	handlerReturned := make(chan struct{})
	var executions atomic.Int32
	handler := newFixRoundHandler()
	handler.repair = func(ctx context.Context, _ Peer, _ Mutation) (RepairResult, error) {
		executions.Add(1)
		close(started)
		<-ctx.Done()
		close(handlerReturned)
		return RepairResult{}, ctx.Err()
	}
	server := newTestServer(handler, store)
	server.OperationLimit = time.Second
	server.IOLimit = time.Second
	raw, err := MarshalRequest("00000000000000000000000000000041", OperationRepair, RepairRequest{})
	if err != nil {
		t.Fatal(err)
	}

	serverSide, clientSide := tcpConnectionPair(t)
	parent, cancel := context.WithCancel(context.Background())
	serverDone := make(chan struct{})
	go func() {
		server.ServeConn(parent, serverSide, NewPeer(501, nil))
		close(serverDone)
	}()
	if err := WriteFrame(clientSide, raw); err != nil {
		t.Fatal(err)
	}
	if err := clientSide.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	<-started
	cancel()
	<-handlerReturned
	<-serverDone
	clientSide.Close()

	if executions.Load() != 1 {
		t.Fatalf("post-start handler executions = %d, want 1", executions.Load())
	}
	key := replayTestKey("00000000000000000000000000000042", "blocked", OperationSetup)
	reservation, err := store.Reserve(context.Background(), key)
	if err != nil || reservation.Decision != ReplayBusy {
		t.Fatalf("Reserve(after canceled post-start mutation) = (%#v, %v), want busy", reservation, err)
	}
}

func TestCanceledDuplicateStatusDoesNotAbandonAnotherReservation(t *testing.T) {
	t.Parallel()

	inner := NewMemoryReplayStore(MemoryReplayConfig{
		StatusCapacity:   1,
		StatusTTL:        time.Minute,
		MutationCapacity: 1,
		InFlightCapacity: 1,
	})
	raw, err := MarshalRequest("00000000000000000000000000000043", OperationStatus, StatusRequest{})
	if err != nil {
		t.Fatal(err)
	}
	request, err := ParseRequest(raw)
	if err != nil {
		t.Fatal(err)
	}
	key := ReplayKey{RequestID: request.RequestID, Digest: request.Digest, Operation: request.Operation}
	first, err := inner.Reserve(context.Background(), key)
	if err != nil || first.Decision != ReplayExecute {
		t.Fatalf("Reserve(first) = (%#v, %v)", first, err)
	}

	parent, cancel := context.WithCancel(context.Background())
	store := &cancelAfterReserveStore{ReplayStore: inner, cancel: cancel}
	server := newTestServer(countingFixRoundHandler(new(atomic.Int32)), store)
	serveRequestWithoutResponse(t, server, parent, raw)

	same, err := inner.Reserve(context.Background(), key)
	if err != nil || same.Decision != ReplayDuplicate {
		t.Fatalf("Reserve(original after canceled duplicate) = (%#v, %v), want duplicate", same, err)
	}
	otherKey := replayTestKey("00000000000000000000000000000044", "blocked", OperationStatus)
	other, err := inner.Reserve(context.Background(), otherKey)
	if err != nil || other.Decision != ReplayBusy {
		t.Fatalf("Reserve(other while original in flight) = (%#v, %v), want busy", other, err)
	}
	if err := inner.AbandonStatus(context.Background(), key); err != nil {
		t.Fatal(err)
	}
}

func serveRequestWithoutResponse(t *testing.T, server *Server, parent context.Context, raw []byte) {
	t.Helper()
	serverSide, clientSide := tcpConnectionPair(t)
	done := make(chan struct{})
	go func() {
		server.ServeConn(parent, serverSide, NewPeer(501, nil))
		close(done)
	}()
	if err := WriteFrame(clientSide, raw); err != nil {
		t.Fatal(err)
	}
	if err := clientSide.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	_ = clientSide.SetReadDeadline(time.Now().Add(time.Second))
	if response, err := ReadFrame(clientSide); err == nil {
		t.Fatalf("canceled request unexpectedly received response %s", response)
	}
	clientSide.Close()
	<-done
}

type preExecutionGateStore struct {
	ReplayStore
	entered chan struct{}
	release chan struct{}
	done    chan struct{}
}

type cancelAfterReserveStore struct {
	ReplayStore
	cancel context.CancelFunc
}

func (store *cancelAfterReserveStore) Reserve(ctx context.Context, key ReplayKey) (ReplayReservation, error) {
	reservation, err := store.ReplayStore.Reserve(ctx, key)
	store.cancel()
	return reservation, err
}

func (store *preExecutionGateStore) Reserve(ctx context.Context, key ReplayKey) (ReplayReservation, error) {
	reservation, err := store.ReplayStore.Reserve(ctx, key)
	if err == nil && reservation.Mutation != nil {
		reservation.Mutation = &preExecutionGateReservation{inner: reservation.Mutation, store: store}
	}
	return reservation, err
}

type preExecutionGateReservation struct {
	inner MutationReservation
	store *preExecutionGateStore
}

func (reservation *preExecutionGateReservation) Key() ReplayKey { return reservation.inner.Key() }

func (reservation *preExecutionGateReservation) Execute(ctx context.Context, execution MutationExecution) ([]byte, error) {
	close(reservation.store.entered)
	<-reservation.store.release
	defer close(reservation.store.done)
	return reservation.inner.Execute(ctx, execution)
}

func (reservation *preExecutionGateReservation) Abandon(ctx context.Context) error {
	return reservation.inner.Abandon(ctx)
}

type observedServeOutcome struct {
	outcome ServeOutcome
	closed  bool
}

func exchangeServerOutcome(t *testing.T, server *Server, raw []byte, wrap func(net.Conn) net.Conn) ([]byte, error, observedServeOutcome) {
	t.Helper()
	serverSide, clientSide := tcpConnectionPair(t)
	closeRecorder := &closeRecordingConn{Conn: serverSide}
	var connection net.Conn = closeRecorder
	if wrap != nil {
		connection = wrap(closeRecorder)
	}
	observed := make(chan observedServeOutcome, 1)
	go func() {
		outcome := server.ServeConn(context.Background(), connection, NewPeer(501, nil))
		observed <- observedServeOutcome{outcome: outcome, closed: closeRecorder.closed.Load()}
	}()
	if err := WriteFrame(clientSide, raw); err != nil {
		clientSide.Close()
		t.Fatal(err)
	}
	if err := clientSide.CloseWrite(); err != nil {
		clientSide.Close()
		t.Fatal(err)
	}
	response, responseErr := ReadFrameExact(clientSide)
	clientSide.Close()
	return response, responseErr, <-observed
}

type closeRecordingConn struct {
	net.Conn
	closed atomic.Bool
}

func (connection *closeRecordingConn) Close() error {
	connection.closed.Store(true)
	return connection.Conn.Close()
}

type partialResponseWriteConn struct {
	net.Conn
	writes atomic.Int32
}

func (connection *partialResponseWriteConn) Write(buffer []byte) (int, error) {
	if connection.writes.Add(1) == 1 {
		return connection.Conn.Write(buffer)
	}
	if len(buffer) == 0 {
		return 0, io.ErrUnexpectedEOF
	}
	n, _ := connection.Conn.Write(buffer[:1])
	return n, io.ErrUnexpectedEOF
}
