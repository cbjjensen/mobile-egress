package relayadmin

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestClientTypedMethodsRoundTripAllOperations(t *testing.T) {
	t.Parallel()

	handler := &behaviorHandler{
		status: func(context.Context, Peer) (StatusResult, error) {
			return StatusResult{ProtocolVersion: 1, HelperVersion: "dev", Initialized: true, RelayRunning: true}, nil
		},
		setup: func(context.Context, Peer, Mutation, SetupRequest) (OwnerBootstrapResult, error) {
			return OwnerBootstrapResult{CertificatePEM: "owner", CACertificatePEM: "ca", Serial: "1", Role: "owner"}, nil
		},
		rotate: func(_ context.Context, _ Peer, _ Mutation, request RotateRequest) (EndpointRotationResult, error) {
			return EndpointRotationResult{PublicURL: request.PublicURL, Serial: "2"}, nil
		},
		repair: func(context.Context, Peer, Mutation) (RepairResult, error) {
			return RepairResult{Ready: true}, nil
		},
	}
	server := newTestServer(handler, NewMemoryReplayStore(MemoryReplayConfig{}))
	random := make([]byte, 64)
	for index := range random {
		random[index] = byte(index)
	}
	client := Client{
		Dial:           serverDialer(t, server, NewPeer(501, nil), nil),
		Random:         bytes.NewReader(random),
		OperationLimit: time.Second,
		IOLimit:        time.Second,
	}

	status, err := client.Status(context.Background())
	if err != nil || status.HelperVersion != "dev" {
		t.Fatalf("Status() = (%#v, %v)", status, err)
	}
	setup, err := client.Setup(context.Background(), SetupRequest{PublicName: "name", PublicURL: "url", OwnerCSRPEM: "csr"})
	if err != nil || setup.CertificatePEM != "owner" {
		t.Fatalf("Setup() = (%#v, %v)", setup, err)
	}
	rotate, err := client.Rotate(context.Background(), RotateRequest{PublicName: "name", PublicURL: "url-2"})
	if err != nil || rotate.PublicURL != "url-2" {
		t.Fatalf("Rotate() = (%#v, %v)", rotate, err)
	}
	repair, err := client.Repair(context.Background())
	if err != nil || !repair.Ready {
		t.Fatalf("Repair() = (%#v, %v)", repair, err)
	}
}

func TestClientRequiresExactResponseIDOperationAndVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		response func(string) []byte
	}{
		{
			name: "request ID",
			response: func(string) []byte {
				raw, _ := MarshalSuccessResponse("ffeeddccbbaa99887766554433221100", OperationStatus, StatusResult{})
				return raw
			},
		},
		{
			name: "operation",
			response: func(id string) []byte {
				raw, _ := MarshalSuccessResponse(id, OperationRepair, RepairResult{})
				return raw
			},
		},
		{
			name: "version",
			response: func(id string) []byte {
				return []byte(`{"version":2,"requestId":"` + id + `","operation":"status","ok":true,"result":{"protocolVersion":1,"helperVersion":"dev","initialized":true,"relayRunning":true}}`)
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			client := Client{
				Dial: singleResponseDialer(t, test.response),
				Random: bytes.NewReader([]byte{
					0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77,
					0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff,
				}),
				OperationLimit: time.Second,
				IOLimit:        time.Second,
			}
			if _, err := client.Status(context.Background()); !errors.Is(err, ErrInvalidResponse) {
				t.Fatalf("Status() error = %v, want ErrInvalidResponse", err)
			}
		})
	}
}

func TestClientRetriesTransportOnceWithByteIdenticalRequestAndSameID(t *testing.T) {
	t.Parallel()

	var executions atomic.Int32
	handler := &behaviorHandler{setup: func(context.Context, Peer, Mutation, SetupRequest) (OwnerBootstrapResult, error) {
		executions.Add(1)
		return OwnerBootstrapResult{CertificatePEM: "owner", CACertificatePEM: "ca", Serial: "1", Role: "owner"}, nil
	}}
	server := newTestServer(handler, NewMemoryReplayStore(MemoryReplayConfig{}))

	var attempts atomic.Int32
	var captureMu sync.Mutex
	var captures [][]byte
	var servers sync.WaitGroup
	dial := func(context.Context) (net.Conn, error) {
		attempt := attempts.Add(1)
		serverSide, clientSide := tcpConnectionPair(t)
		recorder := &recordingConn{Conn: serverSide}
		var connection net.Conn = recorder
		if attempt == 1 {
			connection = &failWriteConn{Conn: recorder}
		}
		servers.Add(1)
		go func() {
			defer servers.Done()
			server.ServeConn(context.Background(), connection, NewPeer(501, nil))
			captureMu.Lock()
			captures = append(captures, recorder.Bytes())
			captureMu.Unlock()
		}()
		return clientSide, nil
	}
	client := Client{
		Dial: dial,
		Random: bytes.NewReader([]byte{
			0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77,
			0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff,
		}),
		OperationLimit: time.Second,
		IOLimit:        time.Second,
	}

	result, err := client.Setup(context.Background(), SetupRequest{PublicName: "name", PublicURL: "url", OwnerCSRPEM: "csr"})
	servers.Wait()
	if err != nil {
		t.Fatalf("Setup() returned an error: %v", err)
	}
	if result.CertificatePEM != "owner" {
		t.Fatalf("Setup() = %#v", result)
	}
	if attempts.Load() != 2 {
		t.Fatalf("dial attempts = %d, want exactly 2", attempts.Load())
	}
	if executions.Load() != 1 {
		t.Fatalf("handler executions = %d, want 1 through completed replay", executions.Load())
	}
	if len(captures) != 2 || !bytes.Equal(captures[0], captures[1]) {
		t.Fatalf("retry request bytes differ: %q versus %q", captures[0], captures[1])
	}
}

func TestClientCapsLongDeadlineAtFiveMinutesAndHonorsShorterCancellation(t *testing.T) {
	t.Parallel()

	var recorded atomic.Int64
	dial := func(context.Context) (net.Conn, error) {
		serverSide, clientSide := tcpConnectionPair(t)
		go serveOneClientResponse(serverSide, func(id string) []byte {
			raw, _ := MarshalSuccessResponse(id, OperationStatus, StatusResult{})
			return raw
		})
		return &deadlineRecordingConn{Conn: clientSide, deadlineUnixNano: &recorded}, nil
	}
	client := Client{
		Dial:           dial,
		Random:         bytes.NewReader(bytes.Repeat([]byte{0x01}, 16)),
		OperationLimit: 24 * time.Hour,
		IOLimit:        24 * time.Hour,
	}
	started := time.Now()
	longContext, cancelLong := context.WithTimeout(context.Background(), 10*time.Minute)
	_, err := client.Status(longContext)
	cancelLong()
	if err != nil {
		t.Fatalf("Status(long deadline) returned an error: %v", err)
	}
	deadline := time.Unix(0, recorded.Load())
	if deadline.After(started.Add(OperationTimeout+time.Second)) || deadline.Before(started.Add(OperationTimeout-time.Second)) {
		t.Fatalf("connection deadline = %s, want approximately five minutes from %s", deadline, started)
	}

	var openConnectionsMu sync.Mutex
	var openConnections []net.Conn
	shortClient := Client{
		Dial: func(ctx context.Context) (net.Conn, error) {
			serverSide, clientSide := tcpConnectionPair(t)
			openConnectionsMu.Lock()
			openConnections = append(openConnections, serverSide)
			openConnectionsMu.Unlock()
			go func() {
				<-ctx.Done()
				serverSide.Close()
			}()
			return clientSide, nil
		},
		Random:         bytes.NewReader(bytes.Repeat([]byte{0x02}, 16)),
		OperationLimit: time.Minute,
		IOLimit:        time.Minute,
	}
	shortContext, cancelShort := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancelShort()
	started = time.Now()
	_, err = shortClient.Status(shortContext)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Status(short deadline) error = %v, want context deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("Status(short deadline) took %s", elapsed)
	}
	openConnectionsMu.Lock()
	for _, connection := range openConnections {
		connection.Close()
	}
	openConnectionsMu.Unlock()
}

func TestClientRejectsMalformedOversizedUnknownAndNonAllowlistedResponsesWithoutRetry(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		response func(string, net.Conn)
	}{
		{
			name: "malformed",
			response: func(_ string, connection net.Conn) {
				_ = WriteFrame(connection, []byte(`{"version":`))
			},
		},
		{
			name: "unknown field",
			response: func(id string, connection net.Conn) {
				_ = WriteFrame(connection, []byte(`{"version":1,"requestId":"`+id+`","operation":"status","ok":false,"errorCode":"unavailable","message":"raw"}`))
			},
		},
		{
			name: "non allowlisted error",
			response: func(id string, connection net.Conn) {
				_ = WriteFrame(connection, []byte(`{"version":1,"requestId":"`+id+`","operation":"status","ok":false,"errorCode":"sqlite_failure"}`))
			},
		},
		{
			name: "oversized",
			response: func(_ string, connection net.Conn) {
				_, _ = connection.Write(framePrefix(MaximumFrameSize + 1))
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var attempts atomic.Int32
			client := Client{
				Dial: func(context.Context) (net.Conn, error) {
					attempts.Add(1)
					serverSide, clientSide := tcpConnectionPair(t)
					go func() {
						defer serverSide.Close()
						requestRaw, err := ReadFrame(serverSide)
						if err != nil {
							return
						}
						request, err := ParseRequest(requestRaw)
						if err != nil {
							return
						}
						test.response(request.RequestID, serverSide)
					}()
					return clientSide, nil
				},
				Random:         bytes.NewReader(bytes.Repeat([]byte{0x03}, 16)),
				OperationLimit: time.Second,
				IOLimit:        time.Second,
			}
			if _, err := client.Status(context.Background()); !errors.Is(err, ErrInvalidResponse) {
				t.Fatalf("Status() error = %v, want ErrInvalidResponse", err)
			}
			if attempts.Load() != 1 {
				t.Fatalf("protocol failure dialed %d times, want no retry", attempts.Load())
			}
		})
	}
}

func TestClientReturnsOnlyAllowlistedRemoteError(t *testing.T) {
	t.Parallel()

	client := Client{
		Dial: singleResponseDialer(t, func(id string) []byte {
			raw, _ := MarshalErrorResponse(id, OperationRepair, ErrorStateIncompatible)
			return raw
		}),
		Random:         bytes.NewReader(bytes.Repeat([]byte{0x04}, 16)),
		OperationLimit: time.Second,
		IOLimit:        time.Second,
	}
	_, err := client.Repair(context.Background())
	var publicError *PublicError
	if !errors.As(err, &publicError) || publicError.Code != ErrorStateIncompatible {
		t.Fatalf("Repair() error = %v, want public state_incompatible", err)
	}
}

func singleResponseDialer(t *testing.T, response func(string) []byte) DialFunc {
	t.Helper()
	return func(context.Context) (net.Conn, error) {
		serverSide, clientSide := tcpConnectionPair(t)
		go serveOneClientResponse(serverSide, response)
		return clientSide, nil
	}
}

func serveOneClientResponse(connection net.Conn, response func(string) []byte) {
	defer connection.Close()
	requestRaw, err := ReadFrame(connection)
	if err != nil {
		return
	}
	request, err := ParseRequest(requestRaw)
	if err != nil {
		return
	}
	_ = WriteFrame(connection, response(request.RequestID))
}

func serverDialer(t *testing.T, server *Server, peer Peer, done *sync.WaitGroup) DialFunc {
	t.Helper()
	return func(context.Context) (net.Conn, error) {
		serverSide, clientSide := tcpConnectionPair(t)
		if done != nil {
			done.Add(1)
		}
		go func() {
			if done != nil {
				defer done.Done()
			}
			server.ServeConn(context.Background(), serverSide, peer)
		}()
		return clientSide, nil
	}
}

type recordingConn struct {
	net.Conn
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (connection *recordingConn) Read(buffer []byte) (int, error) {
	n, err := connection.Conn.Read(buffer)
	connection.mu.Lock()
	_, _ = connection.buffer.Write(buffer[:n])
	connection.mu.Unlock()
	return n, err
}

func (connection *recordingConn) Bytes() []byte {
	connection.mu.Lock()
	defer connection.mu.Unlock()
	return append([]byte(nil), connection.buffer.Bytes()...)
}

type failWriteConn struct{ net.Conn }

func (connection *failWriteConn) Write([]byte) (int, error) {
	return 0, io.ErrClosedPipe
}

type deadlineRecordingConn struct {
	net.Conn
	deadlineUnixNano *atomic.Int64
}

func (connection *deadlineRecordingConn) SetDeadline(deadline time.Time) error {
	connection.deadlineUnixNano.Store(deadline.UnixNano())
	return connection.Conn.SetDeadline(deadline)
}

func (connection *deadlineRecordingConn) CloseWrite() error {
	halfCloser, ok := connection.Conn.(interface{ CloseWrite() error })
	if !ok {
		return ErrTransport
	}
	return halfCloser.CloseWrite()
}
