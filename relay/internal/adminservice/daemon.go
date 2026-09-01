package adminservice

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"mobile-egress/internal/relayadmin"
)

type PeerExtractor func(net.Conn) (relayadmin.Peer, error)

type DaemonConfig struct {
	Listener       net.Listener
	Peer           PeerExtractor
	Server         *relayadmin.Server
	Supervisor     *Supervisor
	MaxConnections int
}

type RunResult struct {
	RestartRequested bool
}

type Daemon struct {
	listener       net.Listener
	peer           PeerExtractor
	server         *relayadmin.Server
	supervisor     *Supervisor
	maxConnections int
	drainLimit     time.Duration

	runMu   sync.Mutex
	started bool
}

func NewDaemon(config DaemonConfig) (*Daemon, error) {
	if config.Listener == nil || config.Peer == nil || config.Server == nil ||
		config.Supervisor == nil || config.MaxConnections <= 0 {
		return nil, errors.New("invalid relay admin daemon configuration")
	}
	return &Daemon{
		listener: config.Listener, peer: config.Peer, server: config.Server,
		supervisor: config.Supervisor, maxConnections: config.MaxConnections,
		drainLimit: relayadmin.OperationTimeout,
	}, nil
}

func (daemon *Daemon) Run(parent context.Context) (RunResult, error) {
	if daemon == nil {
		return RunResult{}, errors.New("relay admin daemon unavailable")
	}
	daemon.runMu.Lock()
	if daemon.started {
		daemon.runMu.Unlock()
		return RunResult{}, errors.New("relay admin daemon already started")
	}
	daemon.started = true
	daemon.runMu.Unlock()
	if parent == nil {
		parent = context.Background()
	}
	serveContext, cancelServe := context.WithCancel(parent)
	defer cancelServe()

	exitRequested := make(chan struct{})
	restartRequested := make(chan struct{})
	var exitOnce sync.Once
	var restartOnce sync.Once
	var listenerCloseErr error
	requestExit := func() {
		exitOnce.Do(func() {
			// Disable every late mutation-finished Reconcile before interrupting
			// ServeConn workers or beginning any drain.
			daemon.supervisor.terminate()
			cancelServe()
			close(exitRequested)
			listenerCloseErr = daemon.listener.Close()
		})
	}
	requestRestart := func() {
		restartOnce.Do(func() {
			close(restartRequested)
			requestExit()
		})
	}

	watcherStop := make(chan struct{})
	watcherDone := make(chan struct{})
	go func() {
		defer close(watcherDone)
		select {
		case <-parent.Done():
			requestExit()
		case <-watcherStop:
		}
	}()

	semaphore := make(chan struct{}, daemon.maxConnections)
	var workers sync.WaitGroup
	var acceptErr error
acceptLoop:
	for {
		connection, err := daemon.listener.Accept()
		if err != nil {
			if parent.Err() == nil && !channelClosed(restartRequested) {
				acceptErr = err
			}
			requestExit()
			break
		}
		select {
		case <-exitRequested:
			_ = connection.Close()
			break acceptLoop
		default:
		}
		select {
		case semaphore <- struct{}{}:
			workers.Add(1)
			go func(connection net.Conn) {
				defer workers.Done()
				defer func() { <-semaphore }()
				peer, err := daemon.peer(connection)
				if err != nil {
					_ = connection.Close()
					return
				}
				peer = relayadmin.NewPeer(peer.UID(), peer.Groups())
				outcome := daemon.server.ServeConn(serveContext, connection, peer)
				if outcome.RepairRestartReady {
					requestRestart()
				}
			}(connection)
		default:
			_ = connection.Close()
		}
	}

	close(watcherStop)
	<-watcherDone
	requestExit()
	workers.Wait()

	limit := daemon.drainLimit
	if limit <= 0 || limit > relayadmin.OperationTimeout {
		limit = relayadmin.OperationTimeout
	}
	drainContext, cancelDrain := context.WithTimeout(context.Background(), limit)
	drainErr := daemon.server.Drain(drainContext)
	cancelDrain()
	stopContext, cancelStop := context.WithTimeout(context.Background(), limit)
	stopErr := daemon.supervisor.Stop(stopContext)
	cancelStop()

	if listenerCloseErr != nil && !errors.Is(listenerCloseErr, net.ErrClosed) {
		listenerCloseErr = fmt.Errorf("close relay admin listener: %w", listenerCloseErr)
	} else {
		listenerCloseErr = nil
	}
	if acceptErr != nil && errors.Is(acceptErr, net.ErrClosed) && (parent.Err() != nil || channelClosed(restartRequested)) {
		acceptErr = nil
	}
	if acceptErr != nil {
		acceptErr = fmt.Errorf("accept relay admin connection: %w", acceptErr)
	}
	if drainErr != nil {
		drainErr = fmt.Errorf("drain relay admin dispatch: %w", drainErr)
	}
	if stopErr != nil {
		stopErr = fmt.Errorf("stop relay supervisor: %w", stopErr)
	}
	joined := errors.Join(acceptErr, listenerCloseErr, drainErr, stopErr)
	if joined != nil {
		return RunResult{}, joined
	}
	return RunResult{RestartRequested: channelClosed(restartRequested)}, nil
}

func channelClosed(channel <-chan struct{}) bool {
	select {
	case <-channel:
		return true
	default:
		return false
	}
}
