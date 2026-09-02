//go:build capacityharness

package capacityharness

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"mobile-egress/internal/capacity"
	"mobile-egress/windows-client/internal/relayclient"
)

const (
	holderIdentities     = 1
	holderStreams        = capacity.ClientMaxConcurrentStreams
	aggregateStreams     = capacity.AgentMaxConcurrentStreams
	probeIdentities      = 1
	totalFreshIdentities = holderIdentities + probeIdentities
	echoPayloadBytes     = 16 << 10
	maxHoldDuration      = 30 * time.Minute
	maxPhaseTimeout      = 2 * time.Minute
	maxCleanupTimeout    = 2 * time.Minute
)

type CapacitySession interface {
	OpenStream(context.Context, string, uint16) (CapacityStream, error)
	Close() error
	CloseContext(context.Context) error
}

type CapacityStream interface {
	io.ReadWriteCloser
	Done() <-chan struct{}
	TryBeginClose() bool
}

type HeldStream interface {
	Close() error
	TryBeginClose() bool
	WaitClose(context.Context) error
	Done() <-chan struct{}
}

type SessionDialer interface {
	Dial(context.Context, *ClientCredential) (CapacitySession, error)
}

type StreamVerifier interface {
	Verify(context.Context, CapacityStream, string, []byte) (HeldStream, error)
}

type RelayRejection struct{ Code string }

func (rejection RelayRejection) Error() string {
	switch rejection.Code {
	case "client_stream_limit", "agent_stream_limit":
		return "relay rejected capacity stream: " + rejection.Code
	default:
		return "relay rejected capacity stream"
	}
}

type RunConfig struct {
	OwnerLoader    OwnerLoader
	Control        Control
	Dialer         SessionDialer
	Verifier       StreamVerifier
	Secrets        RunSecrets
	HoldDuration   time.Duration
	PhaseTimeout   time.Duration
	CleanupTimeout time.Duration
	Emitter        Emitter
}

type Result struct {
	Attempted int
	Open      int
	Verified  int
	Closed    int
}

type RunError struct {
	Phase    Phase
	Category FailureCategory
	cause    error
}

func (runErr *RunError) Error() string {
	if runErr == nil || !validPhase(runErr.Phase) || !validFailure(runErr.Category) {
		return "capacity harness failed"
	}
	return fmt.Sprintf("capacity harness failed: phase=%s category=%s", runErr.Phase, runErr.Category)
}

func Run(ctx context.Context, config RunConfig) (result Result, runErr *RunError) {
	defer config.Secrets.Zero()
	if ctx == nil {
		ctx = context.Background()
	}
	if validationErr := validateRunConfig(config); validationErr != nil {
		return result, &RunError{Phase: PhaseInput, Category: FailureInput, cause: validationErr}
	}
	if config.Emitter == nil {
		config.Emitter = discardEvents{}
	}

	owner, err := config.OwnerLoader.LoadOwner(ctx)
	if err != nil || !completeOwner(owner) {
		return result, failureFor(ctx, PhasePreflight, FailurePreflight, err)
	}
	resources := newRunResources(owner, config.Control)
	defer func() {
		cleanupErr := resources.cleanup(config.CleanupTimeout)
		result.Closed = resources.closedCount()
		cleanupFailure := FailureNone
		if cleanupErr != nil {
			cleanupFailure = FailureCleanup
			if runErr == nil {
				runErr = &RunError{Phase: PhaseCleanup, Category: FailureCleanup, cause: cleanupErr}
			}
		}
		_ = config.Emitter.Emit(result.event(PhaseCleanup, cleanupFailure))
		if runErr == nil {
			if emitErr := config.Emitter.Emit(result.event(PhaseComplete, FailureNone)); emitErr != nil {
				runErr = &RunError{Phase: PhaseComplete, Category: FailureInternal, cause: emitErr}
			}
		} else {
			_ = config.Emitter.Emit(result.event(runErr.Phase, runErr.Category))
		}
	}()

	if runErr = emitAndCheck(ctx, config.Emitter, result.event(PhasePreflight, FailureNone)); runErr != nil {
		return result, runErr
	}
	healthCtx, cancelHealth := context.WithTimeout(ctx, config.PhaseTimeout)
	health, healthErr := config.Control.Health(healthCtx, owner)
	cancelHealth()
	if healthErr != nil || !health.Readiness || !health.AgentConnected || health.ConnectedClients != 0 || health.ActiveStreams != 0 {
		return result, failureFor(ctx, PhasePreflight, FailurePreflight, healthErr)
	}

	clients := make([]*revocableCredential, 0, totalFreshIdentities)
	for index := 0; index < totalFreshIdentities; index++ {
		if runErr = emitAndCheck(ctx, config.Emitter, result.event(PhaseProvision, FailureNone)); runErr != nil {
			return result, runErr
		}
		provisionCtx, cancelProvision := context.WithTimeout(ctx, config.PhaseTimeout)
		credential, provisionErr := provisionClientCredential(provisionCtx, config.Control, owner)
		cancelProvision()
		if provisionErr != nil {
			return result, failureFor(ctx, PhaseProvision, FailurePreflight, provisionErr)
		}
		tracked := resources.trackIdentity(credential)
		clients = append(clients, tracked)
	}

	recheckCtx, cancelRecheck := context.WithTimeout(ctx, config.PhaseTimeout)
	recheckedHealth, recheckErr := config.Control.Health(recheckCtx, owner)
	cancelRecheck()
	if recheckErr != nil || !recheckedHealth.Readiness || !recheckedHealth.AgentConnected ||
		recheckedHealth.ConnectedClients != 0 || recheckedHealth.ActiveStreams != 0 {
		return result, failureFor(ctx, PhasePreflight, FailurePreflight, recheckErr)
	}

	sessions := make([]CapacitySession, 0, holderIdentities+probeIdentities)
	for index := 0; index < holderIdentities+probeIdentities; index++ {
		if runErr = emitAndCheck(ctx, config.Emitter, result.event(PhaseOpen, FailureNone)); runErr != nil {
			return result, runErr
		}
		dialCtx, cancelDial := context.WithTimeout(ctx, config.PhaseTimeout)
		session, dialErr := config.Dialer.Dial(dialCtx, clients[index].credential)
		cancelDial()
		clients[index].credential.clearPrivateKey()
		if dialErr != nil || session == nil {
			return result, failureFor(ctx, PhaseOpen, FailureSession, dialErr)
		}
		resources.trackSession(session)
		sessions = append(sessions, session)
	}

	for holderIndex := 0; holderIndex < holderIdentities; holderIndex++ {
		for streamIndex := 0; streamIndex < holderStreams; streamIndex++ {
			if runErr = emitAndCheck(ctx, config.Emitter, result.event(PhaseOpen, FailureNone)); runErr != nil {
				return result, runErr
			}
			held, openErr := openAndVerify(ctx, config, sessions[holderIndex], &result)
			if openErr != nil {
				return result, openErr
			}
			resources.trackStream(held)
		}
	}

	if runErr = emitAndCheck(ctx, config.Emitter, result.event(PhaseLimit, FailureNone)); runErr != nil {
		return result, runErr
	}
	result.Attempted++
	probeCtx, cancelProbe := context.WithTimeout(ctx, config.PhaseTimeout)
	unexpected, probeErr := sessions[holderIdentities].OpenStream(probeCtx, config.Secrets.TargetHost, config.Secrets.TargetPort)
	cancelProbe()
	if unexpected != nil {
		_ = unexpected.Close()
	}
	if !rejectedWith(probeErr, "agent_stream_limit") {
		return result, failureFor(ctx, PhaseLimit, FailureAgentLimit, probeErr)
	}

	if runErr = emitAndCheck(ctx, config.Emitter, result.event(PhaseHold, FailureNone)); runErr != nil {
		return result, runErr
	}
	holdTimer := time.NewTimer(config.HoldDuration)
	select {
	case <-ctx.Done():
		if !holdTimer.Stop() {
			<-holdTimer.C
		}
		return result, failureFor(ctx, PhaseHold, FailureCanceled, ctx.Err())
	case category := <-resources.failures:
		if !holdTimer.Stop() {
			<-holdTimer.C
		}
		return result, &RunError{Phase: PhaseHold, Category: category}
	case <-holdTimer.C:
	}
	if category := resources.observedFailure(); category != FailureNone {
		return result, &RunError{Phase: PhaseReplacement, Category: category}
	}

	if runErr = emitAndCheck(ctx, config.Emitter, result.event(PhaseReplacement, FailureNone)); runErr != nil {
		return result, runErr
	}
	if closeErr := resources.closeOldestStream(config.PhaseTimeout); closeErr != nil {
		return result, failureFor(ctx, PhaseReplacement, FailureSession, closeErr)
	}
	replacement, replacementErr := openAndVerify(ctx, config, sessions[0], &result)
	if replacementErr != nil {
		return result, replacementErr.withPhase(PhaseReplacement)
	}
	resources.trackStream(replacement)
	if claimErr := resources.claimAllStreamsForShutdown(); claimErr != nil {
		return result, &RunError{Phase: PhaseReplacement, Category: FailureSession, cause: claimErr}
	}
	resources.stopping.Store(true)
	if category := resources.observedFailure(); category != FailureNone {
		return result, &RunError{Phase: PhaseReplacement, Category: category}
	}
	return result, nil
}

func openAndVerify(ctx context.Context, config RunConfig, session CapacitySession, result *Result) (HeldStream, *RunError) {
	result.Attempted++
	openCtx, cancelOpen := context.WithTimeout(ctx, config.PhaseTimeout)
	stream, err := session.OpenStream(openCtx, config.Secrets.TargetHost, config.Secrets.TargetPort)
	cancelOpen()
	if err != nil || stream == nil {
		return nil, failureFor(ctx, PhaseOpen, FailureSession, err)
	}
	result.Open++
	if emitErr := emitAndCheck(ctx, config.Emitter, result.event(PhaseVerify, FailureNone)); emitErr != nil {
		_ = stream.Close()
		return nil, emitErr
	}
	verifyCtx, cancelVerify := context.WithTimeout(ctx, config.PhaseTimeout)
	held, err := config.Verifier.Verify(verifyCtx, stream, config.Secrets.TargetHost, config.Secrets.Token)
	cancelVerify()
	if err != nil || held == nil || held.Done() == nil {
		_ = stream.Close()
		return nil, failureFor(ctx, PhaseVerify, classifyVerificationFailure(err), err)
	}
	result.Verified++
	return held, nil
}

func validateRunConfig(config RunConfig) error {
	if config.OwnerLoader == nil || config.Control == nil || config.Dialer == nil || config.Verifier == nil ||
		len(config.Secrets.Token) != tokenBytes || !validPublicHostname(config.Secrets.TargetHost) || config.Secrets.TargetPort != 443 ||
		config.HoldDuration <= 0 || config.HoldDuration > maxHoldDuration || config.PhaseTimeout <= 0 ||
		config.PhaseTimeout > maxPhaseTimeout || config.CleanupTimeout <= 0 || config.CleanupTimeout > maxCleanupTimeout {
		return errors.New("capacity harness run configuration is invalid")
	}
	return nil
}

func completeOwner(owner relayclient.Identity) bool {
	return owner.Role == "owner" && validIdentitySerial(owner.Serial) && owner.RelayURL != "" &&
		owner.PrivateKeyPEM != "" && owner.CertificatePEM != "" && owner.CACertificatePEM != ""
}

func rejectedWith(err error, code string) bool {
	var rejection RelayRejection
	return errors.As(err, &rejection) && rejection.Code == code
}

func classifyVerificationFailure(err error) FailureCategory {
	var categorized CategorizedError
	if errors.As(err, &categorized) && validFailure(categorized.Category) {
		return categorized.Category
	}
	return FailureEcho
}

type CategorizedError struct {
	Category FailureCategory
	cause    error
}

func (err CategorizedError) Error() string {
	if validFailure(err.Category) {
		return "capacity harness operation failed: " + string(err.Category)
	}
	return "capacity harness operation failed"
}

func failureFor(ctx context.Context, phase Phase, fallback FailureCategory, cause error) *RunError {
	category := fallback
	if errors.Is(ctx.Err(), context.Canceled) {
		category = FailureCanceled
	} else if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(cause, context.DeadlineExceeded) {
		category = FailureTimeout
	}
	return &RunError{Phase: phase, Category: category, cause: cause}
}

func (runErr *RunError) withPhase(phase Phase) *RunError {
	if runErr != nil {
		runErr.Phase = phase
	}
	return runErr
}

func emitAndCheck(ctx context.Context, emitter Emitter, event Event) *RunError {
	if err := emitter.Emit(event); err != nil {
		return &RunError{Phase: event.Phase, Category: FailureInternal, cause: err}
	}
	if err := ctx.Err(); err != nil {
		return failureFor(ctx, event.Phase, FailureCanceled, err)
	}
	return nil
}

func (result Result) event(phase Phase, failure FailureCategory) Event {
	return Event{Phase: phase, Attempted: result.Attempted, Open: result.Open, Verified: result.Verified, Closed: result.Closed, Failure: failure}
}

type discardEvents struct{}

func (discardEvents) Emit(Event) error { return nil }

func (credential *ClientCredential) clearPrivateKey() {
	clear(credential.PrivateKeyPEM)
	credential.PrivateKeyPEM = nil
}

type revocableCredential struct {
	credential *ClientCredential
	once       sync.Once
	err        error
}

func (identity *revocableCredential) revoke(ctx context.Context, control Control, owner relayclient.Identity) error {
	identity.once.Do(func() { identity.err = control.Revoke(ctx, owner, identity.credential.Serial) })
	return identity.err
}

type trackedStream struct {
	stream   HeldStream
	stateMu  sync.Mutex
	begun    bool
	expected bool
	counted  atomic.Bool
}

func (tracked *trackedStream) beginClose() bool {
	tracked.stateMu.Lock()
	defer tracked.stateMu.Unlock()
	if tracked.begun {
		return tracked.expected
	}
	tracked.begun = true
	tracked.expected = tracked.stream.TryBeginClose()
	return tracked.expected
}

func (tracked *trackedStream) closeExpected() bool {
	tracked.stateMu.Lock()
	defer tracked.stateMu.Unlock()
	return tracked.expected
}

func (tracked *trackedStream) close(ctx context.Context) error {
	if !tracked.beginClose() {
		return errors.New("held capacity stream terminated before close handoff")
	}
	return tracked.stream.WaitClose(ctx)
}

type runResources struct {
	owner       relayclient.Identity
	control     Control
	mu          sync.Mutex
	identities  []*revocableCredential
	sessions    []CapacitySession
	streams     []*trackedStream
	stopping    atomic.Bool
	stopOnce    sync.Once
	watchStop   chan struct{}
	watchers    sync.WaitGroup
	failures    chan FailureCategory
	closed      atomic.Int64
	cleanupOnce sync.Once
	cleanupErr  error
}

func newRunResources(owner relayclient.Identity, control Control) *runResources {
	return &runResources{owner: owner, control: control, watchStop: make(chan struct{}), failures: make(chan FailureCategory, 1)}
}

func (resources *runResources) trackIdentity(credential *ClientCredential) *revocableCredential {
	tracked := &revocableCredential{credential: credential}
	resources.mu.Lock()
	resources.identities = append(resources.identities, tracked)
	resources.mu.Unlock()
	return tracked
}

func (resources *runResources) trackSession(session CapacitySession) {
	resources.mu.Lock()
	resources.sessions = append(resources.sessions, session)
	resources.mu.Unlock()
}

func (resources *runResources) trackStream(stream HeldStream) {
	tracked := &trackedStream{stream: stream}
	resources.mu.Lock()
	resources.streams = append(resources.streams, tracked)
	resources.mu.Unlock()
	resources.watchers.Add(1)
	go func() {
		defer resources.watchers.Done()
		select {
		case <-stream.Done():
			if !resources.stopping.Load() && !tracked.closeExpected() {
				select {
				case resources.failures <- FailureSession:
				default:
				}
			}
		case <-resources.watchStop:
		}
	}()
}

func (resources *runResources) closeOldestStream(timeout time.Duration) error {
	resources.mu.Lock()
	if len(resources.streams) == 0 {
		resources.mu.Unlock()
		return errors.New("no held capacity stream")
	}
	tracked := resources.streams[0]
	resources.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := tracked.close(ctx); err != nil {
		return err
	}
	if tracked.counted.CompareAndSwap(false, true) {
		resources.closed.Add(1)
	}
	return nil
}

func (resources *runResources) claimAllStreamsForShutdown() error {
	resources.mu.Lock()
	streams := append([]*trackedStream(nil), resources.streams...)
	resources.mu.Unlock()
	for index := len(streams) - 1; index >= 0; index-- {
		if !streams[index].beginClose() {
			return errors.New("held capacity stream terminated before final cleanup claim")
		}
	}
	return nil
}

func (resources *runResources) cleanup(timeout time.Duration) error {
	resources.cleanupOnce.Do(func() {
		shutdownBudget := timeout / 2
		revocationBudget := timeout - shutdownBudget
		shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), shutdownBudget)
		stack := cleanupStack{}
		stack.push(resources.prepareWorkerWait())
		stack.push(resources.closeTrackedResources)
		stack.push(resources.stopWork)
		shutdownErr := stack.run(shutdownCtx)
		cancelShutdown()
		revocationErr := resources.revokeAllWithin(revocationBudget)
		resources.cleanupErr = shutdownErr
		if resources.cleanupErr == nil {
			resources.cleanupErr = revocationErr
		}
	})
	return resources.cleanupErr
}

func (resources *runResources) stopWork(context.Context) error {
	resources.stopping.Store(true)
	resources.stopOnce.Do(func() { close(resources.watchStop) })
	return nil
}

func (resources *runResources) closeTrackedResources(ctx context.Context) error {
	resources.mu.Lock()
	streams := append([]*trackedStream(nil), resources.streams...)
	sessions := append([]CapacitySession(nil), resources.sessions...)
	resources.mu.Unlock()
	var first error
	for index := len(streams) - 1; index >= 0; index-- {
		if !streams[index].beginClose() && first == nil {
			first = errors.New("held capacity stream terminated before cleanup claim")
		}
	}
	softCtx := ctx
	cancelSoft := func() {}
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining > 0 {
			softCtx, cancelSoft = context.WithTimeout(context.Background(), remaining/2)
		}
	}
	for index := len(streams) - 1; index >= 0; index-- {
		if err := streams[index].stream.WaitClose(softCtx); err != nil && softCtx.Err() == nil && first == nil {
			first = err
		}
	}
	cancelSoft()
	for index := len(sessions) - 1; index >= 0; index-- {
		if err := sessions[index].CloseContext(ctx); err != nil && first == nil {
			first = err
		}
	}
	for index := len(streams) - 1; index >= 0; index-- {
		if err := streams[index].stream.WaitClose(ctx); err != nil {
			if first == nil {
				first = err
			}
			continue
		}
		if streams[index].counted.CompareAndSwap(false, true) {
			resources.closed.Add(1)
		}
	}
	return first
}

func (resources *runResources) prepareWorkerWait() cleanupAction {
	done := make(chan struct{})
	go func() {
		resources.watchers.Wait()
		close(done)
	}()
	return func(ctx context.Context) error {
		select {
		case <-done:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (resources *runResources) revokeAllWithin(timeout time.Duration) error {
	resources.mu.Lock()
	identities := append([]*revocableCredential(nil), resources.identities...)
	resources.mu.Unlock()
	deadline := time.Now().Add(timeout)
	var first error
	for index := len(identities) - 1; index >= 0; index-- {
		remaining := time.Until(deadline)
		attemptBudget := remaining / time.Duration(index+1)
		if attemptBudget <= 0 {
			attemptBudget = time.Nanosecond
		}
		attemptCtx, cancelAttempt := context.WithTimeout(context.Background(), attemptBudget)
		err := identities[index].revoke(attemptCtx, resources.control, resources.owner)
		cancelAttempt()
		if err != nil && first == nil {
			first = err
		}
		identities[index].credential.Zero()
	}
	return first
}

func (resources *runResources) closedCount() int { return int(resources.closed.Load()) }

func (resources *runResources) observedFailure() FailureCategory {
	select {
	case category := <-resources.failures:
		return category
	default:
	}
	resources.mu.Lock()
	streams := append([]*trackedStream(nil), resources.streams...)
	resources.mu.Unlock()
	for _, tracked := range streams {
		if tracked.closeExpected() {
			continue
		}
		select {
		case <-tracked.stream.Done():
			return FailureSession
		default:
		}
	}
	return FailureNone
}

type cleanupAction func(context.Context) error
type cleanupStack struct{ actions []cleanupAction }

func (stack *cleanupStack) push(action cleanupAction) { stack.actions = append(stack.actions, action) }
func (stack cleanupStack) run(ctx context.Context) error {
	var first error
	for index := len(stack.actions) - 1; index >= 0; index-- {
		if err := stack.actions[index](ctx); err != nil && first == nil {
			first = err
		}
	}
	return first
}
