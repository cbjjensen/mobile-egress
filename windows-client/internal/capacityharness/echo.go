//go:build capacityharness

package capacityharness

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"sync"
	"time"
)

const (
	challengeNonceBytes      = 32
	challengeProofBytes      = sha256.Size
	heartbeatPayloadBytes    = 32
	heartbeatFrameBytes      = 1 + heartbeatPayloadBytes
	heartbeatFrameType       = byte(1)
	defaultHeartbeatInterval = 2 * time.Minute
	maximumHeartbeatInterval = 4 * time.Minute
	defaultHeartbeatTimeout  = 30 * time.Second
	maximumHeartbeatTimeout  = 2 * time.Minute
)

type EchoVerifier struct {
	Roots             *x509.CertPool
	Random            io.Reader
	HeartbeatInterval time.Duration
	HeartbeatTimeout  time.Duration
}

func (verifier EchoVerifier) Verify(ctx context.Context, stream CapacityStream, hostname string, token []byte) (HeldStream, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	heartbeatInterval, heartbeatTimeout, heartbeatErr := verifier.heartbeatSettings()
	if stream == nil || stream.Done() == nil || !validPublicHostname(hostname) || len(token) != tokenBytes || heartbeatErr != nil {
		return nil, CategorizedError{Category: FailureInput}
	}
	connection := &capacityStreamConn{stream: stream}
	tlsConnection := tls.Client(connection, &tls.Config{
		RootCAs: verifier.Roots, ServerName: hostname,
		MinVersion: tls.VersionTLS13, MaxVersion: tls.VersionTLS13,
	})
	watcher := startCancellationWatcher(ctx, stream.Close)
	fail := func(category FailureCategory, cause error) (HeldStream, error) {
		watcher.stopAndWait()
		if contextErr := ctx.Err(); contextErr != nil {
			category = failureCategoryForContext(contextErr)
			cause = contextErr
		}
		_ = tlsConnection.Close()
		return nil, CategorizedError{Category: category, cause: cause}
	}
	if err := tlsConnection.HandshakeContext(ctx); err != nil || tlsConnection.ConnectionState().Version != tls.VersionTLS13 {
		return fail(FailureTLS, err)
	}
	nonce := make([]byte, challengeNonceBytes)
	defer clear(nonce)
	if _, err := io.ReadFull(tlsConnection, nonce); err != nil {
		return fail(FailureProtocol, err)
	}
	mac := hmac.New(sha256.New, token)
	_, _ = mac.Write(nonce)
	proof := mac.Sum(nil)
	if err := writeFull(tlsConnection, proof); err != nil {
		clear(proof)
		return fail(FailureProtocol, err)
	}
	clear(proof)
	payload := make([]byte, echoPayloadBytes)
	defer clear(payload)
	random := verifier.Random
	if random == nil {
		random = rand.Reader
	}
	if _, err := io.ReadFull(random, payload); err != nil {
		return fail(FailureInternal, err)
	}
	if err := writeFull(tlsConnection, payload); err != nil {
		return fail(FailureProtocol, err)
	}
	echoed := make([]byte, echoPayloadBytes)
	defer clear(echoed)
	if _, err := io.ReadFull(tlsConnection, echoed); err != nil {
		return fail(FailureEcho, err)
	}
	if !hmac.Equal(payload, echoed) {
		return fail(FailureEcho, errors.New("capacity echo verification failed"))
	}
	watcher.stopAndWait()
	if contextErr := ctx.Err(); contextErr != nil {
		_ = tlsConnection.Close()
		return nil, CategorizedError{Category: failureCategoryForContext(contextErr), cause: contextErr}
	}
	return newHeldTLSStream(tlsConnection, stream, heartbeatInterval, heartbeatTimeout), nil
}

func (verifier EchoVerifier) heartbeatSettings() (time.Duration, time.Duration, error) {
	interval := verifier.HeartbeatInterval
	if interval == 0 {
		interval = defaultHeartbeatInterval
	}
	timeout := verifier.HeartbeatTimeout
	if timeout == 0 {
		timeout = defaultHeartbeatTimeout
	}
	if interval <= 0 || interval > maximumHeartbeatInterval || timeout <= 0 || timeout > maximumHeartbeatTimeout {
		return 0, 0, errors.New("capacity heartbeat configuration is invalid")
	}
	return interval, timeout, nil
}

type cancellationWatcher struct {
	stopOnce sync.Once
	stop     chan struct{}
	done     chan struct{}
}

func startCancellationWatcher(ctx context.Context, closeResource func() error) *cancellationWatcher {
	watcher := &cancellationWatcher{stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(watcher.done)
		select {
		case <-ctx.Done():
			_ = closeResource()
		case <-watcher.stop:
		}
	}()
	return watcher
}

func (watcher *cancellationWatcher) stopAndWait() {
	watcher.stopOnce.Do(func() { close(watcher.stop) })
	<-watcher.done
}

func failureCategoryForContext(err error) FailureCategory {
	if errors.Is(err, context.DeadlineExceeded) {
		return FailureTimeout
	}
	return FailureCanceled
}

type heldTLSStream struct {
	connection        *tls.Conn
	underlying        CapacityStream
	heartbeatInterval time.Duration
	heartbeatTimeout  time.Duration
	done              chan struct{}
	stop              chan struct{}
	workerDone        chan struct{}
	stateMu           sync.Mutex
	closeClaimed      bool
	terminal          bool
	err               error
	beforeTerminal    func()
}

func newHeldTLSStream(connection *tls.Conn, underlying CapacityStream, heartbeatInterval, heartbeatTimeout time.Duration) *heldTLSStream {
	return newHeldTLSStreamWithTerminalHook(connection, underlying, heartbeatInterval, heartbeatTimeout, nil)
}

func newHeldTLSStreamWithTerminalHook(
	connection *tls.Conn,
	underlying CapacityStream,
	heartbeatInterval, heartbeatTimeout time.Duration,
	beforeTerminal func(),
) *heldTLSStream {
	stream := &heldTLSStream{
		connection: connection, underlying: underlying,
		heartbeatInterval: heartbeatInterval, heartbeatTimeout: heartbeatTimeout,
		done: make(chan struct{}), stop: make(chan struct{}), workerDone: make(chan struct{}),
		beforeTerminal: beforeTerminal,
	}
	go stream.runHeartbeats()
	return stream
}

func (stream *heldTLSStream) Close() error {
	stream.TryBeginClose()
	return stream.WaitClose(context.Background())
}

func (stream *heldTLSStream) Done() <-chan struct{} { return stream.done }

func (stream *heldTLSStream) TryBeginClose() bool {
	stream.stateMu.Lock()
	defer stream.stateMu.Unlock()
	if stream.terminal {
		return false
	}
	if !stream.closeClaimed {
		if stream.underlying == nil || !stream.underlying.TryBeginClose() {
			return false
		}
		stream.closeClaimed = true
		close(stream.stop)
	}
	return true
}

func (stream *heldTLSStream) WaitClose(ctx context.Context) error {
	select {
	case <-stream.workerDone:
		return stream.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (stream *heldTLSStream) runHeartbeats() {
	defer func() {
		if stream.beforeTerminal != nil {
			stream.beforeTerminal()
		}
		stream.stateMu.Lock()
		stream.terminal = true
		stream.stateMu.Unlock()
		close(stream.done)
		close(stream.workerDone)
	}()
	ticker := time.NewTicker(stream.heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stream.stop:
			stream.err = stream.connection.Close()
			return
		case <-stream.underlying.Done():
			return
		case <-ticker.C:
			if stream.exchangeHeartbeat() != nil {
				_ = stream.connection.Close()
				return
			}
		}
	}
}

func (stream *heldTLSStream) exchangeHeartbeat() error {
	frame := make([]byte, heartbeatFrameBytes)
	defer clear(frame)
	frame[0] = heartbeatFrameType
	if _, err := io.ReadFull(rand.Reader, frame[1:]); err != nil {
		return err
	}
	operationCtx, cancelOperation := context.WithTimeout(context.Background(), stream.heartbeatTimeout)
	watcher := startCancellationWatcher(operationCtx, stream.connection.Close)
	err := writeFull(stream.connection, frame)
	echoed := make([]byte, heartbeatFrameBytes)
	if err == nil {
		_, err = io.ReadFull(stream.connection, echoed)
	}
	watcher.stopAndWait()
	contextErr := operationCtx.Err()
	cancelOperation()
	if contextErr != nil {
		clear(echoed)
		return contextErr
	}
	matched := hmac.Equal(frame, echoed)
	clear(echoed)
	if err != nil {
		return err
	}
	if !matched {
		return errors.New("capacity heartbeat verification failed")
	}
	return nil
}

type capacityStreamConn struct{ stream CapacityStream }

func (connection *capacityStreamConn) Read(value []byte) (int, error) {
	return connection.stream.Read(value)
}
func (connection *capacityStreamConn) Write(value []byte) (int, error) {
	return connection.stream.Write(value)
}
func (connection *capacityStreamConn) Close() error          { return connection.stream.Close() }
func (*capacityStreamConn) LocalAddr() net.Addr              { return capacityAddr("local") }
func (*capacityStreamConn) RemoteAddr() net.Addr             { return capacityAddr("remote") }
func (*capacityStreamConn) SetDeadline(time.Time) error      { return nil }
func (*capacityStreamConn) SetReadDeadline(time.Time) error  { return nil }
func (*capacityStreamConn) SetWriteDeadline(time.Time) error { return nil }

type capacityAddr string

func (address capacityAddr) Network() string { return "capacity" }
func (address capacityAddr) String() string  { return string(address) }

func handleTargetProtocol(ctx context.Context, connection net.Conn, token []byte, random io.Reader, operationTimeout, extraProbeTimeout time.Duration) error {
	return handleTargetProtocolObserved(ctx, connection, token, random, operationTimeout, extraProbeTimeout, defaultTargetHeartbeatTimeout, nil)
}

func handleTargetProtocolObserved(
	ctx context.Context,
	connection net.Conn,
	token []byte,
	random io.Reader,
	operationTimeout time.Duration,
	extraProbeTimeout time.Duration,
	heartbeatTimeout time.Duration,
	onVerified func() error,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if connection == nil || len(token) != tokenBytes || operationTimeout <= 0 || extraProbeTimeout <= 0 ||
		heartbeatTimeout <= 0 || heartbeatTimeout > maximumTargetHeartbeatTimeout {
		return CategorizedError{Category: FailureInput}
	}
	tlsConnection, ok := connection.(*tls.Conn)
	if !ok {
		return CategorizedError{Category: FailureTLS}
	}
	if err := tlsConnection.SetDeadline(time.Now().Add(operationTimeout)); err != nil {
		return CategorizedError{Category: FailureProtocol, cause: err}
	}
	if err := tlsConnection.HandshakeContext(ctx); err != nil || tlsConnection.ConnectionState().Version != tls.VersionTLS13 {
		return CategorizedError{Category: FailureTLS, cause: err}
	}
	randomSource := random
	if randomSource == nil {
		randomSource = rand.Reader
	}
	nonce := make([]byte, challengeNonceBytes)
	defer clear(nonce)
	if _, err := io.ReadFull(randomSource, nonce); err != nil {
		return CategorizedError{Category: FailureInternal, cause: err}
	}
	if err := writeFull(tlsConnection, nonce); err != nil {
		return CategorizedError{Category: FailureProtocol, cause: err}
	}
	proof := make([]byte, challengeProofBytes)
	defer clear(proof)
	if _, err := io.ReadFull(tlsConnection, proof); err != nil {
		return CategorizedError{Category: FailureAuthentication, cause: err}
	}
	mac := hmac.New(sha256.New, token)
	_, _ = mac.Write(nonce)
	wantProof := mac.Sum(nil)
	authenticated := hmac.Equal(proof, wantProof)
	clear(wantProof)
	if !authenticated {
		return CategorizedError{Category: FailureAuthentication}
	}
	payload := make([]byte, echoPayloadBytes)
	defer clear(payload)
	if _, err := io.ReadFull(tlsConnection, payload); err != nil {
		return CategorizedError{Category: FailureProtocol, cause: err}
	}
	if err := tlsConnection.SetReadDeadline(time.Now().Add(extraProbeTimeout)); err != nil {
		return CategorizedError{Category: FailureProtocol, cause: err}
	}
	extra := []byte{0}
	count, extraErr := tlsConnection.Read(extra)
	clear(extra)
	if count != 0 {
		return CategorizedError{Category: FailureProtocol}
	}
	var timeout net.Error
	if !errors.As(extraErr, &timeout) || !timeout.Timeout() {
		return CategorizedError{Category: FailureProtocol, cause: extraErr}
	}
	if err := tlsConnection.SetDeadline(time.Now().Add(operationTimeout)); err != nil {
		return CategorizedError{Category: FailureProtocol, cause: err}
	}
	if err := writeFull(tlsConnection, payload); err != nil {
		return CategorizedError{Category: FailureEcho, cause: err}
	}
	if onVerified != nil {
		if err := onVerified(); err != nil {
			return CategorizedError{Category: FailureInternal, cause: err}
		}
	}
	watcher := startCancellationWatcher(ctx, tlsConnection.Close)
	defer watcher.stopAndWait()
	heartbeat := make([]byte, heartbeatFrameBytes)
	defer clear(heartbeat)
	for {
		if err := tlsConnection.SetReadDeadline(time.Now().Add(heartbeatTimeout)); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return CategorizedError{Category: FailureProtocol, cause: err}
		}
		count, err := io.ReadFull(tlsConnection, heartbeat)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if count > 0 && heartbeat[0] != heartbeatFrameType {
				return CategorizedError{Category: FailureProtocol}
			}
			var timeout net.Error
			if errors.As(err, &timeout) && timeout.Timeout() {
				return CategorizedError{Category: FailureTimeout, cause: err}
			}
			// Once the authenticated 16 KiB exchange is complete, EOF, reset,
			// or a partial heartbeat is a stream shutdown. The runner still
			// treats an unexpected Done as an acceptance failure; the target
			// must not turn cleanup racing a heartbeat into a protocol fault.
			return nil
		}
		if heartbeat[0] != heartbeatFrameType {
			return CategorizedError{Category: FailureProtocol}
		}
		if err := tlsConnection.SetWriteDeadline(time.Now().Add(operationTimeout)); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return CategorizedError{Category: FailureProtocol, cause: err}
		}
		if err := writeFull(tlsConnection, heartbeat); err != nil {
			return nil
		}
		clear(heartbeat)
	}
}

func writeFull(writer io.Writer, value []byte) error {
	for len(value) > 0 {
		written, err := writer.Write(value)
		if err != nil {
			return err
		}
		if written <= 0 || written > len(value) {
			return io.ErrShortWrite
		}
		value = value[written:]
	}
	return nil
}
