package adminservice

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"unicode/utf8"

	"mobile-egress/internal/relayadmin"
	"mobile-egress/relay/internal/service"
)

const DarwinRelayStateDir = "/Library/Application Support/ZFNF Mobile Egress/Relay"

var errRuntimeNonQuiescent = errors.New("relay admin runtime is not quiescent")

type PreparedPathGuard interface {
	service.AdminPathGuard
	Prepare(context.Context) error
}

type RuntimeConfig struct {
	AdminListener  net.Listener
	Peer           PeerExtractor
	AdminGID       uint32
	HelperVersion  string
	StateDir       string
	RelayAddress   string
	PathGuard      PreparedPathGuard
	MaxConnections int
	ListenRelay    func(string, string) (net.Listener, error)
	OpenRelay      func(string) (RelayInstance, error)
}

type runtimeDaemon interface {
	Run(context.Context) (RunResult, error)
}

type runtimeDependencies struct {
	newSupervisor       func(SupervisorConfig) (*Supervisor, error)
	newMutationFinished func(func() *service.AdminState, *Supervisor) (func(relayadmin.ReplayKey), error)
	openState           func(service.AdminStateOptions) (*service.AdminState, error)
	newHandler          func(HandlerConfig) (*Handler, error)
	newDaemon           func(DaemonConfig) (runtimeDaemon, error)
	closeState          func(*service.AdminState) error
}

type Runtime struct {
	state      *service.AdminState
	supervisor *Supervisor
	daemon     runtimeDaemon
	closeState func(*service.AdminState) error

	mu             sync.Mutex
	runStarted     bool
	runActive      bool
	runFinished    bool
	runResult      RunResult
	closeAttempted bool
	closeErr       error
}

func NewRuntime(config RuntimeConfig) (*Runtime, error) {
	return newRuntime(config, productionRuntimeDependencies())
}

func (runtime *Runtime) Run(ctx context.Context) (RunResult, error) {
	if runtime == nil || runtime.state == nil || runtime.supervisor == nil || runtime.daemon == nil {
		return RunResult{}, errors.New("relay admin runtime unavailable")
	}
	runtime.mu.Lock()
	if runtime.closeAttempted {
		runtime.mu.Unlock()
		return RunResult{}, errors.New("relay admin runtime is closed")
	}
	if runtime.runStarted {
		runtime.mu.Unlock()
		return RunResult{}, errors.New("relay admin runtime already started")
	}
	runtime.runStarted = true
	runtime.runActive = true
	runtime.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	snapshot, err := runtime.state.Snapshot(ctx)
	if err == nil && normalizeAdminSnapshot(snapshot).Class == service.AdminStateReady {
		_ = runtime.supervisor.Reconcile(ctx)
	}
	result, runErr := runtime.daemon.Run(ctx)
	runtime.mu.Lock()
	runtime.runActive = false
	runtime.runFinished = true
	runtime.runResult = result
	runtime.mu.Unlock()
	return result, runErr
}

func (runtime *Runtime) Close() error {
	if runtime == nil || runtime.state == nil || runtime.closeState == nil {
		return errors.New("relay admin runtime unavailable")
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.runActive || runtime.runFinished && !runtime.runResult.Quiescent {
		return errRuntimeNonQuiescent
	}
	if runtime.closeAttempted {
		return runtime.closeErr
	}
	runtime.closeAttempted = true
	runtime.closeErr = runtime.closeState(runtime.state)
	return runtime.closeErr
}

func newRuntime(config RuntimeConfig, dependencies runtimeDependencies) (*Runtime, error) {
	if err := validateRuntimeConfiguration(config, dependencies); err != nil {
		return nil, err
	}
	if err := config.PathGuard.Prepare(context.Background()); err != nil {
		return nil, err
	}
	supervisor, err := dependencies.newSupervisor(SupervisorConfig{
		StateDir: config.StateDir, Address: config.RelayAddress,
		Listen: config.ListenRelay, Open: config.OpenRelay,
	})
	if err != nil {
		return nil, err
	}
	var state *service.AdminState
	mutationFinished, err := dependencies.newMutationFinished(func() *service.AdminState { return state }, supervisor)
	if err != nil {
		return nil, err
	}
	state, err = dependencies.openState(service.AdminStateOptions{
		StateDir: config.StateDir, PathGuard: config.PathGuard,
		MutationCapacity: relayadmin.MutationReplayCapacity, MutationFinished: mutationFinished,
	})
	if err != nil {
		return nil, err
	}
	handler, err := dependencies.newHandler(HandlerConfig{
		State: state, Supervisor: supervisor, AdminGID: config.AdminGID, HelperVersion: config.HelperVersion,
	})
	if err != nil {
		return nil, errors.Join(err, dependencies.closeState(state))
	}
	server := &relayadmin.Server{
		Authorize:      handler.Authorize,
		Handler:        handler,
		Replay:         state.ReplayStore(),
		OperationLimit: relayadmin.OperationTimeout,
		IOLimit:        relayadmin.OperationTimeout,
	}
	daemon, err := dependencies.newDaemon(DaemonConfig{
		Listener: config.AdminListener, Peer: config.Peer, Server: server,
		Supervisor: supervisor, MaxConnections: config.MaxConnections,
	})
	if err != nil {
		return nil, errors.Join(err, dependencies.closeState(state))
	}
	return &Runtime{state: state, supervisor: supervisor, daemon: daemon, closeState: dependencies.closeState}, nil
}

func validateRuntimeConfiguration(config RuntimeConfig, dependencies runtimeDependencies) error {
	if config.AdminListener == nil || config.Peer == nil || config.AdminGID == 0 ||
		strings.TrimSpace(config.HelperVersion) == "" || !utf8.ValidString(config.HelperVersion) ||
		strings.TrimSpace(config.StateDir) == "" || !loopbackAddress(config.RelayAddress) ||
		config.PathGuard == nil || config.MaxConnections <= 0 || config.ListenRelay == nil || config.OpenRelay == nil ||
		dependencies.newSupervisor == nil || dependencies.newMutationFinished == nil || dependencies.openState == nil ||
		dependencies.newHandler == nil || dependencies.newDaemon == nil || dependencies.closeState == nil {
		return errors.New("invalid relay admin runtime configuration")
	}
	return nil
}

func productionRuntimeDependencies() runtimeDependencies {
	return runtimeDependencies{
		newSupervisor:       NewSupervisor,
		newMutationFinished: newMutationFinishedCallback,
		openState:           service.OpenAdminState,
		newHandler:          NewHandler,
		newDaemon: func(config DaemonConfig) (runtimeDaemon, error) {
			return NewDaemon(config)
		},
		closeState: func(state *service.AdminState) error { return state.Close() },
	}
}
