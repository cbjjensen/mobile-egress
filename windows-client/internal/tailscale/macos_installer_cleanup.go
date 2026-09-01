package tailscale

import (
	"context"
	"errors"
	"sync"
	"time"
)

type installerTerminalEvidence uint8

const (
	installerTerminalUnknown installerTerminalEvidence = iota
	installerTerminalExact
	installerTerminalInvalid
)

type installerCleanupManager struct {
	mu         sync.Mutex
	generation uint64
	active     *installerCleanupLease

	cleanupLimit time.Duration
	retryAfter   func(context.Context, time.Duration) bool
}

type installerCleanupLease struct {
	manager    *installerCleanupManager
	generation uint64

	stopGate       chan struct{}
	mu             sync.Mutex
	stage          *stagedMacPKG
	session        installerSession
	stageBound     bool
	sessionBound   bool
	released       bool
	evidence       installerTerminalEvidence
	terminal       installerWaitResult
	managedStarted bool
}

func newInstallerCleanupManager() *installerCleanupManager {
	return &installerCleanupManager{
		cleanupLimit: installerCleanupLimit,
		retryAfter:   waitInstallerCleanupRetry,
	}
}

func (manager *installerCleanupManager) Acquire() (*installerCleanupLease, error) {
	if manager == nil {
		return nil, errMacCleanupPending
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.active != nil {
		return nil, errDarwinInstallerBusy
	}
	manager.generation++
	lease := &installerCleanupLease{
		manager: manager, generation: manager.generation,
		stopGate: make(chan struct{}, 1),
	}
	manager.active = lease
	return lease, nil
}

func (lease *installerCleanupLease) BindStage(stage *stagedMacPKG) error {
	if lease == nil || stage == nil {
		return errMacCleanupPending
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.released || lease.stageBound {
		return errMacCleanupPending
	}
	lease.stage = stage
	lease.stageBound = true
	return nil
}

func (lease *installerCleanupLease) BindSession(session installerSession) error {
	if lease == nil || session == nil {
		return errMacCleanupPending
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.released || !lease.stageBound || lease.stage == nil || lease.sessionBound {
		return errMacCleanupPending
	}
	lease.session = session
	lease.sessionBound = true
	return nil
}

func (lease *installerCleanupLease) latchTerminal(result installerWaitResult, ok bool) {
	if lease == nil {
		return
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.released || lease.evidence == installerTerminalInvalid {
		return
	}
	if !ok || !validInstallerWaitResult(result) {
		lease.evidence = installerTerminalInvalid
		return
	}
	if lease.evidence == installerTerminalExact && lease.terminal != result {
		lease.evidence = installerTerminalInvalid
		return
	}
	lease.evidence = installerTerminalExact
	lease.terminal = result
}

func (lease *installerCleanupLease) stop(ctx context.Context) (installerStopResult, error) {
	if lease == nil || ctx == nil {
		return installerStopResult{}, errMacCleanupPending
	}
	if err := ctx.Err(); err != nil {
		return installerStopResult{}, err
	}
	lease.mu.Lock()
	if lease.released || !lease.sessionBound || lease.session == nil {
		lease.mu.Unlock()
		return installerStopResult{}, errMacCleanupPending
	}
	session := lease.session
	lease.mu.Unlock()
	select {
	case lease.stopGate <- struct{}{}:
		defer func() { <-lease.stopGate }()
	case <-ctx.Done():
		return installerStopResult{}, ctx.Err()
	}
	return session.Stop(ctx)
}

func (lease *installerCleanupLease) ReleaseBeforeDispatch() error {
	if lease == nil {
		return errMacCleanupPending
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.released || lease.sessionBound {
		return errMacCleanupPending
	}
	if lease.stageBound {
		if lease.stage == nil || lease.stage.RemoveAfterQuiescence() != nil {
			return errMacCleanupPending
		}
	}
	return lease.releaseLocked()
}

func (lease *installerCleanupLease) ReleaseAfterNaturalQuiescence(result installerStopResult) error {
	if lease == nil {
		return errMacCleanupPending
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.released || !lease.stageBound || lease.stage == nil || !lease.sessionBound || lease.session == nil ||
		!result.Quiescent || result.Terminal != installerTerminalNaturalZero || !lease.evidenceAllowsNaturalZeroLocked() {
		return errMacCleanupPending
	}
	if lease.stage.RemoveAfterQuiescence() != nil {
		return errMacCleanupPending
	}
	return lease.releaseLocked()
}

func (lease *installerCleanupLease) evidenceAllowsNaturalZeroLocked() bool {
	switch lease.evidence {
	case installerTerminalUnknown:
		return true
	case installerTerminalExact:
		return lease.terminal == (installerWaitResult{Reason: installerTerminalNaturalZero, ExitCode: 0})
	default:
		return false
	}
}

func (lease *installerCleanupLease) releaseLocked() error {
	manager := lease.manager
	if manager == nil {
		return errMacCleanupPending
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.active != lease || manager.generation != lease.generation {
		return errMacCleanupPending
	}
	manager.active = nil
	lease.released = true
	return nil
}

func (lease *installerCleanupLease) ContinueManagedCleanup() error {
	if lease == nil {
		return errMacCleanupPending
	}
	lease.mu.Lock()
	if lease.released {
		lease.mu.Unlock()
		return nil
	}
	if lease.managedStarted {
		lease.mu.Unlock()
		return nil
	}
	lease.managedStarted = true
	manager := lease.manager
	lease.mu.Unlock()
	if manager == nil {
		return errMacCleanupPending
	}
	go lease.runManagedCleanup(manager)
	return nil
}

func (lease *installerCleanupLease) runManagedCleanup(manager *installerCleanupManager) {
	for {
		lease.mu.Lock()
		if lease.released || !lease.sessionBound || lease.session == nil || lease.evidence == installerTerminalInvalid ||
			(lease.evidence == installerTerminalExact && lease.terminal.Reason != installerTerminalNaturalZero) {
			lease.mu.Unlock()
			return
		}
		limit := manager.cleanupLimit
		if limit <= 0 || limit > installerCleanupLimit {
			limit = installerCleanupLimit
		}
		lease.mu.Unlock()

		ctx, cancel := context.WithTimeout(context.Background(), limit)
		result, err := lease.stop(ctx)
		cancel()
		if err == nil {
			if releaseErr := lease.ReleaseAfterNaturalQuiescence(result); releaseErr == nil {
				return
			}
			if !result.Quiescent || result.Terminal != installerTerminalNaturalZero {
				return
			}
			return
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			return
		}
		if !manager.waitForRetry(context.Background(), installerCleanupRetryInterval) {
			return
		}
	}
}

func (manager *installerCleanupManager) waitForRetry(ctx context.Context, delay time.Duration) bool {
	if manager == nil || manager.retryAfter == nil {
		return false
	}
	return manager.retryAfter(ctx, delay)
}

func waitInstallerCleanupRetry(ctx context.Context, delay time.Duration) bool {
	if ctx == nil || delay <= 0 {
		return false
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
