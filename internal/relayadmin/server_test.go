package relayadmin

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestServerDispatchesEachTypedOperationAndReturnsOnlyTypedRedactedResults(t *testing.T) {
	t.Parallel()

	handler := &behaviorHandler{
		status: func(context.Context, Peer) (StatusResult, error) {
			return StatusResult{ProtocolVersion: Version, HelperVersion: "dev", Initialized: true, RelayRunning: true}, nil
		},
		setup: func(_ context.Context, peer Peer, _ Mutation, params SetupRequest) (OwnerBootstrapResult, error) {
			if peer.UID() != 501 || params.PublicName != "relay.example" || params.OwnerCSRPEM != "public-csr" {
				return OwnerBootstrapResult{}, errors.New("unexpected typed setup input")
			}
			return OwnerBootstrapResult{CertificatePEM: "owner", CACertificatePEM: "ca", Serial: "1", Role: "owner"}, nil
		},
		rotate: func(_ context.Context, _ Peer, _ Mutation, params RotateRequest) (EndpointRotationResult, error) {
			return EndpointRotationResult{PublicURL: params.PublicURL, Serial: "2"}, nil
		},
		repair: func(context.Context, Peer, Mutation) (RepairResult, error) {
			return RepairResult{Ready: true, Restarting: false}, nil
		},
	}
	server := newTestServer(handler, NewMemoryReplayStore(MemoryReplayConfig{}))
	peer := NewPeer(501, []uint32{20, 80})

	tests := []struct {
		id        string
		operation Operation
		params    any
		want      any
	}{
		{"00000000000000000000000000000001", OperationStatus, StatusRequest{}, StatusResult{ProtocolVersion: Version, HelperVersion: "dev", Initialized: true, RelayRunning: true}},
		{"00000000000000000000000000000002", OperationSetup, SetupRequest{PublicName: "relay.example", PublicURL: "https://relay.example", OwnerCSRPEM: "public-csr"}, OwnerBootstrapResult{CertificatePEM: "owner", CACertificatePEM: "ca", Serial: "1", Role: "owner"}},
		{"00000000000000000000000000000003", OperationRotate, RotateRequest{PublicName: "relay-2.example", PublicURL: "https://relay-2.example"}, EndpointRotationResult{PublicURL: "https://relay-2.example", Serial: "2"}},
		{"00000000000000000000000000000004", OperationRepair, RepairRequest{}, RepairResult{Ready: true, Restarting: false}},
	}
	for _, test := range tests {
		raw, err := MarshalRequest(test.id, test.operation, test.params)
		if err != nil {
			t.Fatalf("MarshalRequest(%s) returned an error: %v", test.operation, err)
		}
		responseRaw := exchangeServer(t, server, peer, raw)
		assertNoSecretSchemaNames(t, responseRaw)
		response, err := ParseResponse(responseRaw)
		if err != nil {
			t.Fatalf("ParseResponse(%s) returned an error: %v", test.operation, err)
		}
		if !response.OK || response.RequestID != test.id || response.Operation != test.operation {
			t.Fatalf("response for %s = %#v", test.operation, response)
		}
		if !deepEqual(response.Result, test.want) {
			t.Fatalf("response result for %s = %#v, want %#v", test.operation, response.Result, test.want)
		}
	}
}

func TestServerRejectsMalformedUnknownOversizedAndUnauthorizedWithoutDispatch(t *testing.T) {
	t.Parallel()

	var executions atomic.Int32
	handler := countingHandler(&executions)
	replay := NewMemoryReplayStore(MemoryReplayConfig{
		StatusCapacity:   1,
		StatusTTL:        time.Minute,
		MutationCapacity: 1,
		InFlightCapacity: 2,
	})
	reserveAndComplete(t, replay, replayTestKey("full", "full", OperationSetup), []byte("full"))
	server := newTestServer(handler, replay)
	server.Authorize = func(context.Context, Peer, Operation) bool { return false }

	unauthorizedRaw, err := MarshalRequest(testRequestID, OperationSetup, SetupRequest{PublicName: "name", PublicURL: "url", OwnerCSRPEM: "csr"})
	if err != nil {
		t.Fatal(err)
	}
	unauthorized := parseServerResponse(t, exchangeServer(t, server, NewPeer(502, nil), unauthorizedRaw))
	if unauthorized.OK || unauthorized.ErrorCode != ErrorUnauthorized {
		t.Fatalf("unauthorized response = %#v, want unauthorized (not replay busy)", unauthorized)
	}

	invalidWithID := []byte(`{"version":1,"requestId":"` + testRequestID + `","operation":"status","params":{},"unexpected":true}`)
	invalid := parseServerResponse(t, exchangeServer(t, server, NewPeer(502, nil), invalidWithID))
	if invalid.OK || invalid.ErrorCode != ErrorInvalidRequest {
		t.Fatalf("invalid response = %#v", invalid)
	}

	serverSide, clientSide := tcpConnectionPair(t)
	done := make(chan struct{})
	go func() {
		server.ServeConn(context.Background(), serverSide, NewPeer(502, nil))
		close(done)
	}()
	if err := WriteFrame(clientSide, []byte(`{"version":1}`)); err != nil {
		t.Fatalf("WriteFrame(malformed) returned an error: %v", err)
	}
	if err := clientSide.CloseWrite(); err != nil {
		t.Fatalf("CloseWrite(malformed) returned an error: %v", err)
	}
	if _, err := ReadFrame(clientSide); !errors.Is(err, io.EOF) {
		t.Fatalf("malformed request received response/error %v, want EOF", err)
	}
	clientSide.Close()
	<-done

	pipeServer, pipeClient := net.Pipe()
	done = make(chan struct{})
	go func() {
		server.ServeConn(context.Background(), pipeServer, NewPeer(502, nil))
		close(done)
	}()
	if _, err := pipeClient.Write(framePrefix(MaximumFrameSize + 1)); err != nil {
		t.Fatalf("write oversized prefix: %v", err)
	}
	if _, err := ReadFrame(pipeClient); !errors.Is(err, io.EOF) {
		t.Fatalf("oversized request received response/error %v, want EOF", err)
	}
	pipeClient.Close()
	<-done

	if got := executions.Load(); got != 0 {
		t.Fatalf("handler executed %d times for rejected requests", got)
	}
}

func TestServerRejectsTrailingSecondFrameWithoutDispatch(t *testing.T) {
	t.Parallel()

	var executions atomic.Int32
	server := newTestServer(countingHandler(&executions), NewMemoryReplayStore(MemoryReplayConfig{}))
	raw, err := MarshalRequest(testRequestID, OperationStatus, StatusRequest{})
	if err != nil {
		t.Fatal(err)
	}
	first := append(framePrefix(len(raw)), raw...)
	second := append(framePrefix(len(raw)), raw...)

	serverSide, clientSide := net.Pipe()
	done := make(chan struct{})
	go func() {
		server.ServeConn(context.Background(), serverSide, NewPeer(501, nil))
		close(done)
	}()
	writeDone := make(chan error, 1)
	go func() {
		_, err := clientSide.Write(append(first, second...))
		writeDone <- err
	}()
	if _, err := ReadFrame(clientSide); !errors.Is(err, io.EOF) {
		t.Fatalf("trailing frame received response/error %v, want EOF", err)
	}
	clientSide.Close()
	<-writeDone
	<-done
	if executions.Load() != 0 {
		t.Fatal("handler executed a request with trailing frame data")
	}
}

func TestServerRejectsInFlightDuplicateAndDoesNotExecuteTwice(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	release := make(chan struct{})
	var executions atomic.Int32
	handler := &behaviorHandler{setup: func(context.Context, Peer, Mutation, SetupRequest) (OwnerBootstrapResult, error) {
		if executions.Add(1) == 1 {
			close(started)
		}
		<-release
		return OwnerBootstrapResult{CertificatePEM: "owner", CACertificatePEM: "ca", Serial: "1", Role: "owner"}, nil
	}}
	server := newTestServer(handler, NewMemoryReplayStore(MemoryReplayConfig{}))
	raw, err := MarshalRequest(testRequestID, OperationSetup, SetupRequest{PublicName: "name", PublicURL: "url", OwnerCSRPEM: "csr"})
	if err != nil {
		t.Fatal(err)
	}

	serverSide, firstClient := tcpConnectionPair(t)
	firstDone := make(chan struct{})
	go func() {
		server.ServeConn(context.Background(), serverSide, NewPeer(501, nil))
		close(firstDone)
	}()
	if err := WriteFrame(firstClient, raw); err != nil {
		t.Fatalf("WriteFrame(first) returned an error: %v", err)
	}
	if err := firstClient.CloseWrite(); err != nil {
		t.Fatalf("CloseWrite(first) returned an error: %v", err)
	}
	<-started

	duplicate := parseServerResponse(t, exchangeServer(t, server, NewPeer(501, nil), raw))
	if duplicate.OK || duplicate.ErrorCode != ErrorDuplicateRequest {
		t.Fatalf("in-flight duplicate response = %#v", duplicate)
	}
	if executions.Load() != 1 {
		t.Fatalf("handler executions = %d, want 1", executions.Load())
	}

	close(release)
	firstRaw, err := ReadFrameExact(firstClient)
	if err != nil {
		t.Fatalf("ReadFrameExact(first) returned an error: %v", err)
	}
	first := parseServerResponse(t, firstRaw)
	if !first.OK {
		t.Fatalf("first response = %#v", first)
	}
	firstClient.Close()
	<-firstDone
}

func TestServerReturnsCachedCompletedResponseAndRejectsSameIDDifferentDigest(t *testing.T) {
	t.Parallel()

	var executions atomic.Int32
	handler := &behaviorHandler{rotate: func(_ context.Context, _ Peer, _ Mutation, request RotateRequest) (EndpointRotationResult, error) {
		executions.Add(1)
		return EndpointRotationResult{PublicURL: request.PublicURL, Serial: "serial"}, nil
	}}
	server := newTestServer(handler, NewMemoryReplayStore(MemoryReplayConfig{}))
	firstRaw, _ := MarshalRequest(testRequestID, OperationRotate, RotateRequest{PublicName: "name", PublicURL: "url-a"})
	firstResponse := exchangeServer(t, server, NewPeer(501, nil), firstRaw)
	secondResponse := exchangeServer(t, server, NewPeer(501, nil), firstRaw)
	if !bytes.Equal(firstResponse, secondResponse) {
		t.Fatalf("cached response bytes changed:\nfirst:  %s\nsecond: %s", firstResponse, secondResponse)
	}
	if executions.Load() != 1 {
		t.Fatalf("handler executions after completed retry = %d, want 1", executions.Load())
	}

	differentRaw, _ := MarshalRequest(testRequestID, OperationRotate, RotateRequest{PublicName: "name", PublicURL: "url-b"})
	different := parseServerResponse(t, exchangeServer(t, server, NewPeer(501, nil), differentRaw))
	if different.OK || different.ErrorCode != ErrorDuplicateRequest {
		t.Fatalf("different digest response = %#v", different)
	}
	if executions.Load() != 1 {
		t.Fatal("same ID with a different digest reexecuted the handler")
	}
}

func TestServerFailsClosedOnInvalidCachedResponseBytes(t *testing.T) {
	t.Parallel()

	raw, _ := MarshalRequest(testRequestID, OperationStatus, StatusRequest{})
	request, err := ParseRequest(raw)
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryReplayStore(MemoryReplayConfig{})
	key := ReplayKey{RequestID: request.RequestID, Digest: request.Digest, Operation: request.Operation}
	reservation, err := store.Reserve(context.Background(), key)
	if err != nil || reservation.Decision != ReplayExecute {
		t.Fatalf("Reserve() = (%#v, %v)", reservation, err)
	}
	if err := store.CompleteStatus(context.Background(), key, []byte(`{"privateKey":"secret"}`)); err != nil {
		t.Fatal(err)
	}
	var executions atomic.Int32
	server := newTestServer(countingHandler(&executions), store)
	responseRaw := exchangeServer(t, server, NewPeer(501, nil), raw)
	if strings.Contains(string(responseRaw), "privateKey") || strings.Contains(string(responseRaw), "secret") {
		t.Fatalf("invalid cached bytes crossed the trust boundary: %s", responseRaw)
	}
	response := parseServerResponse(t, responseRaw)
	if response.OK || response.ErrorCode != ErrorUnavailable {
		t.Fatalf("invalid cache response = %#v, want unavailable", response)
	}
	if executions.Load() != 0 {
		t.Fatal("invalid completed cache entry reexecuted the handler")
	}
}

func TestServerTimesOutHandlerAndSuppressesLateSuccess(t *testing.T) {
	t.Parallel()

	handlerCanceled := make(chan struct{})
	allowLateResult := make(chan struct{})
	lateReturned := make(chan struct{})
	handler := &behaviorHandler{repair: func(ctx context.Context, _ Peer, _ Mutation) (RepairResult, error) {
		<-ctx.Done()
		close(handlerCanceled)
		<-allowLateResult
		close(lateReturned)
		return RepairResult{Ready: true, Restarting: false}, nil
	}}
	server := newTestServer(handler, NewMemoryReplayStore(MemoryReplayConfig{}))
	server.OperationLimit = 20 * time.Millisecond
	server.IOLimit = time.Second
	raw, _ := MarshalRequest(testRequestID, OperationRepair, RepairRequest{})

	serverSide, clientSide := tcpConnectionPair(t)
	done := make(chan struct{})
	go func() {
		server.ServeConn(context.Background(), serverSide, NewPeer(501, nil))
		close(done)
	}()
	if err := WriteFrame(clientSide, raw); err != nil {
		t.Fatal(err)
	}
	if err := clientSide.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	responseRaw, readErr := ReadFrameExact(clientSide)
	if readErr == nil {
		response := parseServerResponse(t, responseRaw)
		if response.OK {
			t.Fatalf("timeout emitted success: %#v", response)
		}
	}
	clientSide.Close()
	<-done
	<-handlerCanceled
	close(allowLateResult)
	<-lateReturned
	if strings.Contains(string(responseRaw), `"ready":true`) {
		t.Fatalf("late success leaked into response: %s", responseRaw)
	}
}

func TestServerDoesNotDispatchWhenReplayReservationFinishesAfterCancellation(t *testing.T) {
	t.Parallel()

	clockEntered := make(chan struct{})
	releaseClock := make(chan struct{})
	var clockCalls atomic.Int32
	store := NewMemoryReplayStore(MemoryReplayConfig{Now: func() time.Time {
		if clockCalls.Add(1) == 1 {
			close(clockEntered)
			<-releaseClock
		}
		return time.Now()
	}})
	handlerExecuted := make(chan struct{}, 1)
	handler := &behaviorHandler{status: func(context.Context, Peer) (StatusResult, error) {
		handlerExecuted <- struct{}{}
		return StatusResult{}, nil
	}}
	server := newTestServer(handler, store)
	raw, _ := MarshalRequest(testRequestID, OperationStatus, StatusRequest{})
	serverSide, clientSide := tcpConnectionPair(t)
	parent, cancel := context.WithCancel(context.Background())
	serverDone := make(chan struct{})
	go func() {
		server.ServeConn(parent, serverSide, NewPeer(501, nil))
		close(serverDone)
	}()
	if err := WriteFrame(clientSide, raw); err != nil {
		t.Fatalf("WriteFrame() returned an error: %v", err)
	}
	if err := clientSide.CloseWrite(); err != nil {
		t.Fatalf("CloseWrite() returned an error: %v", err)
	}
	<-clockEntered
	cancel()
	close(releaseClock)
	_ = clientSide.SetReadDeadline(time.Now().Add(time.Second))
	_, _ = ReadFrame(clientSide)
	clientSide.Close()
	<-serverDone
	select {
	case <-handlerExecuted:
		t.Fatal("handler dispatched after replay reservation crossed cancellation")
	case <-time.After(25 * time.Millisecond):
	}
}

func TestServerNeverSerializesRawHandlerErrors(t *testing.T) {
	t.Parallel()

	handler := &behaviorHandler{status: func(context.Context, Peer) (StatusResult, error) {
		return StatusResult{}, errors.New("secret: /Library/private owner-key.pem stderr")
	}}
	server := newTestServer(handler, NewMemoryReplayStore(MemoryReplayConfig{}))
	raw, _ := MarshalRequest(testRequestID, OperationStatus, StatusRequest{})
	responseRaw := exchangeServer(t, server, NewPeer(501, nil), raw)
	response := parseServerResponse(t, responseRaw)
	if response.OK || response.ErrorCode != ErrorOperationFailed {
		t.Fatalf("handler error response = %#v", response)
	}
	if strings.Contains(string(responseRaw), "secret") || strings.Contains(string(responseRaw), "/Library") || strings.Contains(string(responseRaw), "stderr") {
		t.Fatalf("raw handler error leaked: %s", responseRaw)
	}
}

func newTestServer(handler Handler, replay ReplayStore) *Server {
	return &Server{
		Authorize:      func(context.Context, Peer, Operation) bool { return true },
		Handler:        handler,
		Replay:         replay,
		OperationLimit: time.Second,
		IOLimit:        time.Second,
	}
}

func exchangeServer(t *testing.T, server *Server, peer Peer, request []byte) []byte {
	t.Helper()
	serverSide, clientSide := tcpConnectionPair(t)
	done := make(chan struct{})
	go func() {
		server.ServeConn(context.Background(), serverSide, peer)
		close(done)
	}()
	if err := WriteFrame(clientSide, request); err != nil {
		clientSide.Close()
		t.Fatalf("WriteFrame(request) returned an error: %v", err)
	}
	if err := clientSide.CloseWrite(); err != nil {
		clientSide.Close()
		t.Fatalf("CloseWrite(request) returned an error: %v", err)
	}
	response, err := ReadFrameExact(clientSide)
	if err != nil {
		clientSide.Close()
		t.Fatalf("ReadFrameExact(response) returned an error: %v", err)
	}
	clientSide.Close()
	<-done
	return response
}

func parseServerResponse(t *testing.T, raw []byte) Response {
	t.Helper()
	response, err := ParseResponse(raw)
	if err != nil {
		t.Fatalf("ParseResponse() returned an error for %s: %v", raw, err)
	}
	return response
}

type behaviorHandler struct {
	status func(context.Context, Peer) (StatusResult, error)
	setup  func(context.Context, Peer, Mutation, SetupRequest) (OwnerBootstrapResult, error)
	rotate func(context.Context, Peer, Mutation, RotateRequest) (EndpointRotationResult, error)
	repair func(context.Context, Peer, Mutation) (RepairResult, error)
}

func (handler *behaviorHandler) Status(ctx context.Context, peer Peer) (StatusResult, error) {
	if handler.status == nil {
		return StatusResult{}, errors.New("unexpected status")
	}
	return handler.status(ctx, peer)
}

func (handler *behaviorHandler) Setup(ctx context.Context, peer Peer, mutation Mutation, request SetupRequest) (OwnerBootstrapResult, error) {
	if handler.setup == nil {
		return OwnerBootstrapResult{}, errors.New("unexpected setup")
	}
	return handler.setup(ctx, peer, mutation, request)
}

func (handler *behaviorHandler) Rotate(ctx context.Context, peer Peer, mutation Mutation, request RotateRequest) (EndpointRotationResult, error) {
	if handler.rotate == nil {
		return EndpointRotationResult{}, errors.New("unexpected rotate")
	}
	return handler.rotate(ctx, peer, mutation, request)
}

func (handler *behaviorHandler) Repair(ctx context.Context, peer Peer, mutation Mutation) (RepairResult, error) {
	if handler.repair == nil {
		return RepairResult{}, errors.New("unexpected repair")
	}
	return handler.repair(ctx, peer, mutation)
}

func countingHandler(executions *atomic.Int32) Handler {
	return &behaviorHandler{
		status: func(context.Context, Peer) (StatusResult, error) {
			executions.Add(1)
			return StatusResult{}, nil
		},
		setup: func(context.Context, Peer, Mutation, SetupRequest) (OwnerBootstrapResult, error) {
			executions.Add(1)
			return OwnerBootstrapResult{}, nil
		},
		rotate: func(context.Context, Peer, Mutation, RotateRequest) (EndpointRotationResult, error) {
			executions.Add(1)
			return EndpointRotationResult{}, nil
		},
		repair: func(context.Context, Peer, Mutation) (RepairResult, error) {
			executions.Add(1)
			return RepairResult{}, nil
		},
	}
}

func deepEqual(got, want any) bool {
	return reflect.DeepEqual(got, want)
}
