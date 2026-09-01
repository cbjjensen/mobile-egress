//go:build capacityharness

package capacityharness

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestRealLoopbackTLS13TargetAuthenticatesHMACAndVerifiesExactSixteenKiBEcho(t *testing.T) {
	t.Parallel()

	token := []byte("0123456789abcdefghijklmnopqrstuv")
	serverTLS, roots, certificatePEM, keyPEM := newEchoServerTLS(t, "echo.example.com")
	address, serverResult := startTargetProtocolFixture(t, serverTLS, token)
	connection, err := net.Dial("tcp4", address)
	if err != nil {
		t.Fatal(err)
	}
	stream := newNetworkCapacityStream(connection)
	held, err := (EchoVerifier{Roots: roots}).Verify(context.Background(), stream, "echo.example.com", token)
	if err != nil {
		t.Fatalf("Verify() = %v", err)
	}
	if held == nil {
		t.Fatal("Verify() returned no held TLS stream")
	}
	_ = held.Close()
	if targetErr := <-serverResult; targetErr != nil {
		t.Fatalf("target protocol = %v", targetErr)
	}

	directory := t.TempDir()
	certificateFile := filepath.Join(directory, "fullchain.pem")
	privateKeyFile := filepath.Join(directory, "private-key.pem")
	if err := os.WriteFile(certificateFile, certificatePEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(privateKeyFile, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadTargetTLSConfig(TargetSecrets{
		Token: append([]byte(nil), token...), Hostname: "echo.example.com",
		CertificateFile: certificateFile, PrivateKeyFile: privateKeyFile,
	}, roots, time.Now())
	if err != nil {
		t.Fatalf("LoadTargetTLSConfig() = %v", err)
	}
	if loaded.MinVersion != tls.VersionTLS13 || loaded.MaxVersion != tls.VersionTLS13 {
		t.Fatalf("target TLS versions = %x/%x, want TLS 1.3 only", loaded.MinVersion, loaded.MaxVersion)
	}
	if _, err := LoadTargetTLSConfig(TargetSecrets{
		Token: append([]byte(nil), token...), Hostname: "wrong.example.com",
		CertificateFile: certificateFile, PrivateKeyFile: privateKeyFile,
	}, roots, time.Now()); err == nil {
		t.Fatal("LoadTargetTLSConfig accepted a certificate for a different hostname")
	}
}

func TestRealLoopbackTargetMapsAuthenticationShortExtraAndTLSFailuresToFixedCategories(t *testing.T) {
	t.Parallel()

	token := []byte("abcdefghijklmnopqrstuvwxyz012345")
	serverTLS, roots, _, _ := newEchoServerTLS(t, "echo.example.com")

	t.Run("authentication", func(t *testing.T) {
		address, result := startTargetProtocolFixture(t, serverTLS, token)
		connection := dialEchoTLS(t, address, roots)
		nonce := make([]byte, challengeNonceBytes)
		if _, err := io.ReadFull(connection, nonce); err != nil {
			t.Fatal(err)
		}
		_, _ = connection.Write(make([]byte, challengeProofBytes))
		_ = connection.Close()
		assertCategorized(t, <-result, FailureAuthentication)
	})

	t.Run("short payload", func(t *testing.T) {
		address, result := startTargetProtocolFixture(t, serverTLS, token)
		connection := dialEchoTLS(t, address, roots)
		writeValidChallengeProof(t, connection, token)
		_, _ = connection.Write(make([]byte, 100))
		_ = connection.Close()
		assertCategorized(t, <-result, FailureProtocol)
	})

	t.Run("extra payload", func(t *testing.T) {
		address, result := startTargetProtocolFixture(t, serverTLS, token)
		connection := dialEchoTLS(t, address, roots)
		writeValidChallengeProof(t, connection, token)
		_, _ = connection.Write(make([]byte, echoPayloadBytes+1))
		assertCategorized(t, <-result, FailureProtocol)
		_ = connection.Close()
	})

	t.Run("TLS", func(t *testing.T) {
		address, result := startTargetProtocolFixture(t, serverTLS, token)
		connection, err := net.Dial("tcp4", address)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = connection.Write([]byte("not TLS and SECRET-RAW-ERROR"))
		_ = connection.Close()
		targetErr := <-result
		assertCategorized(t, targetErr, FailureTLS)
		if targetErr != nil && (containsSecret(targetErr.Error(), "SECRET-RAW-ERROR") || containsSecret(targetErr.Error(), "not TLS")) {
			t.Fatal("target error disclosed raw peer input")
		}
	})

	t.Run("verified close during heartbeat", func(t *testing.T) {
		address, result := startTargetProtocolFixture(t, serverTLS, token)
		connection := dialEchoTLS(t, address, roots)
		writeValidChallengeProof(t, connection, token)
		payload := make([]byte, echoPayloadBytes)
		if _, err := connection.Write(payload); err != nil {
			t.Fatal(err)
		}
		if _, err := io.ReadFull(connection, payload); err != nil {
			t.Fatal(err)
		}
		if _, err := connection.Write([]byte{heartbeatFrameType}); err != nil {
			t.Fatal(err)
		}
		_ = connection.Close()
		if targetErr := <-result; targetErr != nil {
			t.Fatalf("verified heartbeat shutdown = %v, want clean close", targetErr)
		}
	})
}

func TestEchoVerifierRejectsCorruptedEchoWithFixedCategoryAndNoSecretDisclosure(t *testing.T) {
	t.Parallel()

	token := []byte("SECRET-TOKEN-0123456789-ABCDEFGH")
	serverTLS, roots, _, _ := newEchoServerTLS(t, "echo.example.com")
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		raw, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer raw.Close()
		connection := tls.Server(raw, serverTLS.Clone())
		nonce := make([]byte, challengeNonceBytes)
		_, _ = io.ReadFull(zeroReader{}, nonce)
		_, _ = connection.Write(nonce)
		proof := make([]byte, challengeProofBytes)
		_, _ = io.ReadFull(connection, proof)
		payload := make([]byte, echoPayloadBytes)
		_, _ = io.ReadFull(connection, payload)
		payload[0] ^= 0xff
		_, _ = connection.Write(payload)
	}()
	raw, err := net.Dial("tcp4", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_, verifyErr := (EchoVerifier{Roots: roots}).Verify(context.Background(), newNetworkCapacityStream(raw), "echo.example.com", token)
	assertCategorized(t, verifyErr, FailureEcho)
	if verifyErr != nil && containsSecret(verifyErr.Error(), "SECRET-TOKEN") {
		t.Fatal("verification error disclosed token")
	}
}

func TestEchoVerifierStopsCancellationWatcherBeforeReturningHeldStream(t *testing.T) {
	token := []byte("abcdefghijklmnopqrstuvwxyz012345")
	serverTLS, roots, _, _ := newEchoServerTLS(t, "echo.example.com")

	for attempt := 0; attempt < 16; attempt++ {
		address, serverResult := startTargetProtocolFixture(t, serverTLS, token)
		raw, err := net.Dial("tcp4", address)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		held, err := (EchoVerifier{Roots: roots}).Verify(ctx, newNetworkCapacityStream(raw), "echo.example.com", token)
		if err != nil {
			cancel()
			t.Fatalf("Verify() = %v", err)
		}

		// The phase context is canceled by the runner immediately after Verify
		// returns. A completed verifier must have retired its cancellation
		// watcher first or that watcher can tear down a successfully held stream.
		cancel()
		select {
		case <-held.Done():
			t.Fatalf("held stream %d was closed by the retired verification context", attempt+1)
		case <-time.After(5 * time.Millisecond):
		}
		_ = held.Close()
		if targetErr := <-serverResult; targetErr != nil {
			t.Fatalf("target protocol = %v", targetErr)
		}
	}
}

func TestCancellationWatcherAcknowledgesStopAndCancellation(t *testing.T) {
	t.Run("stop retires watcher before later cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		closed := make(chan struct{}, 1)
		watcher := startCancellationWatcher(ctx, func() error {
			closed <- struct{}{}
			return nil
		})
		watcher.stopAndWait()
		cancel()
		select {
		case <-closed:
			t.Fatal("retired watcher closed the resource after cancellation")
		case <-time.After(10 * time.Millisecond):
		}
	})

	t.Run("stop waits for an already selected cancellation close", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		entered := make(chan struct{})
		release := make(chan struct{})
		watcher := startCancellationWatcher(ctx, func() error {
			close(entered)
			<-release
			return nil
		})
		cancel()
		<-entered
		stopped := make(chan struct{})
		go func() {
			watcher.stopAndWait()
			close(stopped)
		}()
		select {
		case <-stopped:
			t.Fatal("stop returned before the selected cancellation close completed")
		case <-time.After(10 * time.Millisecond):
		}
		close(release)
		select {
		case <-stopped:
		case <-time.After(time.Second):
			t.Fatal("stop did not observe cancellation watcher completion")
		}
	})
}

func TestAuthenticatedHeartbeatKeepsHeldStreamLiveBeyondInjectedIdleWindow(t *testing.T) {
	token := []byte("abcdefghijklmnopqrstuvwxyz012345")
	serverTLS, roots, _, _ := newEchoServerTLS(t, "echo.example.com")
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	serverResult := make(chan error, 1)
	go func() {
		serverResult <- serveTargetListener(ctx, listener, TargetConfig{
			Token: append([]byte(nil), token...), TLSConfig: serverTLS,
			ConnectionTimeout: time.Second, CleanupTimeout: time.Second,
			HeartbeatTimeout: 60 * time.Millisecond, Emitter: discardEmitter{},
		})
	}()
	raw, err := net.Dial("tcp4", listener.Addr().String())
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	held, err := (EchoVerifier{
		Roots: roots, HeartbeatInterval: 10 * time.Millisecond, HeartbeatTimeout: 40 * time.Millisecond,
	}).Verify(context.Background(), newNetworkCapacityStream(raw), "echo.example.com", token)
	if err != nil {
		cancel()
		t.Fatalf("Verify() = %v", err)
	}
	select {
	case <-held.Done():
		t.Fatal("authenticated heartbeat did not keep the held stream live")
	case serveErr := <-serverResult:
		t.Fatalf("target stopped inside the injected idle window: %v", serveErr)
	case <-time.After(180 * time.Millisecond):
	}
	_ = held.Close()
	cancel()
	select {
	case serveErr := <-serverResult:
		if serveErr != nil {
			t.Fatalf("target cleanup = %v", serveErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("target heartbeat cleanup exceeded its bound")
	}
}

func TestHeldTLSStreamRejectsCloseClaimAfterProducerPublishedTerminalState(t *testing.T) {
	local, peer := net.Pipe()
	defer local.Close()
	defer peer.Close()
	underlying := &controlledUnderlyingCapacityStream{done: make(chan struct{})}
	underlying.publishTerminal()
	held := newHeldTLSStream(
		tls.Client(local, &tls.Config{MinVersion: tls.VersionTLS13, MaxVersion: tls.VersionTLS13}),
		underlying, time.Hour, time.Second,
	)
	select {
	case <-held.Done():
	case <-time.After(time.Second):
		t.Fatal("held stream producer did not publish terminal state")
	}
	if held.TryBeginClose() {
		t.Fatal("TryBeginClose accepted after the producer published terminal state")
	}
	if err := held.WaitClose(context.Background()); err != nil {
		t.Fatalf("WaitClose() = %v", err)
	}
}

func TestHeldTLSStreamRejectsCloseClaimWhileUnderlyingTerminalPublicationIsPending(t *testing.T) {
	local, peer := net.Pipe()
	defer local.Close()
	defer peer.Close()
	underlying := &controlledUnderlyingCapacityStream{done: make(chan struct{})}
	terminalSelected := make(chan struct{})
	publishWrapperTerminal := make(chan struct{})
	held := newHeldTLSStreamWithTerminalHook(
		tls.Client(local, &tls.Config{MinVersion: tls.VersionTLS13, MaxVersion: tls.VersionTLS13}),
		underlying, time.Hour, time.Second,
		func() {
			close(terminalSelected)
			<-publishWrapperTerminal
		},
	)
	underlying.publishTerminal()
	select {
	case <-terminalSelected:
	case <-time.After(time.Second):
		t.Fatal("held stream did not observe the underlying terminal state")
	}
	select {
	case <-held.Done():
		t.Fatal("held wrapper published terminal state before the controlled gap")
	default:
	}
	if held.TryBeginClose() {
		t.Fatal("TryBeginClose accepted after underlying terminal publication")
	}
	close(publishWrapperTerminal)
	select {
	case <-held.Done():
	case <-time.After(time.Second):
		t.Fatal("held wrapper did not finish terminal publication")
	}
}

type controlledUnderlyingCapacityStream struct {
	mu       sync.Mutex
	done     chan struct{}
	terminal bool
	claimed  bool
}

func (*controlledUnderlyingCapacityStream) Read([]byte) (int, error) { return 0, io.EOF }
func (*controlledUnderlyingCapacityStream) Write(value []byte) (int, error) {
	return len(value), nil
}
func (stream *controlledUnderlyingCapacityStream) Close() error {
	stream.publishTerminal()
	return nil
}
func (stream *controlledUnderlyingCapacityStream) Done() <-chan struct{} { return stream.done }
func (stream *controlledUnderlyingCapacityStream) TryBeginClose() bool {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if stream.terminal {
		return false
	}
	stream.claimed = true
	return true
}
func (stream *controlledUnderlyingCapacityStream) publishTerminal() {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if !stream.terminal {
		stream.terminal = true
		close(stream.done)
	}
}

func startTargetProtocolFixture(t *testing.T, serverTLS *tls.Config, token []byte) (string, <-chan error) {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		defer listener.Close()
		raw, acceptErr := listener.Accept()
		if acceptErr != nil {
			result <- CategorizedError{Category: FailureInternal}
			return
		}
		defer raw.Close()
		result <- handleTargetProtocol(context.Background(), tls.Server(raw, serverTLS.Clone()), token, nil, 5*time.Second, 20*time.Millisecond)
	}()
	return listener.Addr().String(), result
}

func dialEchoTLS(t *testing.T, address string, roots *x509.CertPool) *tls.Conn {
	t.Helper()
	raw, err := net.Dial("tcp4", address)
	if err != nil {
		t.Fatal(err)
	}
	connection := tls.Client(raw, &tls.Config{
		RootCAs: roots, ServerName: "echo.example.com", MinVersion: tls.VersionTLS13, MaxVersion: tls.VersionTLS13,
	})
	if err := connection.Handshake(); err != nil {
		t.Fatal(err)
	}
	return connection
}

func writeValidChallengeProof(t *testing.T, connection net.Conn, token []byte) {
	t.Helper()
	nonce := make([]byte, challengeNonceBytes)
	if _, err := io.ReadFull(connection, nonce); err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, token)
	_, _ = mac.Write(nonce)
	if _, err := connection.Write(mac.Sum(nil)); err != nil {
		t.Fatal(err)
	}
}

func newEchoServerTLS(t *testing.T, hostname string) (*tls.Config, *x509.CertPool, []byte, []byte) {
	t.Helper()
	ca, caKey, caPEM := newHarnessTestCA(t)
	certificate, leafPEM, keyPEM := newHarnessSignedCertificate(t, ca, caKey, &x509.Certificate{
		SerialNumber: big.NewInt(20), Subject: pkix.Name{CommonName: hostname}, DNSNames: []string{hostname},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	return &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS13, MaxVersion: tls.VersionTLS13}, roots, append(leafPEM, caPEM...), keyPEM
}

type networkCapacityStream struct {
	net.Conn
	done     chan struct{}
	stateMu  sync.Mutex
	terminal bool
	claimed  bool
}

func newNetworkCapacityStream(connection net.Conn) *networkCapacityStream {
	return &networkCapacityStream{Conn: connection, done: make(chan struct{})}
}

func (stream *networkCapacityStream) Close() error {
	stream.stateMu.Lock()
	if !stream.terminal {
		stream.terminal = true
		close(stream.done)
	}
	stream.stateMu.Unlock()
	return stream.Conn.Close()
}

func (stream *networkCapacityStream) Done() <-chan struct{} { return stream.done }
func (stream *networkCapacityStream) TryBeginClose() bool {
	stream.stateMu.Lock()
	defer stream.stateMu.Unlock()
	if stream.terminal {
		return false
	}
	stream.claimed = true
	return true
}

type zeroReader struct{}

func (zeroReader) Read(value []byte) (int, error) {
	for index := range value {
		value[index] = byte(index + 1)
	}
	return len(value), nil
}

func assertCategorized(t *testing.T, err error, want FailureCategory) {
	t.Helper()
	var categorized CategorizedError
	if !errors.As(err, &categorized) || categorized.Category != want {
		t.Fatalf("error = %v, want fixed category %s", err, want)
	}
}

func containsSecret(value, secret string) bool {
	return len(secret) > 0 && len(value) >= len(secret) && stringContains(value, secret)
}
func stringContains(value, search string) bool {
	for index := 0; index+len(search) <= len(value); index++ {
		if value[index:index+len(search)] == search {
			return true
		}
	}
	return false
}
