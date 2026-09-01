//go:build capacityharness

package capacityharness

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

const (
	minimumTargetListenPort = 1024
	// The relay still admits at most aggregateStreams. The target permits one
	// bounded retiring+replacement overlap because relay slot release can race
	// with the old target-side TCP connection draining its TLS close.
	maximumTargetConnections      = aggregateStreams + 1
	maximumTargetEvents           = 1024
	defaultExtraDataProbe         = 20 * time.Millisecond
	defaultTargetHeartbeatTimeout = 4 * time.Minute
	maximumTargetHeartbeatTimeout = 4*time.Minute + 30*time.Second
)

type TargetConfig struct {
	Token             []byte
	TLSConfig         *tls.Config
	ListenPort        uint16
	ConnectionTimeout time.Duration
	CleanupTimeout    time.Duration
	HeartbeatTimeout  time.Duration
	Emitter           Emitter
}

func ServeTarget(ctx context.Context, config TargetConfig) error {
	defer clear(config.Token)
	if config.ListenPort < minimumTargetListenPort || !validTargetConfig(config) {
		return CategorizedError{Category: FailureInput}
	}
	listenConfig := net.ListenConfig{}
	listener, err := listenConfig.Listen(ctx, "tcp4", fmt.Sprintf("127.0.0.1:%d", config.ListenPort))
	if err != nil {
		return CategorizedError{Category: FailureInternal, cause: err}
	}
	return serveTargetListener(ctx, listener, config)
}

func serveTargetListener(ctx context.Context, listener net.Listener, config TargetConfig) error {
	defer clear(config.Token)
	if ctx == nil {
		ctx = context.Background()
	}
	if listener == nil || !validTargetConfig(config) || !loopbackListener(listener) {
		if listener != nil {
			_ = listener.Close()
		}
		return CategorizedError{Category: FailureInput}
	}
	if config.Emitter == nil {
		config.Emitter = discardEvents{}
	}
	serverCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	state := &targetState{
		listener: listener, config: config, connections: make(map[net.Conn]struct{}),
		semaphore: make(chan struct{}, maximumTargetConnections), failures: make(chan error, 1),
	}
	listenerClosed := make(chan struct{})
	go func() {
		select {
		case <-serverCtx.Done():
			_ = listener.Close()
		case <-listenerClosed:
		}
	}()

	var serveErr error
acceptLoop:
	for {
		connection, err := listener.Accept()
		if err != nil {
			select {
			case failure := <-state.failures:
				serveErr = failure
			default:
				if ctx.Err() == nil {
					serveErr = CategorizedError{Category: FailureInternal, cause: err}
				}
			}
			break acceptLoop
		}
		state.recordAttempt()
		select {
		case state.semaphore <- struct{}{}:
		default:
			_ = connection.Close()
			incrementReported(&state.closed)
			if emitErr := state.emit(FailureProtocol); emitErr != nil {
				serveErr = CategorizedError{Category: FailureInternal, cause: emitErr}
				break acceptLoop
			}
			continue
		}
		state.mu.Lock()
		state.connections[connection] = struct{}{}
		state.mu.Unlock()
		incrementReported(&state.open)
		if err := state.emit(FailureNone); err != nil {
			serveErr = CategorizedError{Category: FailureInternal, cause: err}
			break acceptLoop
		}
		state.workers.Add(1)
		go state.handle(serverCtx, connection)
		select {
		case failure := <-state.failures:
			serveErr = failure
			break acceptLoop
		default:
		}
	}
	close(listenerClosed)
	cancel()
	_ = listener.Close()
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), config.CleanupTimeout)
	defer cleanupCancel()
	if cleanupErr := state.cleanup(cleanupCtx); cleanupErr != nil && serveErr == nil {
		serveErr = CategorizedError{Category: FailureCleanup, cause: cleanupErr}
	}
	if serveErr == nil {
		select {
		case failure := <-state.failures:
			serveErr = failure
		default:
		}
	}
	if serveErr != nil {
		return fixedCategorized(serveErr)
	}
	return nil
}

func validTargetConfig(config TargetConfig) bool {
	return len(config.Token) == tokenBytes && config.TLSConfig != nil && len(config.TLSConfig.Certificates) == 1 &&
		config.TLSConfig.MinVersion == tls.VersionTLS13 && config.TLSConfig.MaxVersion == tls.VersionTLS13 &&
		config.ConnectionTimeout > 0 && config.ConnectionTimeout <= maxPhaseTimeout &&
		config.CleanupTimeout > 0 && config.CleanupTimeout <= maxCleanupTimeout &&
		config.HeartbeatTimeout >= 0 && config.HeartbeatTimeout <= maximumTargetHeartbeatTimeout
}

func loopbackListener(listener net.Listener) bool {
	address, ok := listener.Addr().(*net.TCPAddr)
	return ok && address.IP != nil && address.IP.To4() != nil && address.IP.Equal(net.ParseIP("127.0.0.1"))
}

type targetState struct {
	listener    net.Listener
	config      TargetConfig
	mu          sync.Mutex
	connections map[net.Conn]struct{}
	semaphore   chan struct{}
	workers     sync.WaitGroup
	failures    chan error
	attempted   atomic.Int64
	open        atomic.Int64
	verified    atomic.Int64
	closed      atomic.Int64
	emitted     atomic.Int64
}

func incrementReported(counter *atomic.Int64) int64 {
	for {
		current := counter.Load()
		if current >= maxReportedCount {
			return maxReportedCount
		}
		if counter.CompareAndSwap(current, current+1) {
			return current + 1
		}
	}
}

func (state *targetState) recordAttempt() int64 { return incrementReported(&state.attempted) }

func (state *targetState) reserveEvent() bool {
	for {
		current := state.emitted.Load()
		if current >= maximumTargetEvents {
			return false
		}
		if state.emitted.CompareAndSwap(current, current+1) {
			return true
		}
	}
}

func (state *targetState) handle(ctx context.Context, raw net.Conn) {
	defer state.workers.Done()
	finalFailure := FailureNone
	emitClose := true
	defer func() {
		state.mu.Lock()
		delete(state.connections, raw)
		state.mu.Unlock()
		<-state.semaphore
		_ = raw.Close()
		incrementReported(&state.closed)
		if emitClose {
			if err := state.emit(finalFailure); err != nil {
				state.reportFatal(err)
			}
		}
	}()
	connection := tls.Server(raw, state.config.TLSConfig.Clone())
	var emitterFailure error
	err := handleTargetProtocolObserved(
		ctx, connection, state.config.Token, nil, state.config.ConnectionTimeout, defaultExtraDataProbe,
		state.heartbeatTimeout(),
		func() error {
			incrementReported(&state.verified)
			emitterFailure = state.emit(FailureNone)
			return emitterFailure
		},
	)
	if emitterFailure != nil {
		emitClose = false
		state.reportFatal(emitterFailure)
		return
	}
	if err == nil {
		return
	}
	fixed := fixedCategorized(err)
	category := FailureInternal
	var categorized CategorizedError
	if errors.As(fixed, &categorized) {
		category = categorized.Category
	}
	finalFailure = category
}

func (state *targetState) reportFatal(err error) {
	if err == nil {
		return
	}
	failure := CategorizedError{Category: FailureInternal, cause: err}
	select {
	case state.failures <- failure:
		_ = state.listener.Close()
	default:
	}
}

func (state *targetState) heartbeatTimeout() time.Duration {
	if state.config.HeartbeatTimeout != 0 {
		return state.config.HeartbeatTimeout
	}
	return defaultTargetHeartbeatTimeout
}

func (state *targetState) emit(failure FailureCategory) error {
	if !state.reserveEvent() {
		return nil
	}
	return state.config.Emitter.Emit(Event{
		Phase: PhaseTarget, Attempted: int(state.attempted.Load()), Open: int(state.open.Load()),
		Verified: int(state.verified.Load()), Closed: int(state.closed.Load()), Failure: failure,
	})
}

func (state *targetState) cleanup(ctx context.Context) error {
	state.mu.Lock()
	connections := make([]net.Conn, 0, len(state.connections))
	for connection := range state.connections {
		connections = append(connections, connection)
	}
	state.mu.Unlock()
	for index := len(connections) - 1; index >= 0; index-- {
		_ = connections[index].Close()
	}
	done := make(chan struct{})
	go func() {
		state.workers.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func fixedCategorized(err error) error {
	var categorized CategorizedError
	if errors.As(err, &categorized) && validFailure(categorized.Category) {
		return CategorizedError{Category: categorized.Category}
	}
	return CategorizedError{Category: FailureInternal}
}
