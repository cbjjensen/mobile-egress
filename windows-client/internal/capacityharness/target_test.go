//go:build capacityharness

package capacityharness

import (
	"bytes"
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

func TestTargetServerUsesOnlyLoopbackReportsVerificationAndCleansUpBoundedly(t *testing.T) {
	t.Parallel()

	token := []byte("0123456789abcdefghijklmnopqrstuv")
	serverTLS, roots, _, _ := newEchoServerTLS(t, "echo.example.com")
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().(*net.TCPAddr)
	if !address.IP.Equal(net.ParseIP("127.0.0.1")) {
		t.Fatalf("listener address = %v, want IPv4 loopback", address)
	}
	ctx, cancel := context.WithCancel(context.Background())
	emitter := &recordingEmitter{events: make(chan Event, 32)}
	serverResult := make(chan error, 1)
	go func() {
		serverResult <- serveTargetListener(ctx, listener, TargetConfig{
			Token: append([]byte(nil), token...), TLSConfig: serverTLS,
			ConnectionTimeout: 5 * time.Second, CleanupTimeout: time.Second, Emitter: emitter,
		})
	}()

	raw, err := net.Dial("tcp4", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	held, err := (EchoVerifier{Roots: roots}).Verify(context.Background(), newNetworkCapacityStream(raw), "echo.example.com", token)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.After(2 * time.Second)
	verified := false
	for !verified {
		select {
		case event := <-emitter.events:
			verified = event.Phase == PhaseTarget && event.Verified == 1 && event.Failure == FailureNone
		case <-deadline:
			t.Fatal("target did not report verification while the stream remained held")
		}
	}

	_ = held.Close()
	closedDeadline := time.After(2 * time.Second)
	closed := false
	for !closed {
		select {
		case event := <-emitter.events:
			closed = event.Phase == PhaseTarget && event.Closed == 1 && event.Failure == FailureNone
		case <-closedDeadline:
			t.Fatal("target did not report the closed verified stream")
		}
	}
	cancel()
	select {
	case serveErr := <-serverResult:
		if serveErr != nil {
			t.Fatalf("serveTargetListener() = %v", serveErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("target cleanup exceeded its bound")
	}
}

func TestTargetKeepsVerifiedStreamsAliveAfterConnectionLocalFailures(t *testing.T) {
	token := []byte("abcdefghijklmnopqrstuvwxyz012345")
	serverTLS, roots, _, _ := newEchoServerTLS(t, "echo.example.com")
	tests := []struct {
		name    string
		failure FailureCategory
		inject  func(*testing.T, string)
	}{
		{
			name: "TLS", failure: FailureTLS,
			inject: func(t *testing.T, address string) {
				connection, err := net.Dial("tcp4", address)
				if err != nil {
					t.Fatal(err)
				}
				_, _ = connection.Write([]byte("invalid TLS peer"))
				_ = connection.Close()
			},
		},
		{
			name: "short HMAC", failure: FailureAuthentication,
			inject: func(t *testing.T, address string) {
				connection := dialEchoTLS(t, address, roots)
				nonce := make([]byte, challengeNonceBytes)
				if _, err := io.ReadFull(connection, nonce); err != nil {
					t.Fatal(err)
				}
				_, _ = connection.Write([]byte{1})
				_ = connection.Close()
			},
		},
		{
			name: "bad HMAC", failure: FailureAuthentication,
			inject: func(t *testing.T, address string) {
				connection := dialEchoTLS(t, address, roots)
				nonce := make([]byte, challengeNonceBytes)
				if _, err := io.ReadFull(connection, nonce); err != nil {
					t.Fatal(err)
				}
				_, _ = connection.Write(make([]byte, challengeProofBytes))
				_ = connection.Close()
			},
		},
		{
			name: "short authenticated payload", failure: FailureProtocol,
			inject: func(t *testing.T, address string) {
				connection := dialEchoTLS(t, address, roots)
				writeValidChallengeProof(t, connection, token)
				_, _ = connection.Write(make([]byte, 100))
				_ = connection.Close()
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			listener, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			emitter := &recordingEmitter{events: make(chan Event, 64)}
			serverResult := make(chan error, 1)
			go func() {
				serverResult <- serveTargetListener(ctx, listener, TargetConfig{
					Token: append([]byte(nil), token...), TLSConfig: serverTLS,
					ConnectionTimeout: time.Second, CleanupTimeout: time.Second, Emitter: emitter,
				})
			}()

			raw, err := net.Dial("tcp4", listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			held, err := (EchoVerifier{Roots: roots}).Verify(context.Background(), newNetworkCapacityStream(raw), "echo.example.com", token)
			if err != nil {
				t.Fatalf("initial Verify() = %v", err)
			}
			defer held.Close()
			waitForTargetEvent(t, emitter.events, func(event Event) bool {
				return event.Verified == 1 && event.Failure == FailureNone
			})

			test.inject(t, listener.Addr().String())
			waitForTargetEvent(t, emitter.events, func(event Event) bool {
				return event.Closed >= 1 && event.Failure == test.failure
			})
			select {
			case serveErr := <-serverResult:
				t.Fatalf("connection-local %s failure stopped target: %v", test.name, serveErr)
			case <-held.Done():
				t.Fatalf("connection-local %s failure closed a verified stream", test.name)
			case <-time.After(25 * time.Millisecond):
			}

			replacementRaw, err := net.Dial("tcp4", listener.Addr().String())
			if err != nil {
				t.Fatalf("dial after %s failure = %v", test.name, err)
			}
			replacement, err := (EchoVerifier{Roots: roots}).Verify(context.Background(), newNetworkCapacityStream(replacementRaw), "echo.example.com", token)
			if err != nil {
				t.Fatalf("Verify() after %s failure = %v", test.name, err)
			}
			_ = replacement.Close()
			_ = held.Close()
			cancel()
			select {
			case serveErr := <-serverResult:
				if serveErr != nil {
					t.Fatalf("target cleanup after %s failure = %v", test.name, serveErr)
				}
			case <-time.After(2 * time.Second):
				t.Fatalf("target did not clean up after %s failure", test.name)
			}
		})
	}
}

func TestServeTargetRejectsUnsafeListenPortsBeforeBinding(t *testing.T) {
	t.Parallel()

	serverTLS, _, _, _ := newEchoServerTLS(t, "echo.example.com")
	for _, port := range []uint16{0, 1, 443, 1023} {
		err := ServeTarget(context.Background(), TargetConfig{
			Token: []byte("0123456789abcdefghijklmnopqrstuv"), TLSConfig: serverTLS, ListenPort: port,
			ConnectionTimeout: time.Second, CleanupTimeout: time.Second, Emitter: discardEmitter{},
		})
		assertCategorized(t, err, FailureInput)
	}
}

func TestTargetAllowsOneReplacementOverlapAndRejectsASecond(t *testing.T) {
	token := []byte("abcdefghijklmnopqrstuvwxyz012345")
	serverTLS, roots, _, _ := newEchoServerTLS(t, "echo.example.com")
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	emitter := &recordingEmitter{events: make(chan Event, 1024)}
	serverResult := make(chan error, 1)
	go func() {
		serverResult <- serveTargetListener(ctx, listener, TargetConfig{
			Token: append([]byte(nil), token...), TLSConfig: serverTLS,
			ConnectionTimeout: 5 * time.Second, CleanupTimeout: time.Second, Emitter: emitter,
		})
	}()

	legitimateRaw, err := net.Dial("tcp4", listener.Addr().String())
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	legitimate, err := (EchoVerifier{Roots: roots}).Verify(context.Background(), newNetworkCapacityStream(legitimateRaw), "echo.example.com", token)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	connections := make([]net.Conn, 0, maximumTargetConnections-1)
	defer func() {
		_ = legitimate.Close()
		cancel()
		for _, connection := range connections {
			_ = connection.Close()
		}
	}()
	for index := 0; index < maximumTargetConnections-1; index++ {
		connection, dialErr := net.DialTimeout("tcp4", listener.Addr().String(), time.Second)
		if dialErr != nil {
			t.Fatalf("connection %d: %v", index+1, dialErr)
		}
		connections = append(connections, connection)
	}
	waitForTargetEvent(t, emitter.events, func(event Event) bool {
		return event.Attempted == maximumTargetConnections && event.Open == maximumTargetConnections
	})
	secondOverlap, err := net.DialTimeout("tcp4", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatalf("second overlap connection: %v", err)
	}
	defer secondOverlap.Close()
	waitForTargetEventOrFailure(t, emitter.events, serverResult, func(event Event) bool {
		return event.Attempted == maximumTargetConnections+1 && event.Failure == FailureProtocol
	})
	select {
	case serveErr := <-serverResult:
		t.Fatalf("second overlap stopped target: %v", serveErr)
	case <-legitimate.Done():
		t.Fatal("second overlap closed the verified legitimate stream")
	case <-time.After(25 * time.Millisecond):
	}
	_ = connections[len(connections)-1].Close()
	connections = connections[:len(connections)-1]
	waitForTargetEvent(t, emitter.events, func(event Event) bool {
		return event.Closed >= 2 && event.Failure == FailureTLS
	})
	replacementRaw, err := net.DialTimeout("tcp4", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatalf("replacement dial after rejected overlap = %v", err)
	}
	replacement, err := (EchoVerifier{Roots: roots}).Verify(context.Background(), newNetworkCapacityStream(replacementRaw), "echo.example.com", token)
	if err != nil {
		t.Fatalf("replacement Verify() after rejected overlap = %v", err)
	}
	_ = replacement.Close()
}

func TestTargetSaturatesReportedCountsWithoutRejectingConnectionsAfterLifetimeBound(t *testing.T) {
	token := []byte("abcdefghijklmnopqrstuvwxyz012345")
	serverTLS, roots, _, _ := newEchoServerTLS(t, "echo.example.com")
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var output bytes.Buffer
	emitter := &observingEmitter{delegate: NewJSONEmitter(&output), events: make(chan Event, 8)}
	serverResult := make(chan error, 1)
	go func() {
		serverResult <- serveTargetListener(ctx, listener, TargetConfig{
			Token: append([]byte(nil), token...), TLSConfig: serverTLS,
			ConnectionTimeout: time.Second, CleanupTimeout: time.Second, Emitter: emitter,
		})
	}()

	for attempt := 1; attempt <= maxReportedCount+16; attempt++ {
		connection, dialErr := net.Dial("tcp4", listener.Addr().String())
		if dialErr != nil {
			t.Fatalf("malformed connection %d dial = %v", attempt, dialErr)
		}
		_, _ = connection.Write([]byte("invalid TLS peer"))
		_ = connection.Close()
		wantCount := attempt
		if wantCount > maxReportedCount {
			wantCount = maxReportedCount
		}
		if attempt <= maxReportedCount {
			waitForTargetEventOrFailure(t, emitter.events, serverResult, func(event Event) bool {
				return event.Attempted == wantCount && event.Open == wantCount &&
					event.Closed == wantCount && event.Failure == FailureTLS
			})
		}
	}

	raw, err := net.Dial("tcp4", listener.Addr().String())
	if err != nil {
		t.Fatalf("legitimate dial after lifetime report bound = %v", err)
	}
	held, err := (EchoVerifier{Roots: roots}).Verify(context.Background(), newNetworkCapacityStream(raw), "echo.example.com", token)
	if err != nil {
		t.Fatalf("legitimate Verify() after lifetime report bound = %v", err)
	}
	select {
	case <-held.Done():
		t.Fatal("post-bound legitimate stream was not held")
	case serveErr := <-serverResult:
		t.Fatalf("post-bound legitimate connection stopped target: %v", serveErr)
	case <-time.After(25 * time.Millisecond):
	}
	const maximumExpectedTargetEvents = 1024
	if lines := bytes.Count(output.Bytes(), []byte{'\n'}); lines != maximumExpectedTargetEvents {
		t.Fatalf("JSON event lines = %d, want finite budget %d", lines, maximumExpectedTargetEvents)
	}
	if output.Len() > maximumExpectedTargetEvents*256 {
		t.Fatalf("bounded JSON output = %d bytes, want at most %d", output.Len(), maximumExpectedTargetEvents*256)
	}
	_ = held.Close()
	cancel()
	select {
	case serveErr := <-serverResult:
		if serveErr != nil {
			t.Fatalf("post-bound target cleanup = %v", serveErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("post-bound target cleanup exceeded its bound")
	}
}

func waitForTargetEvent(t *testing.T, events <-chan Event, matches func(Event) bool) Event {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case event := <-events:
			if matches(event) {
				return event
			}
		case <-deadline:
			t.Fatal("target did not emit the expected bounded event")
		}
	}
}

func waitForTargetEventOrFailure(t *testing.T, events <-chan Event, serverResult <-chan error, matches func(Event) bool) Event {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case event := <-events:
			if matches(event) {
				return event
			}
		case err := <-serverResult:
			t.Fatalf("target stopped while waiting for a connection-local event: %v", err)
		case <-deadline:
			t.Fatal("target did not emit the expected bounded event")
		}
	}
}

type recordingEmitter struct {
	mu     sync.Mutex
	events chan Event
}

type observingEmitter struct {
	delegate Emitter
	events   chan Event
}

func (emitter *observingEmitter) Emit(event Event) error {
	if err := emitter.delegate.Emit(event); err != nil {
		return err
	}
	select {
	case emitter.events <- event:
	default:
	}
	return nil
}

func (emitter *recordingEmitter) Emit(event Event) error {
	emitter.mu.Lock()
	defer emitter.mu.Unlock()
	select {
	case emitter.events <- event:
	default:
	}
	return nil
}
