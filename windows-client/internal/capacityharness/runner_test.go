//go:build capacityharness

package capacityharness

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"mobile-egress/windows-client/internal/relayclient"
)

func TestRunnerUsesTenFreshOwnerProvisionedIdentitiesAndExactEightByThirtyTwoTopology(t *testing.T) {
	t.Parallel()

	control := newFakeControl()
	dialer := newCapacityFakeDialer(control)
	verifier := &fakeVerifier{}
	result, runErr := Run(context.Background(), RunConfig{
		OwnerLoader: fakeOwnerLoader{}, Control: control, Dialer: dialer, Verifier: verifier,
		Secrets: testRunSecrets(), HoldDuration: time.Millisecond, PhaseTimeout: time.Second,
		CleanupTimeout: time.Second, Emitter: discardEmitter{},
	})
	if runErr != nil {
		t.Fatalf("Run() = %v", runErr)
	}
	if control.provisionCalls != 10 {
		t.Fatalf("provision calls = %d, want 10", control.provisionCalls)
	}
	if control.healthCalls != 2 {
		t.Fatalf("dedicated-relay health checks = %d, want 2", control.healthCalls)
	}
	if dialer.dialCalls != 9 {
		t.Fatalf("session dials = %d, want 9", dialer.dialCalls)
	}
	if !dialer.sentinelWasRevokedBeforeDial {
		t.Fatal("first session dial happened before the tenth sentinel identity was revoked")
	}
	if verifier.calls != 257 {
		t.Fatalf("verified echoes = %d, want 257 (256 held plus replacement)", verifier.calls)
	}
	if result.Attempted != 266 || result.Open != 257 || result.Verified != 257 || result.Closed != 257 {
		t.Fatalf("result = %#v, want attempted/open/verified/closed 266/257/257/257", result)
	}
	if len(dialer.sessions) != 9 {
		t.Fatalf("sessions = %d, want 9", len(dialer.sessions))
	}
	for index, session := range dialer.sessions[:8] {
		wantAttempts := 33
		if index == 0 {
			wantAttempts = 34
		}
		if session.openCalls != wantAttempts {
			t.Fatalf("holder %d open calls = %d, want %d", index+1, session.openCalls, wantAttempts)
		}
		if session.closeCalls != 1 {
			t.Fatalf("holder %d close calls = %d, want 1", index+1, session.closeCalls)
		}
		for streamIndex, stream := range session.streams {
			if stream.closeCalls != 1 {
				t.Fatalf("holder %d stream %d close calls = %d, want exactly 1", index+1, streamIndex+1, stream.closeCalls)
			}
		}
	}
	if probe := dialer.sessions[8]; probe.openCalls != 1 || probe.closeCalls != 1 {
		t.Fatalf("probe calls = open %d/close %d, want 1/1", probe.openCalls, probe.closeCalls)
	}
	for serial, count := range control.revokeCalls {
		if count != 1 {
			t.Fatalf("identity %s revoked %d times, want exactly once", serial, count)
		}
	}
	if len(control.revokeCalls) != 10 || control.revokeCalls["0A"] != 1 {
		t.Fatalf("revocations = %#v, want all ten including sentinel exactly once", control.revokeCalls)
	}
	if active := dialer.activeCount(); active != 0 {
		t.Fatalf("active fake streams after cleanup = %d, want 0", active)
	}
}

func TestRunnerAttemptsFailedSentinelRevocationExactlyOnceAndDoesNotOpenSessions(t *testing.T) {
	t.Parallel()

	control := newFakeControl()
	control.failRevokeSerial = "0A"
	dialer := newCapacityFakeDialer(control)
	_, runErr := Run(context.Background(), RunConfig{
		OwnerLoader: fakeOwnerLoader{}, Control: control, Dialer: dialer, Verifier: &fakeVerifier{},
		Secrets: testRunSecrets(), HoldDuration: time.Millisecond, PhaseTimeout: time.Second,
		CleanupTimeout: time.Second, Emitter: discardEmitter{},
	})
	if runErr == nil || runErr.Phase != PhaseProvision || runErr.Category != FailurePreflight {
		t.Fatalf("Run() = %#v, want sentinel preflight failure", runErr)
	}
	if dialer.dialCalls != 0 {
		t.Fatalf("session dials = %d, want zero", dialer.dialCalls)
	}
	if len(control.revokeCalls) != 10 {
		t.Fatalf("revocation identities = %d, want 10", len(control.revokeCalls))
	}
	for serial, count := range control.revokeCalls {
		if count != 1 {
			t.Fatalf("identity %s revocation attempts = %d, want exactly 1", serial, count)
		}
	}
}

func TestRunnerTreatsEveryFreshIdentityCapacityFailureAsPreflightAndRevokesKnownIdentities(t *testing.T) {
	t.Parallel()

	for failAt := 1; failAt <= 10; failAt++ {
		failAt := failAt
		t.Run(fmt.Sprintf("provision-%d", failAt), func(t *testing.T) {
			control := newFakeControl()
			control.failProvisionAt = failAt
			dialer := newCapacityFakeDialer(control)
			_, runErr := Run(context.Background(), RunConfig{
				OwnerLoader: fakeOwnerLoader{}, Control: control, Dialer: dialer, Verifier: &fakeVerifier{},
				Secrets: testRunSecrets(), HoldDuration: time.Millisecond, PhaseTimeout: time.Second,
				CleanupTimeout: time.Second, Emitter: discardEmitter{},
			})
			if runErr == nil || runErr.Category != FailurePreflight || runErr.Phase != PhaseProvision {
				t.Fatalf("Run() error = %#v, want provision/preflight", runErr)
			}
			if dialer.dialCalls != 0 {
				t.Fatalf("session dials = %d, want zero after incomplete clean-relay proof", dialer.dialCalls)
			}
			if len(control.revokeCalls) != failAt-1 {
				t.Fatalf("revoked identities = %d, want %d", len(control.revokeCalls), failAt-1)
			}
			for serial, count := range control.revokeCalls {
				if count != 1 {
					t.Fatalf("identity %s revoked %d times", serial, count)
				}
			}
		})
	}
}

func TestRunnerFailsPreflightBeforeProvisioningWhenRelayIsNotDedicated(t *testing.T) {
	t.Parallel()

	for _, health := range []relayclient.RelayHealth{
		{Readiness: true, AgentConnected: true, ConnectedClients: 1, ActiveStreams: 0, ErrorCounts: map[string]int64{}},
		{Readiness: true, AgentConnected: true, ConnectedClients: 0, ActiveStreams: 1, ErrorCounts: map[string]int64{}},
		{Readiness: true, AgentConnected: false, ConnectedClients: 0, ActiveStreams: 0, ErrorCounts: map[string]int64{}},
	} {
		control := newFakeControl()
		control.health = health
		_, runErr := Run(context.Background(), RunConfig{
			OwnerLoader: fakeOwnerLoader{}, Control: control, Dialer: newCapacityFakeDialer(control), Verifier: &fakeVerifier{},
			Secrets: testRunSecrets(), HoldDuration: time.Millisecond, PhaseTimeout: time.Second,
			CleanupTimeout: time.Second, Emitter: discardEmitter{},
		})
		if runErr == nil || runErr.Category != FailurePreflight || control.provisionCalls != 0 {
			t.Fatalf("Run() = %#v with %d provisions, want preflight failure before provisioning", runErr, control.provisionCalls)
		}
	}
}

func TestRunnerCancellationAtEachLifecyclePhaseCleansUpKnownResources(t *testing.T) {
	t.Parallel()

	for _, phase := range []Phase{PhaseProvision, PhaseOpen, PhaseVerify, PhaseHold, PhaseReplacement} {
		phase := phase
		t.Run(string(phase), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			control := newFakeControl()
			dialer := newCapacityFakeDialer(control)
			emitter := &cancelingEmitter{phase: phase, cancel: cancel}
			_, runErr := Run(ctx, RunConfig{
				OwnerLoader: fakeOwnerLoader{}, Control: control, Dialer: dialer, Verifier: &fakeVerifier{},
				Secrets: testRunSecrets(), HoldDuration: time.Millisecond, PhaseTimeout: time.Second,
				CleanupTimeout: time.Second, Emitter: emitter,
			})
			if runErr == nil || runErr.Category != FailureCanceled {
				t.Fatalf("Run() = %#v, want canceled", runErr)
			}
			for serial, count := range control.revokeCalls {
				if count != 1 {
					t.Fatalf("identity %s revoked %d times", serial, count)
				}
			}
			for index, session := range dialer.sessions {
				if session.closeCalls != 1 {
					t.Fatalf("session %d closed %d times", index, session.closeCalls)
				}
			}
			if active := dialer.activeCount(); active != 0 {
				t.Fatalf("active streams after %s cancellation = %d", phase, active)
			}
		})
	}
}

func TestRunnerCancellationWhileExternalPhaseOperationsAreInFlight(t *testing.T) {
	t.Parallel()

	for _, phase := range []Phase{PhaseProvision, PhaseOpen, PhaseVerify, PhaseHold, PhaseReplacement} {
		phase := phase
		t.Run(string(phase), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			control := newFakeControl()
			dialer := newCapacityFakeDialer(control)
			verifier := &fakeVerifier{}
			entered := make(chan struct{}, 1)
			emitter := Emitter(discardEmitter{})
			switch phase {
			case PhaseProvision:
				control.blockProvisionAt = 3
				control.provisionEntered = entered
			case PhaseOpen:
				dialer.blockDialAt = 3
				dialer.dialEntered = entered
			case PhaseVerify:
				verifier.blockAt = 3
				verifier.entered = entered
			case PhaseHold:
				emitter = &phaseSignalEmitter{phase: PhaseHold, entered: entered}
			case PhaseReplacement:
				verifier.blockAt = aggregateStreams + 1
				verifier.entered = entered
			}
			type outcome struct {
				result Result
				err    *RunError
			}
			completed := make(chan outcome, 1)
			holdDuration := time.Minute
			if phase == PhaseReplacement {
				holdDuration = time.Millisecond
			}
			go func() {
				result, runErr := Run(ctx, RunConfig{
					OwnerLoader: fakeOwnerLoader{}, Control: control, Dialer: dialer, Verifier: verifier,
					Secrets: testRunSecrets(), HoldDuration: holdDuration, PhaseTimeout: time.Second,
					CleanupTimeout: time.Second, Emitter: emitter,
				})
				completed <- outcome{result: result, err: runErr}
			}()
			select {
			case <-entered:
			case <-time.After(2 * time.Second):
				t.Fatalf("%s operation never entered", phase)
			}
			cancel()
			select {
			case run := <-completed:
				if run.err == nil || run.err.Category != FailureCanceled {
					t.Fatalf("Run() = %#v, want in-flight %s cancellation", run.err, phase)
				}
			case <-time.After(2 * time.Second):
				t.Fatalf("Run did not retire the canceled %s operation", phase)
			}
			wantRevocations := 10
			if phase == PhaseProvision {
				wantRevocations = 2
			}
			if got := len(control.revokeCalls); got != wantRevocations {
				t.Fatalf("%s revocations = %d, want %d", phase, got, wantRevocations)
			}
			for index, session := range dialer.sessions {
				if calls := session.closeCount(); calls != 1 {
					t.Fatalf("%s session %d close calls = %d, want 1", phase, index, calls)
				}
			}
			if active := dialer.activeCount(); active != 0 {
				t.Fatalf("%s left %d active streams", phase, active)
			}
		})
	}
}

func TestRunnerBoundsCleanupAndStillAttemptsEveryCloseAndRevoke(t *testing.T) {
	t.Parallel()

	for attempt := 0; attempt < 5; attempt++ {
		control := newFakeControl()
		dialer := newCapacityFakeDialer(control)
		closeControl := &fakeSessionCloseControl{
			gate: make(chan struct{}), started: make(chan struct{}, 9), finished: make(chan struct{}, 9),
		}
		dialer.sessionCloseControl = closeControl
		var releaseOnce sync.Once
		release := func() { releaseOnce.Do(func() { close(closeControl.gate) }) }
		defer release()
		started := time.Now()
		_, runErr := Run(context.Background(), RunConfig{
			OwnerLoader: fakeOwnerLoader{}, Control: control, Dialer: dialer, Verifier: &fakeVerifier{},
			Secrets: testRunSecrets(), HoldDuration: time.Millisecond, PhaseTimeout: time.Second,
			CleanupTimeout: 20 * time.Millisecond, Emitter: discardEmitter{},
		})
		if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
			t.Fatalf("attempt %d bounded cleanup took %v", attempt+1, elapsed)
		}
		if runErr == nil || runErr.Phase != PhaseCleanup || runErr.Category != FailureCleanup {
			t.Fatalf("attempt %d Run() = %#v, want fixed cleanup failure", attempt+1, runErr)
		}
		for index := 0; index < 9; index++ {
			select {
			case <-closeControl.started:
			case <-time.After(time.Second):
				t.Fatalf("attempt %d started only %d session closes", attempt+1, index)
			}
		}
		if len(control.revokeCalls) != 10 {
			t.Fatalf("cleanup attempted %d identity revocations, want 10", len(control.revokeCalls))
		}
		if control.revokesWithCanceledContext != 0 {
			t.Fatalf("cleanup began %d revocations with an already-canceled context", control.revokesWithCanceledContext)
		}
		for index, session := range dialer.sessions {
			if calls := session.closeCount(); calls != 1 {
				t.Fatalf("session %d close calls = %d, want exactly 1", index, calls)
			}
		}
		for index := 0; index < 9; index++ {
			select {
			case <-closeControl.finished:
			case <-time.After(20 * time.Millisecond):
				t.Fatalf("attempt %d returned with %d session close workers still live", attempt+1, 9-index)
			}
		}
		release()
	}
}

func TestRunnerFailsWhenAnUnrelatedHeldStreamDiesDuringReplacement(t *testing.T) {
	t.Parallel()

	control := newFakeControl()
	dialer := newCapacityFakeDialer(control)
	emitter := &actionEmitter{phase: PhaseReplacement}
	emitter.action = func() {
		dialer.mu.Lock()
		unrelated := dialer.sessions[1].streams[0]
		dialer.mu.Unlock()
		_ = unrelated.Close()
		// Give the already-registered watcher a deterministic chance to publish
		// the failure before replacement proceeds.
		time.Sleep(10 * time.Millisecond)
	}
	_, runErr := Run(context.Background(), RunConfig{
		OwnerLoader: fakeOwnerLoader{}, Control: control, Dialer: dialer, Verifier: &fakeVerifier{},
		Secrets: testRunSecrets(), HoldDuration: time.Millisecond, PhaseTimeout: time.Second,
		CleanupTimeout: time.Second, Emitter: emitter,
	})
	if runErr == nil || runErr.Phase != PhaseReplacement || runErr.Category != FailureSession {
		t.Fatalf("Run() = %#v, want replacement/session after unrelated held-stream loss", runErr)
	}
}

func TestRunnerFailsWhenSelectedReplacementStreamTerminatesBeforeCloseHandoff(t *testing.T) {
	control := newFakeControl()
	dialer := newCapacityFakeDialer(control)
	watcherEntered := make(chan struct{})
	watcherGate := make(chan struct{})
	beginEntered := make(chan struct{})
	beginGate := make(chan struct{})
	cleanupEntered := make(chan struct{})
	verifier := &replacementRaceVerifier{
		watcherEntered: watcherEntered, watcherGate: watcherGate,
		beginEntered: beginEntered, beginGate: beginGate, cleanupEntered: cleanupEntered,
	}
	firstTracked := make(chan struct{})
	continueAfterWatcher := make(chan struct{})
	replacementEntered := make(chan struct{})
	continueReplacement := make(chan struct{})
	emitter := &replacementRaceEmitter{
		firstTracked: firstTracked, continueAfterWatcher: continueAfterWatcher,
		replacementEntered: replacementEntered, continueReplacement: continueReplacement,
	}
	type runResult struct {
		result Result
		err    *RunError
	}
	finished := make(chan runResult, 1)
	go func() {
		result, runErr := Run(context.Background(), RunConfig{
			OwnerLoader: fakeOwnerLoader{}, Control: control, Dialer: dialer, Verifier: verifier,
			Secrets: testRunSecrets(), HoldDuration: time.Millisecond, PhaseTimeout: time.Second,
			CleanupTimeout: time.Second, Emitter: emitter,
		})
		finished <- runResult{result: result, err: runErr}
	}()

	select {
	case <-firstTracked:
	case <-time.After(2 * time.Second):
		t.Fatal("runner did not reach the first post-track emission")
	}
	select {
	case <-watcherEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("oldest stream watcher did not enter its controlled Done call")
	}
	close(continueAfterWatcher)
	select {
	case <-replacementEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("runner did not reach the replacement emission")
	}
	verifier.mu.Lock()
	oldest := verifier.oldest
	verifier.mu.Unlock()
	if oldest == nil {
		t.Fatal("verifier did not retain the selected oldest stream")
	}
	if err := oldest.HeldStream.Close(); err != nil {
		t.Fatalf("terminate selected oldest stream = %v", err)
	}
	close(continueReplacement)

	// The buggy path marks the stream expected and enters BeginClose. The fixed
	// path detects the preexisting terminal state and enters cleanup without it.
	select {
	case <-beginEntered:
		close(watcherGate)
		close(beginGate)
	case <-cleanupEntered:
		close(watcherGate)
		close(beginGate)
	case <-time.After(2 * time.Second):
		t.Fatal("runner neither began the replacement close nor failed into cleanup")
	}
	select {
	case completed := <-finished:
		if completed.err == nil || completed.err.Phase != PhaseReplacement || completed.err.Category != FailureSession {
			t.Fatalf("Run() = %#v, want replacement/session for a pre-closed selected stream", completed.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runner did not finish after the controlled replacement race")
	}
}

func TestTrackedStreamCloseUsesProducerClaimAcrossTerminalPublicationGap(t *testing.T) {
	held := &controlledClaimHeldStream{
		done: make(chan struct{}), claimObservedOpen: make(chan struct{}),
		continueClaim: make(chan struct{}), legacyBegin: make(chan struct{}),
	}
	tracked := &trackedStream{stream: held}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- tracked.close(ctx) }()
	select {
	case <-held.claimObservedOpen:
	case <-held.legacyBegin:
		t.Fatal("tracked close used non-atomic BeginClose instead of the producer claim")
	case <-time.After(time.Second):
		t.Fatal("tracked close did not enter the producer close claim")
	}
	held.publishTerminal()
	close(held.continueClaim)
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("tracked close accepted a terminal state published before its producer claim")
		}
	case <-time.After(time.Second):
		t.Fatal("tracked close did not resolve the producer claim")
	}
}

func TestRunnerFailsWhenStreamTerminatesBetweenFinalLivenessScanAndCleanupClaim(t *testing.T) {
	control := newFakeControl()
	dialer := newCapacityFakeDialer(control)
	verifier := &finalClaimRaceVerifier{
		claimObservedOpen: make(chan struct{}), continueClaim: make(chan struct{}),
	}
	type runResult struct {
		result Result
		err    *RunError
	}
	finished := make(chan runResult, 1)
	go func() {
		result, runErr := Run(context.Background(), RunConfig{
			OwnerLoader: fakeOwnerLoader{}, Control: control, Dialer: dialer, Verifier: verifier,
			Secrets: testRunSecrets(), HoldDuration: time.Millisecond, PhaseTimeout: time.Second,
			CleanupTimeout: time.Second, Emitter: discardEmitter{},
		})
		finished <- runResult{result: result, err: runErr}
	}()

	select {
	case <-verifier.claimObservedOpen:
	case <-time.After(2 * time.Second):
		t.Fatal("runner never attempted an atomic final cleanup claim")
	}
	verifier.mu.Lock()
	replacement := verifier.replacement
	verifier.mu.Unlock()
	if replacement == nil {
		t.Fatal("verifier did not retain the final replacement stream")
	}
	replacement.publishTerminal()
	close(verifier.continueClaim)
	select {
	case completed := <-finished:
		if completed.err == nil || completed.err.Phase != PhaseReplacement || completed.err.Category != FailureSession {
			t.Fatalf("Run() = %#v, want replacement/session for final transition stream loss", completed.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runner did not finish after final transition stream loss")
	}
}

func TestCleanupUsesSessionCloseToUnblockHeldStreamsWithoutReportingSoftTimeout(t *testing.T) {
	sessionClosed := make(chan struct{})
	held := &escalatingHeldStream{sessionClosed: sessionClosed, done: make(chan struct{})}
	session := &escalatingSession{closed: sessionClosed}
	resources := newRunResources(relayclient.Identity{}, nil)
	resources.trackSession(session)
	resources.trackStream(held)
	_ = resources.stopWork(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if err := resources.closeTrackedResources(ctx); err != nil {
		t.Fatalf("closeTrackedResources() = %v, want successful hard-deadline escalation", err)
	}
	select {
	case <-held.done:
	case <-time.After(time.Second):
		t.Fatal("held close worker outlived session-close escalation")
	}
}

type escalatingHeldStream struct {
	sessionClosed <-chan struct{}
	done          chan struct{}
	once          sync.Once
	mu            sync.Mutex
	claimed       bool
	terminal      bool
}

func (stream *escalatingHeldStream) Close() error {
	stream.TryBeginClose()
	return stream.WaitClose(context.Background())
}
func (stream *escalatingHeldStream) Done() <-chan struct{} { return stream.done }
func (stream *escalatingHeldStream) TryBeginClose() bool {
	stream.mu.Lock()
	if stream.terminal {
		stream.mu.Unlock()
		return false
	}
	if stream.claimed {
		stream.mu.Unlock()
		return true
	}
	stream.claimed = true
	stream.mu.Unlock()
	stream.once.Do(func() {
		go func() {
			<-stream.sessionClosed
			stream.mu.Lock()
			stream.terminal = true
			stream.mu.Unlock()
			close(stream.done)
		}()
	})
	return true
}
func (stream *escalatingHeldStream) WaitClose(ctx context.Context) error {
	select {
	case <-stream.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type escalatingSession struct {
	closed chan struct{}
	once   sync.Once
}

func (*escalatingSession) OpenStream(context.Context, string, uint16) (CapacityStream, error) {
	return nil, errors.New("unused")
}
func (session *escalatingSession) Close() error {
	return session.CloseContext(context.Background())
}
func (session *escalatingSession) CloseContext(context.Context) error {
	session.once.Do(func() { close(session.closed) })
	return nil
}

type fakeOwnerLoader struct{}

func (fakeOwnerLoader) LoadOwner(context.Context) (relayclient.Identity, error) {
	return relayclient.Identity{
		RelayURL: "https://relay.example.com", Role: "owner", Serial: "AA",
		PrivateKeyPEM: "owner-key", CertificatePEM: "owner-cert", CACertificatePEM: "relay-ca",
	}, nil
}

type fakeControl struct {
	mu                         sync.Mutex
	health                     relayclient.RelayHealth
	healthCalls                int
	provisionCalls             int
	failProvisionAt            int
	blockProvisionAt           int
	provisionEntered           chan<- struct{}
	revokeCalls                map[string]int
	failRevokeSerial           string
	revokesWithCanceledContext int
}

func newFakeControl() *fakeControl {
	return &fakeControl{
		health:      relayclient.RelayHealth{Readiness: true, AgentConnected: true, ErrorCounts: map[string]int64{}},
		revokeCalls: make(map[string]int),
	}
}

func (control *fakeControl) Health(context.Context, relayclient.Identity) (relayclient.RelayHealth, error) {
	control.mu.Lock()
	control.healthCalls++
	control.mu.Unlock()
	return control.health, nil
}

func (control *fakeControl) ProvisionClient(ctx context.Context, _ relayclient.Identity, _ string) (relayclient.ProvisionedIdentity, error) {
	control.mu.Lock()
	control.provisionCalls++
	call := control.provisionCalls
	failed := call == control.failProvisionAt
	blocked := call == control.blockProvisionAt
	entered := control.provisionEntered
	control.mu.Unlock()
	if failed {
		return relayclient.ProvisionedIdentity{}, errors.New("SECRET-PROVISION-ERROR")
	}
	if blocked {
		entered <- struct{}{}
		<-ctx.Done()
		return relayclient.ProvisionedIdentity{}, ctx.Err()
	}
	serial := fmt.Sprintf("%02X", call)
	return relayclient.ProvisionedIdentity{
		RelayURL: "https://relay.example.com", Role: "client", Serial: serial,
		CertificatePEM: "client-cert", CACertificatePEM: "relay-ca",
	}, nil
}

func (control *fakeControl) Revoke(ctx context.Context, _ relayclient.Identity, serial string) error {
	control.mu.Lock()
	defer control.mu.Unlock()
	control.revokeCalls[serial]++
	if ctx.Err() != nil {
		control.revokesWithCanceledContext++
	}
	if serial == control.failRevokeSerial {
		return errors.New("SECRET-REVOKE-ERROR")
	}
	return nil
}

type capacityFakeDialer struct {
	mu                           sync.Mutex
	control                      *fakeControl
	dialCalls                    int
	active                       int
	sessions                     []*capacityFakeSession
	sentinelWasRevokedBeforeDial bool
	sessionCloseControl          *fakeSessionCloseControl
	blockDialAt                  int
	dialEntered                  chan<- struct{}
}

type fakeSessionCloseControl struct {
	gate     chan struct{}
	started  chan struct{}
	finished chan struct{}
}

func newCapacityFakeDialer(control *fakeControl) *capacityFakeDialer {
	return &capacityFakeDialer{control: control}
}

func (dialer *capacityFakeDialer) Dial(ctx context.Context, _ *ClientCredential) (CapacitySession, error) {
	dialer.mu.Lock()
	dialer.dialCalls++
	call := dialer.dialCalls
	if call == 1 {
		dialer.control.mu.Lock()
		dialer.sentinelWasRevokedBeforeDial = dialer.control.revokeCalls["0A"] == 1
		dialer.control.mu.Unlock()
	}
	blocked := call == dialer.blockDialAt
	entered := dialer.dialEntered
	dialer.mu.Unlock()
	if blocked {
		entered <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	dialer.mu.Lock()
	defer dialer.mu.Unlock()
	session := &capacityFakeSession{dialer: dialer, closeControl: dialer.sessionCloseControl}
	dialer.sessions = append(dialer.sessions, session)
	return session, nil
}

func (dialer *capacityFakeDialer) activeCount() int {
	dialer.mu.Lock()
	defer dialer.mu.Unlock()
	return dialer.active
}

type capacityFakeSession struct {
	mu           sync.Mutex
	dialer       *capacityFakeDialer
	active       int
	openCalls    int
	closeCalls   int
	closeControl *fakeSessionCloseControl
	closed       bool
	streams      []*capacityFakeStream
}

func (session *capacityFakeSession) OpenStream(context.Context, string, uint16) (CapacityStream, error) {
	session.mu.Lock()
	defer session.mu.Unlock()
	session.openCalls++
	if session.active >= holderStreams {
		return nil, RelayRejection{Code: "client_stream_limit"}
	}
	session.dialer.mu.Lock()
	defer session.dialer.mu.Unlock()
	if session.dialer.active >= aggregateStreams {
		return nil, RelayRejection{Code: "agent_stream_limit"}
	}
	stream := &capacityFakeStream{session: session, done: make(chan struct{})}
	session.active++
	session.dialer.active++
	session.streams = append(session.streams, stream)
	return stream, nil
}

func (session *capacityFakeSession) Close() error {
	return session.CloseContext(context.Background())
}

func (session *capacityFakeSession) CloseContext(ctx context.Context) error {
	session.mu.Lock()
	session.closeCalls++
	session.closed = true
	closeControl := session.closeControl
	session.mu.Unlock()
	if closeControl != nil {
		closeControl.started <- struct{}{}
		select {
		case <-closeControl.gate:
		case <-ctx.Done():
		}
		closeControl.finished <- struct{}{}
	}
	return ctx.Err()
}

func (session *capacityFakeSession) closeCount() int {
	session.mu.Lock()
	defer session.mu.Unlock()
	return session.closeCalls
}

type capacityFakeStream struct {
	mu         sync.Mutex
	session    *capacityFakeSession
	done       chan struct{}
	closeCalls int
	closed     bool
}

func (*capacityFakeStream) Read([]byte) (int, error)        { return 0, io.EOF }
func (*capacityFakeStream) Write(value []byte) (int, error) { return len(value), nil }
func (stream *capacityFakeStream) Done() <-chan struct{}    { return stream.done }
func (stream *capacityFakeStream) TryBeginClose() bool      { return stream.closeFirst() }
func (stream *capacityFakeStream) WaitClose(ctx context.Context) error {
	select {
	case <-stream.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (stream *capacityFakeStream) Close() error {
	stream.closeFirst()
	return nil
}

func (stream *capacityFakeStream) closeFirst() bool {
	stream.mu.Lock()
	stream.closeCalls++
	if stream.closed {
		stream.mu.Unlock()
		return false
	}
	stream.closed = true
	close(stream.done)
	stream.mu.Unlock()

	stream.session.mu.Lock()
	stream.session.active--
	stream.session.mu.Unlock()
	stream.session.dialer.mu.Lock()
	stream.session.dialer.active--
	stream.session.dialer.mu.Unlock()
	return true
}

type fakeVerifier struct {
	mu      sync.Mutex
	calls   int
	blockAt int
	entered chan<- struct{}
}

type replacementRaceVerifier struct {
	mu               sync.Mutex
	calls            int
	oldest           *replacementRaceHeldStream
	watcherEntered   chan struct{}
	watcherGate      chan struct{}
	beginEntered     chan struct{}
	beginGate        chan struct{}
	cleanupEntered   chan struct{}
	cleanupSignalSet bool
}

func (verifier *replacementRaceVerifier) Verify(_ context.Context, stream CapacityStream, _ string, _ []byte) (HeldStream, error) {
	held, ok := stream.(HeldStream)
	if !ok {
		return nil, errors.New("fake stream does not implement held cleanup")
	}
	verifier.mu.Lock()
	defer verifier.mu.Unlock()
	verifier.calls++
	if verifier.calls == 1 {
		verifier.oldest = &replacementRaceHeldStream{
			HeldStream: held, watcherEntered: verifier.watcherEntered, watcherGate: verifier.watcherGate,
			beginEntered: verifier.beginEntered, beginGate: verifier.beginGate,
		}
		return verifier.oldest, nil
	}
	if !verifier.cleanupSignalSet {
		verifier.cleanupSignalSet = true
		return &cleanupSignalHeldStream{HeldStream: held, entered: verifier.cleanupEntered}, nil
	}
	return held, nil
}

type replacementRaceHeldStream struct {
	HeldStream
	mu                 sync.Mutex
	doneCalls          int
	watcherEntered     chan struct{}
	watcherEnteredOnce sync.Once
	watcherGate        chan struct{}
	beginEntered       chan struct{}
	beginEnteredOnce   sync.Once
	beginGate          chan struct{}
}

func (stream *replacementRaceHeldStream) Done() <-chan struct{} {
	stream.mu.Lock()
	stream.doneCalls++
	call := stream.doneCalls
	stream.mu.Unlock()
	if call == 2 {
		stream.watcherEnteredOnce.Do(func() { close(stream.watcherEntered) })
		<-stream.watcherGate
	}
	return stream.HeldStream.Done()
}

func (stream *replacementRaceHeldStream) TryBeginClose() bool {
	stream.beginEnteredOnce.Do(func() { close(stream.beginEntered) })
	<-stream.beginGate
	return stream.HeldStream.TryBeginClose()
}

type cleanupSignalHeldStream struct {
	HeldStream
	entered chan struct{}
	once    sync.Once
}

type controlledClaimHeldStream struct {
	mu                sync.Mutex
	done              chan struct{}
	terminal          bool
	claimed           bool
	claimObservedOpen chan struct{}
	claimObservedOnce sync.Once
	continueClaim     chan struct{}
	legacyBegin       chan struct{}
	legacyBeginOnce   sync.Once
}

type finalClaimRaceVerifier struct {
	mu                sync.Mutex
	calls             int
	replacement       *controlledClaimHeldStream
	claimObservedOpen chan struct{}
	continueClaim     chan struct{}
}

func (verifier *finalClaimRaceVerifier) Verify(_ context.Context, stream CapacityStream, _ string, _ []byte) (HeldStream, error) {
	held, ok := stream.(HeldStream)
	if !ok {
		return nil, errors.New("fake stream does not implement held cleanup")
	}
	verifier.mu.Lock()
	defer verifier.mu.Unlock()
	verifier.calls++
	if verifier.calls != aggregateStreams+1 {
		return held, nil
	}
	verifier.replacement = &controlledClaimHeldStream{
		done: make(chan struct{}), claimObservedOpen: verifier.claimObservedOpen,
		continueClaim: verifier.continueClaim, legacyBegin: make(chan struct{}),
	}
	return verifier.replacement, nil
}

func (*controlledClaimHeldStream) Read([]byte) (int, error)        { return 0, io.EOF }
func (*controlledClaimHeldStream) Write(value []byte) (int, error) { return len(value), nil }
func (*controlledClaimHeldStream) Close() error                    { return nil }
func (stream *controlledClaimHeldStream) Done() <-chan struct{}    { return stream.done }
func (stream *controlledClaimHeldStream) BeginClose() {
	stream.legacyBeginOnce.Do(func() { close(stream.legacyBegin) })
}
func (stream *controlledClaimHeldStream) TryBeginClose() bool {
	stream.mu.Lock()
	if stream.terminal {
		stream.mu.Unlock()
		return false
	}
	stream.mu.Unlock()
	stream.claimObservedOnce.Do(func() { close(stream.claimObservedOpen) })
	<-stream.continueClaim
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if stream.terminal {
		return false
	}
	stream.claimed = true
	return true
}
func (stream *controlledClaimHeldStream) WaitClose(ctx context.Context) error {
	select {
	case <-stream.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (stream *controlledClaimHeldStream) publishTerminal() {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if !stream.terminal {
		stream.terminal = true
		close(stream.done)
	}
}

func (stream *cleanupSignalHeldStream) TryBeginClose() bool {
	stream.once.Do(func() { close(stream.entered) })
	return stream.HeldStream.TryBeginClose()
}

func (verifier *fakeVerifier) Verify(ctx context.Context, stream CapacityStream, _ string, _ []byte) (HeldStream, error) {
	verifier.mu.Lock()
	verifier.calls++
	call := verifier.calls
	blocked := call == verifier.blockAt
	entered := verifier.entered
	verifier.mu.Unlock()
	if blocked {
		entered <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	held, ok := stream.(HeldStream)
	if !ok {
		return nil, errors.New("fake stream does not implement held cleanup")
	}
	return held, nil
}

type discardEmitter struct{}

func (discardEmitter) Emit(Event) error { return nil }

type cancelingEmitter struct {
	mu     sync.Mutex
	phase  Phase
	cancel context.CancelFunc
	done   bool
}

type actionEmitter struct {
	mu     sync.Mutex
	phase  Phase
	action func()
	done   bool
}

type phaseSignalEmitter struct {
	mu      sync.Mutex
	phase   Phase
	entered chan<- struct{}
	done    bool
}

type replacementRaceEmitter struct {
	mu                   sync.Mutex
	firstDone            bool
	replacementDone      bool
	firstTracked         chan struct{}
	continueAfterWatcher chan struct{}
	replacementEntered   chan struct{}
	continueReplacement  chan struct{}
}

func (emitter *replacementRaceEmitter) Emit(event Event) error {
	emitter.mu.Lock()
	if !emitter.firstDone && event.Phase == PhaseOpen && event.Verified == 1 {
		emitter.firstDone = true
		entered := emitter.firstTracked
		gate := emitter.continueAfterWatcher
		emitter.mu.Unlock()
		close(entered)
		<-gate
		return nil
	}
	if !emitter.replacementDone && event.Phase == PhaseReplacement {
		emitter.replacementDone = true
		entered := emitter.replacementEntered
		gate := emitter.continueReplacement
		emitter.mu.Unlock()
		close(entered)
		<-gate
		return nil
	}
	emitter.mu.Unlock()
	return nil
}

func (emitter *phaseSignalEmitter) Emit(event Event) error {
	emitter.mu.Lock()
	defer emitter.mu.Unlock()
	if !emitter.done && event.Phase == emitter.phase {
		emitter.done = true
		emitter.entered <- struct{}{}
	}
	return nil
}

func (emitter *actionEmitter) Emit(event Event) error {
	emitter.mu.Lock()
	if emitter.done || event.Phase != emitter.phase {
		emitter.mu.Unlock()
		return nil
	}
	emitter.done = true
	action := emitter.action
	emitter.mu.Unlock()
	action()
	return nil
}

func (emitter *cancelingEmitter) Emit(event Event) error {
	emitter.mu.Lock()
	defer emitter.mu.Unlock()
	if !emitter.done && event.Phase == emitter.phase {
		emitter.done = true
		emitter.cancel()
	}
	return nil
}

func testRunSecrets() RunSecrets {
	return RunSecrets{Token: []byte("0123456789abcdefghijklmnopqrstuv"), TargetHost: "echo.example.com", TargetPort: 443}
}
