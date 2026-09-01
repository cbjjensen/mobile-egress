package tailscale

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestInstallerCleanupLeaseIsExclusiveAndRemovesOnlyAfterNaturalQuiescence(t *testing.T) {
	manager := newInstallerCleanupManager()
	lease, err := manager.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Acquire(); !errors.Is(err, errDarwinInstallerBusy) {
		t.Fatalf("second Acquire() = %v, want busy", err)
	}

	stage, operations := newModelStagedPackage(t)
	session := newCleanupTestSession(installerWaitResult{Reason: installerTerminalNaturalZero, ExitCode: 0})
	if err := lease.BindStage(stage); err != nil {
		t.Fatal(err)
	}
	if err := lease.BindSession(session); err != nil {
		t.Fatal(err)
	}
	lease.latchTerminal(installerWaitResult{Reason: installerTerminalNaturalZero, ExitCode: 0}, true)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	stop, err := lease.stop(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.ReleaseAfterNaturalQuiescence(stop); err != nil {
		t.Fatal(err)
	}
	if operations.removeFileCalls != 1 || operations.removeDirectoryCalls != 1 {
		t.Fatalf("removal calls = %d/%d, want 1/1", operations.removeFileCalls, operations.removeDirectoryCalls)
	}
	if _, err := manager.Acquire(); err != nil {
		t.Fatalf("Acquire() after quiescent cleanup: %v", err)
	}
}

func TestInstallerCleanupLeaseRejectsSessionBeforeStage(t *testing.T) {
	manager := newInstallerCleanupManager()
	lease, err := manager.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.BindSession(newCleanupTestSession(installerWaitResult{Reason: installerTerminalNaturalZero, ExitCode: 0})); !errors.Is(err, errMacCleanupPending) {
		t.Fatalf("BindSession() before stage = %v", err)
	}
	if err := lease.ReleaseBeforeDispatch(); err != nil {
		t.Fatal(err)
	}
}

func TestInstallerCleanupLeaseRejectsContradictoryWaitEvidence(t *testing.T) {
	manager := newInstallerCleanupManager()
	lease, err := manager.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	stage, operations := newModelStagedPackage(t)
	if err := lease.BindStage(stage); err != nil {
		t.Fatal(err)
	}
	if err := lease.BindSession(newCleanupTestSession(installerWaitResult{Reason: installerTerminalNaturalZero, ExitCode: 0})); err != nil {
		t.Fatal(err)
	}
	lease.latchTerminal(installerWaitResult{Reason: installerTerminalNaturalNonzero, ExitCode: 7}, true)

	if err := lease.ReleaseAfterNaturalQuiescence(installerStopResult{Quiescent: true, Terminal: installerTerminalNaturalZero}); !errors.Is(err, errMacCleanupPending) {
		t.Fatalf("ReleaseAfterNaturalQuiescence() = %v", err)
	}
	if operations.removeFileCalls != 0 || operations.removeDirectoryCalls != 0 {
		t.Fatalf("ambiguous evidence removed stage: %d/%d", operations.removeFileCalls, operations.removeDirectoryCalls)
	}
	if _, err := manager.Acquire(); !errors.Is(err, errDarwinInstallerBusy) {
		t.Fatalf("retained lease Acquire() = %v", err)
	}
}

func TestInstallerSessionStopCanBeJoinedWithoutConsumingDone(t *testing.T) {
	result := installerWaitResult{Reason: installerTerminalNaturalZero, ExitCode: 0}
	session := newCleanupTestSession(result)

	const callers = 16
	var wait sync.WaitGroup
	wait.Add(callers)
	results := make(chan installerStopResult, callers)
	for index := 0; index < callers; index++ {
		go func() {
			defer wait.Done()
			stop, err := session.Stop(context.Background())
			if err != nil {
				t.Errorf("Stop(): %v", err)
				return
			}
			results <- stop
		}()
	}
	wait.Wait()
	close(results)
	for stop := range results {
		if stop != (installerStopResult{Quiescent: true, Terminal: installerTerminalNaturalZero}) {
			t.Fatalf("Stop() = %#v", stop)
		}
	}
	if got, ok := <-session.Done(); !ok || got != result {
		t.Fatalf("Done() = %#v/%t", got, ok)
	}
}

func TestInstallerCleanupStopDoesNotHoldLeaseLockWhileWaiting(t *testing.T) {
	manager := newInstallerCleanupManager()
	lease, err := manager.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	session := &controlledInstallerSession{
		done: make(chan installerWaitResult),
		stop: func(context.Context) (installerStopResult, error) {
			close(entered)
			<-release
			return installerStopResult{Quiescent: true, Terminal: installerTerminalNaturalZero}, nil
		},
	}
	stage, _ := newModelStagedPackage(t)
	if err := lease.BindStage(stage); err != nil {
		t.Fatal(err)
	}
	if err := lease.BindSession(session); err != nil {
		t.Fatal(err)
	}
	stopped := make(chan struct{})
	go func() {
		_, _ = lease.stop(context.Background())
		close(stopped)
	}()
	<-entered
	latched := make(chan struct{})
	go func() {
		lease.latchTerminal(installerWaitResult{Reason: installerTerminalNaturalZero, ExitCode: 0}, true)
		close(latched)
	}()
	select {
	case <-latched:
	case <-time.After(250 * time.Millisecond):
		close(release)
		<-stopped
		t.Fatal("terminal latch blocked behind session Stop")
	}
	close(release)
	<-stopped
}

func TestInstallerCleanupLeaseSerializesSessionStopCalls(t *testing.T) {
	manager := newInstallerCleanupManager()
	lease, err := manager.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	session := &serialStopTestSession{
		done:    make(chan installerWaitResult),
		entered: make(chan struct{}, 2),
		release: make(chan struct{}, 2),
	}
	stage, _ := newModelStagedPackage(t)
	if err := lease.BindStage(stage); err != nil {
		t.Fatal(err)
	}
	if err := lease.BindSession(session); err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	wait.Add(2)
	for index := 0; index < 2; index++ {
		go func() {
			defer wait.Done()
			_, _ = lease.stop(context.Background())
		}()
	}
	<-session.entered
	select {
	case <-session.entered:
		t.Fatal("concurrent lease Stop calls reached the session")
	case <-time.After(100 * time.Millisecond):
	}
	session.release <- struct{}{}
	<-session.entered
	session.release <- struct{}{}
	wait.Wait()
	if session.maximum != 1 {
		t.Fatalf("maximum concurrent Stop calls = %d", session.maximum)
	}
}

func TestInstallerCleanupQueuedStopHonorsItsOwnContext(t *testing.T) {
	manager := newInstallerCleanupManager()
	lease, err := manager.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	session := &serialStopTestSession{
		done:    make(chan installerWaitResult),
		entered: make(chan struct{}, 2),
		release: make(chan struct{}, 2),
	}
	stage, _ := newModelStagedPackage(t)
	if err := lease.BindStage(stage); err != nil {
		t.Fatal(err)
	}
	if err := lease.BindSession(session); err != nil {
		t.Fatal(err)
	}
	firstDone := make(chan struct{})
	go func() {
		_, _ = lease.stop(context.Background())
		close(firstDone)
	}()
	<-session.entered

	queuedContext, cancel := context.WithCancel(context.Background())
	cancel()
	queuedResult := make(chan error, 1)
	go func() {
		_, queuedErr := lease.stop(queuedContext)
		queuedResult <- queuedErr
	}()
	select {
	case queuedErr := <-queuedResult:
		if !errors.Is(queuedErr, context.Canceled) {
			t.Fatalf("queued Stop() = %v", queuedErr)
		}
	case <-time.After(250 * time.Millisecond):
		session.release <- struct{}{}
		<-firstDone
		t.Fatal("queued Stop ignored its own canceled context")
	}
	session.release <- struct{}{}
	<-firstDone
}

func TestInstallerManagedCleanupDoesNotSpinOnPermanentStopError(t *testing.T) {
	manager := newInstallerCleanupManager()
	manager.retryAfter = func(context.Context, time.Duration) bool {
		t.Fatal("permanent Stop error was retried")
		return false
	}
	lease, err := manager.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	stage, _ := newModelStagedPackage(t)
	if err := lease.BindStage(stage); err != nil {
		t.Fatal(err)
	}
	if err := lease.BindSession(&controlledInstallerSession{
		done: make(chan installerWaitResult),
		stop: func(context.Context) (installerStopResult, error) {
			return installerStopResult{}, errors.New("permanent fixture failure")
		},
	}); err != nil {
		t.Fatal(err)
	}
	lease.runManagedCleanup(manager)
	if _, err := manager.Acquire(); !errors.Is(err, errDarwinInstallerBusy) {
		t.Fatalf("permanent uncertainty did not retain lease: %v", err)
	}
}

type cleanupTestSession struct {
	done   chan installerWaitResult
	result installerWaitResult
}

type serialStopTestSession struct {
	mu      sync.Mutex
	done    chan installerWaitResult
	entered chan struct{}
	release chan struct{}
	active  int
	maximum int
}

func (session *serialStopTestSession) Done() <-chan installerWaitResult { return session.done }

func (session *serialStopTestSession) Stop(context.Context) (installerStopResult, error) {
	session.mu.Lock()
	session.active++
	if session.active > session.maximum {
		session.maximum = session.active
	}
	session.mu.Unlock()
	session.entered <- struct{}{}
	<-session.release
	session.mu.Lock()
	session.active--
	session.mu.Unlock()
	return installerStopResult{}, errors.New("fixture stop")
}

func newCleanupTestSession(result installerWaitResult) *cleanupTestSession {
	done := make(chan installerWaitResult, 1)
	done <- result
	close(done)
	return &cleanupTestSession{done: done, result: result}
}

func (session *cleanupTestSession) Done() <-chan installerWaitResult { return session.done }

func (session *cleanupTestSession) Stop(ctx context.Context) (installerStopResult, error) {
	if err := ctx.Err(); err != nil {
		return installerStopResult{}, err
	}
	return stopResultForWait(session.result), nil
}
