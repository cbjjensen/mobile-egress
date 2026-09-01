package adminservice

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
)

var errSupervisorTerminated = errors.New("relay supervisor is terminally shut down")

// RelayInstance is the existing relay data plane opened against one state
// directory. The supervisor owns it from Open through Close.
type RelayInstance interface {
	Handler() http.Handler
	TLSConfig() *tls.Config
	Close() error
}

type SupervisorConfig struct {
	StateDir string
	Address  string
	Listen   func(network, address string) (net.Listener, error)
	Open     func(string) (RelayInstance, error)
}

type SupervisorStatus struct {
	RelayRunning bool
}

type Supervisor struct {
	stateDir string
	address  string
	listen   func(string, string) (net.Listener, error)
	open     func(string) (RelayInstance, error)

	lifecycle    chan struct{}
	mu           sync.RWMutex
	running      bool
	terminal     bool
	terminalDone chan struct{}
	current      *supervisedRelay
}

type supervisedRelay struct {
	server   *http.Server
	listener net.Listener
	instance RelayInstance
	handler  *trackedHandler
	done     chan struct{}

	mu       sync.Mutex
	serveErr error
	closeErr error
}

// trackedHandler makes the relay instance lifetime independent of net/http's
// connection bookkeeping. Server.Close intentionally does not wait for active
// handlers, so the instance cannot be closed safely until every admitted call
// to the underlying relay handler has returned.
type trackedHandler struct {
	target http.Handler

	mu     sync.Mutex
	sealed bool
	active int
	idle   chan struct{}
}

func newTrackedHandler(target http.Handler) *trackedHandler {
	idle := make(chan struct{})
	close(idle)
	return &trackedHandler{target: target, idle: idle}
}

func (handler *trackedHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	handler.mu.Lock()
	if handler.sealed {
		handler.mu.Unlock()
		http.Error(writer, "relay unavailable", http.StatusServiceUnavailable)
		return
	}
	if handler.active == 0 {
		handler.idle = make(chan struct{})
	}
	handler.active++
	handler.mu.Unlock()

	defer func() {
		handler.mu.Lock()
		handler.active--
		if handler.active == 0 {
			close(handler.idle)
		}
		handler.mu.Unlock()
	}()
	handler.target.ServeHTTP(writer, request)
}

func (handler *trackedHandler) seal() {
	handler.mu.Lock()
	handler.sealed = true
	handler.mu.Unlock()
}

func (handler *trackedHandler) wait(ctx context.Context) error {
	handler.mu.Lock()
	idle := handler.idle
	handler.mu.Unlock()
	select {
	case <-idle:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func NewSupervisor(config SupervisorConfig) (*Supervisor, error) {
	stateDir := strings.TrimSpace(config.StateDir)
	if stateDir == "" || config.Listen == nil || config.Open == nil || !loopbackAddress(config.Address) {
		return nil, errors.New("invalid relay supervisor configuration")
	}
	supervisor := &Supervisor{
		stateDir:     stateDir,
		address:      config.Address,
		listen:       config.Listen,
		open:         config.Open,
		lifecycle:    make(chan struct{}, 1),
		terminalDone: make(chan struct{}),
	}
	supervisor.lifecycle <- struct{}{}
	return supervisor, nil
}

func loopbackAddress(address string) bool {
	host, port, err := net.SplitHostPort(address)
	if err != nil || port == "" {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (supervisor *Supervisor) Reconcile(ctx context.Context) error {
	if supervisor == nil {
		return errors.New("relay supervisor unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if supervisor.isTerminal() {
		return errSupervisorTerminated
	}

	if err := supervisor.acquireReconcileLifecycle(ctx); err != nil {
		return err
	}
	defer supervisor.releaseLifecycle()
	if err := ctx.Err(); err != nil {
		return err
	}
	if supervisor.isTerminal() {
		return errSupervisorTerminated
	}
	if current := supervisor.currentRun(); current != nil {
		if supervisor.Snapshot().RelayRunning {
			return nil
		}
		select {
		case <-current.done:
		case <-ctx.Done():
			return ctx.Err()
		case <-supervisor.terminalDone:
			return errSupervisorTerminated
		}
		serveErr, closeErr := current.errors()
		if closeErr != nil {
			return errors.Join(serveErr, closeErr)
		}
		supervisor.clearRun(current)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if supervisor.isTerminal() {
		return errSupervisorTerminated
	}

	listener, err := supervisor.listen("tcp", supervisor.address)
	if err != nil {
		supervisor.setRunning(false)
		return err
	}
	if listener == nil {
		supervisor.setRunning(false)
		return errors.New("relay listener unavailable")
	}
	if !loopbackListener(listener) {
		closeErr := listener.Close()
		supervisor.setRunning(false)
		return errors.Join(errors.New("relay listener is not loopback"), closeErr)
	}
	if supervisor.isTerminal() {
		closeErr := listener.Close()
		return errors.Join(errSupervisorTerminated, closeErr)
	}
	if err := ctx.Err(); err != nil {
		_ = listener.Close()
		supervisor.setRunning(false)
		return err
	}

	instance, openErr := supervisor.open(supervisor.stateDir)
	if openErr != nil || instance == nil {
		var closeErr error
		if instance != nil {
			closeErr = instance.Close()
		}
		listenerErr := listener.Close()
		supervisor.setRunning(false)
		if openErr == nil {
			openErr = errors.New("relay service unavailable")
		}
		return errors.Join(openErr, closeErr, listenerErr)
	}
	if supervisor.isTerminal() {
		listenerErr := listener.Close()
		closeErr := instance.Close()
		return errors.Join(errSupervisorTerminated, listenerErr, closeErr)
	}
	handler := instance.Handler()
	tlsConfig := instance.TLSConfig()
	if handler == nil || tlsConfig == nil {
		listenerErr := listener.Close()
		closeErr := instance.Close()
		supervisor.setRunning(false)
		return errors.Join(errors.New("invalid relay service configuration"), listenerErr, closeErr)
	}
	if err := ctx.Err(); err != nil {
		listenerErr := listener.Close()
		closeErr := instance.Close()
		supervisor.setRunning(false)
		return errors.Join(err, listenerErr, closeErr)
	}

	tlsListener := tls.NewListener(listener, tlsConfig.Clone())
	tracked := newTrackedHandler(handler)
	run := &supervisedRelay{
		server:   &http.Server{Handler: tracked},
		listener: tlsListener,
		instance: instance,
		handler:  tracked,
		done:     make(chan struct{}),
	}
	supervisor.mu.Lock()
	if supervisor.terminal {
		supervisor.mu.Unlock()
		listenerErr := tlsListener.Close()
		closeErr := instance.Close()
		return errors.Join(errSupervisorTerminated, listenerErr, closeErr)
	}
	supervisor.current = run
	supervisor.running = true
	supervisor.mu.Unlock()
	go supervisor.serve(run)
	return nil
}

func loopbackListener(listener net.Listener) bool {
	if listener == nil {
		return false
	}
	address, ok := listener.Addr().(*net.TCPAddr)
	return ok && address.IP != nil && address.IP.IsLoopback()
}

func (supervisor *Supervisor) serve(run *supervisedRelay) {
	err := run.server.Serve(run.listener)
	expectedClose := errors.Is(err, http.ErrServerClosed)
	if expectedClose {
		err = nil
	}
	run.handler.seal()
	supervisor.mu.Lock()
	if supervisor.current == run {
		supervisor.running = false
	}
	supervisor.mu.Unlock()
	var serverCloseErr error
	if !expectedClose {
		serverCloseErr = run.server.Close()
	}
	// Serve and Server.Close may both return while ServeHTTP is still running.
	// The sealed wrapper is the authoritative drain boundary for the store.
	drainErr := run.handler.wait(context.Background())
	closeErr := run.instance.Close()
	run.mu.Lock()
	run.serveErr = errors.Join(err, serverCloseErr, drainErr)
	run.closeErr = closeErr
	run.mu.Unlock()
	close(run.done)
}

func (supervisor *Supervisor) Stop(ctx context.Context) error {
	if supervisor == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := supervisor.acquireLifecycle(ctx); err != nil {
		return err
	}
	defer supervisor.releaseLifecycle()
	return supervisor.stopLocked(ctx)
}

// shutdown permanently disables Reconcile before waiting for any in-progress
// lifecycle operation. Daemon termination uses this; rotation uses Stop so a
// completed mutation can reconcile the replacement listener.
func (supervisor *Supervisor) shutdown(ctx context.Context) error {
	if supervisor == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	supervisor.terminate()
	if err := supervisor.acquireLifecycle(ctx); err != nil {
		return err
	}
	defer supervisor.releaseLifecycle()
	return supervisor.stopLocked(ctx)
}

func (supervisor *Supervisor) acquireLifecycle(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-supervisor.lifecycle:
	}
	if err := ctx.Err(); err != nil {
		supervisor.releaseLifecycle()
		return err
	}
	return nil
}

func (supervisor *Supervisor) acquireReconcileLifecycle(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-supervisor.terminalDone:
		return errSupervisorTerminated
	case <-supervisor.lifecycle:
	}
	if err := ctx.Err(); err != nil {
		supervisor.releaseLifecycle()
		return err
	}
	if supervisor.isTerminal() {
		supervisor.releaseLifecycle()
		return errSupervisorTerminated
	}
	return nil
}

func (supervisor *Supervisor) releaseLifecycle() {
	supervisor.lifecycle <- struct{}{}
}

func (supervisor *Supervisor) terminate() {
	if supervisor == nil {
		return
	}
	supervisor.mu.Lock()
	wasTerminal := supervisor.terminal
	supervisor.terminal = true
	supervisor.running = false
	if supervisor.current != nil {
		// Terminalization precedes the daemon's potentially long admin drain.
		// Seal synchronously so no target relay work can begin after this call
		// returns, while already-admitted handlers retain the instance lifetime.
		supervisor.current.handler.seal()
	}
	if !wasTerminal {
		close(supervisor.terminalDone)
	}
	supervisor.mu.Unlock()
}

func (supervisor *Supervisor) stopLocked(ctx context.Context) error {
	run := supervisor.currentRun()
	if run == nil {
		supervisor.setRunning(false)
		return nil
	}
	select {
	case <-run.done:
		return supervisor.retireStoppedRun(run)
	default:
	}

	// Publish stopped before shutdown begins so status can never advertise a
	// listener whose handlers are draining.
	supervisor.setRunning(false)
	run.handler.seal()
	shutdownErr := run.server.Shutdown(ctx)
	var forceCloseErr error
	if shutdownErr != nil {
		forceCloseErr = run.server.Close()
	}
	select {
	case <-run.done:
	case <-ctx.Done():
		return errors.Join(shutdownErr, forceCloseErr, ctx.Err())
	}
	return errors.Join(shutdownErr, forceCloseErr, supervisor.retireStoppedRun(run))
}

func (supervisor *Supervisor) retireStoppedRun(run *supervisedRelay) error {
	serveErr, closeErr := run.errors()
	if closeErr == nil {
		supervisor.clearRun(run)
	}
	return errors.Join(serveErr, closeErr)
}

func (run *supervisedRelay) errors() (error, error) {
	run.mu.Lock()
	defer run.mu.Unlock()
	return run.serveErr, run.closeErr
}

func (supervisor *Supervisor) Snapshot() SupervisorStatus {
	if supervisor == nil {
		return SupervisorStatus{}
	}
	supervisor.mu.RLock()
	status := SupervisorStatus{RelayRunning: supervisor.running}
	supervisor.mu.RUnlock()
	return status
}

func (supervisor *Supervisor) currentRun() *supervisedRelay {
	supervisor.mu.RLock()
	current := supervisor.current
	supervisor.mu.RUnlock()
	return current
}

func (supervisor *Supervisor) setRunning(running bool) {
	supervisor.mu.Lock()
	supervisor.running = running && !supervisor.terminal
	supervisor.mu.Unlock()
}

func (supervisor *Supervisor) isTerminal() bool {
	if supervisor == nil {
		return true
	}
	supervisor.mu.RLock()
	terminal := supervisor.terminal
	supervisor.mu.RUnlock()
	return terminal
}

func (supervisor *Supervisor) clearRun(run *supervisedRelay) {
	supervisor.mu.Lock()
	if supervisor.current == run {
		supervisor.current = nil
		supervisor.running = false
	}
	supervisor.mu.Unlock()
}
