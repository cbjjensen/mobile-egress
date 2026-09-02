package relayservice

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"mobile-egress/internal/relayadmin"
)

const defaultRetryDelay = 250 * time.Millisecond

type Service struct {
	native                Native
	admin                 Admin
	expectedHelperVersion string
	nativeGate            chan struct{}
	retryDelay            time.Duration
}

func New(native Native, admin Admin, expectedHelperVersion string) (*Service, error) {
	if native == nil || admin == nil || !validVersion(expectedHelperVersion) {
		return nil, errors.New("relay service dependencies and helper version are required")
	}
	service := &Service{
		native: native, admin: admin, expectedHelperVersion: expectedHelperVersion,
		nativeGate: make(chan struct{}, 1), retryDelay: defaultRetryDelay,
	}
	service.nativeGate <- struct{}{}
	return service, nil
}

func validVersion(version string) bool {
	return utf8.ValidString(version) && strings.TrimSpace(version) != ""
}

func (service *Service) Observe(ctx context.Context) Observation {
	if service == nil {
		return unavailable(NativeUnknown, FailureNative)
	}
	status, class, ok := service.nativeStatus(ctx)
	if !ok {
		return unavailable(NativeUnknown, FailureCancelled)
	}
	if class != NativeErrorNone {
		return unavailable(status, FailureNative)
	}
	return service.observeStatus(ctx, status)
}

func (service *Service) nativeStatus(ctx context.Context) (NativeStatus, NativeErrorClass, bool) {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return NativeUnknown, NativeErrorNone, false
	case <-service.nativeGate:
	}
	defer func() { service.nativeGate <- struct{}{} }()
	if ctx.Err() != nil {
		return NativeUnknown, NativeErrorNone, false
	}
	status, class := service.native.Status(ctx)
	if ctx.Err() != nil {
		return NativeUnknown, NativeErrorNone, false
	}
	return status, class, true
}

func (service *Service) observeStatus(ctx context.Context, status NativeStatus) Observation {
	switch status {
	case NativeNotRegistered:
		return Observation{State: StateNotRegistered, Native: status}
	case NativeApprovalRequired:
		return Observation{State: StateApprovalRequired, Native: status}
	case NativeEnabled:
		return service.observeAdmin(ctx)
	case NativeNotFound, NativeUnknown:
		return unavailable(status, FailureNative)
	default:
		return unavailable(NativeUnknown, FailureNative)
	}
}

func (service *Service) observeAdmin(ctx context.Context) Observation {
	if ctx == nil {
		ctx = context.Background()
	}
	result, err := service.admin.Status(ctx)
	if err != nil {
		return unavailable(NativeEnabled, classifyAdminError(ctx, err))
	}
	if result.ProtocolVersion != relayadmin.Version || !validVersion(result.HelperVersion) {
		return unavailable(NativeEnabled, FailureProtocol)
	}
	if !result.Initialized && result.RelayRunning {
		return unavailable(NativeEnabled, FailureInconsistent)
	}
	exact := result.HelperVersion == service.expectedHelperVersion
	state := StateVersionMismatch
	if exact {
		state = StateEnabled
	}
	return Observation{
		State: state, Native: NativeEnabled, StrictV1: true, ExactHelper: exact,
		Initialized: result.Initialized, RelayRunning: result.RelayRunning,
		Repairable: result.Initialized,
	}
}

func classifyAdminError(ctx context.Context, err error) FailureClass {
	if ctx != nil && ctx.Err() != nil {
		return FailureCancelled
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return FailureCancelled
	}
	if errors.Is(err, relayadmin.ErrTransport) {
		return FailureTransport
	}
	if errors.Is(err, relayadmin.ErrInvalidResponse) {
		return FailureProtocol
	}
	var publicError *relayadmin.PublicError
	if errors.As(err, &publicError) {
		if publicError.Code == relayadmin.ErrorUnauthorized {
			return FailureUnauthorized
		}
		return FailureRemote
	}
	return FailureRemote
}

func unavailable(native NativeStatus, failure FailureClass) Observation {
	return Observation{State: StateUnavailable, Native: native, Failure: failure}
}

func (service *Service) PrepareSetup(ctx context.Context) SetupGate {
	if service == nil {
		return SetupGate{Observation: unavailable(NativeUnknown, FailureNative)}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return SetupGate{Observation: unavailable(NativeUnknown, FailureCancelled)}
	case <-service.nativeGate:
	}
	release := func() { service.nativeGate <- struct{}{} }

	status, class := service.native.Status(ctx)
	if ctx.Err() != nil {
		release()
		return SetupGate{Observation: unavailable(NativeUnknown, FailureCancelled)}
	}
	if class != NativeErrorNone {
		release()
		return SetupGate{Observation: unavailable(status, FailureNative)}
	}
	if status == NativeNotRegistered || status == NativeNotFound {
		registerClass := service.native.Register(ctx)
		if ctx.Err() != nil {
			release()
			return SetupGate{Observation: unavailable(NativeUnknown, FailureCancelled)}
		}
		if registerClass == NativeErrorUnavailable || registerClass == NativeErrorOther {
			release()
			return SetupGate{Observation: unavailable(NativeNotRegistered, FailureNative)}
		}
		status, class = service.native.Status(ctx)
		if ctx.Err() != nil {
			release()
			return SetupGate{Observation: unavailable(NativeUnknown, FailureCancelled)}
		}
		if class != NativeErrorNone {
			release()
			return SetupGate{Observation: unavailable(status, FailureNative)}
		}
	}

	if status == NativeApprovalRequired {
		openClass := service.native.OpenLoginItems(ctx)
		release()
		if openClass != NativeErrorNone || ctx.Err() != nil {
			return SetupGate{Observation: unavailable(NativeApprovalRequired, FailureNative)}
		}
		return SetupGate{Observation: Observation{State: StateApprovalRequired, Native: status}, Decision: SetupAwaitingApproval}
	}
	release()

	observation := service.observeStatus(ctx, status)
	decision := SetupBlocked
	if observation.State == StateEnabled && observation.StrictV1 && observation.ExactHelper && !observation.Initialized {
		decision = SetupProceed
	}
	return SetupGate{Observation: observation, Decision: decision}
}

func (service *Service) GateRotate(ctx context.Context) RotateGate {
	observation := service.Observe(ctx)
	return RotateGate{Observation: observation, Proceed: observation.State == StateEnabled && observation.ExactHelper && observation.Initialized}
}

func (service *Service) GateRepair(ctx context.Context) RepairGate {
	observation := service.Observe(ctx)
	return RepairGate{Observation: observation, Proceed: observation.Repairable}
}

func (service *Service) WaitForExactHelper(ctx context.Context) Observation {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		observation := service.Observe(ctx)
		if observation.State == StateEnabled && observation.ExactHelper && observation.Initialized {
			return observation
		}
		retry := observation.Failure == FailureTransport ||
			(observation.State == StateVersionMismatch && observation.StrictV1 && observation.Initialized)
		if !retry {
			return observation
		}
		timer := time.NewTimer(service.retryDelay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return unavailable(NativeUnknown, FailureCancelled)
		case <-timer.C:
		}
	}
}
