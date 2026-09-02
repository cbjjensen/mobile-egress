package relayservice

import (
	"context"
	"sync"
	"testing"
	"time"

	"mobile-egress/internal/relayadmin"
)

func TestObservationMapsServiceManagementAndHelperHealth(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		native NativeStatus
		result relayadmin.StatusResult
		err    error
		want   Observation
	}{
		{name: "not registered", native: NativeNotRegistered, want: Observation{State: StateNotRegistered, Native: NativeNotRegistered}},
		{name: "approval required", native: NativeApprovalRequired, want: Observation{State: StateApprovalRequired, Native: NativeApprovalRequired}},
		{
			name: "exact initialized helper", native: NativeEnabled,
			result: relayadmin.StatusResult{ProtocolVersion: relayadmin.Version, HelperVersion: "1.1.0", Initialized: true, RelayRunning: true},
			want:   Observation{State: StateEnabled, Native: NativeEnabled, StrictV1: true, ExactHelper: true, Initialized: true, RelayRunning: true, Repairable: true},
		},
		{
			name: "different initialized helper", native: NativeEnabled,
			result: relayadmin.StatusResult{ProtocolVersion: relayadmin.Version, HelperVersion: "1.0.9", Initialized: true, RelayRunning: true},
			want:   Observation{State: StateVersionMismatch, Native: NativeEnabled, StrictV1: true, Initialized: true, RelayRunning: true, Repairable: true},
		},
		{name: "socket unavailable", native: NativeEnabled, err: relayadmin.ErrTransport, want: Observation{State: StateUnavailable, Native: NativeEnabled, Failure: FailureTransport}},
		{
			name: "running before initialization is inconsistent", native: NativeEnabled,
			result: relayadmin.StatusResult{ProtocolVersion: relayadmin.Version, HelperVersion: "1.1.0", RelayRunning: true},
			want:   Observation{State: StateUnavailable, Native: NativeEnabled, Failure: FailureInconsistent},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			native := &fakeNative{statuses: []NativeStatus{test.native}}
			admin := &fakeAdmin{result: test.result, err: test.err}
			service := mustService(t, native, admin)
			if got := service.Observe(context.Background()); got != test.want {
				t.Fatalf("Observe() = %#v, want %#v", got, test.want)
			}
			if test.native != NativeEnabled && admin.calls != 0 {
				t.Fatalf("admin status calls = %d, want 0", admin.calls)
			}
		})
	}
}

func TestPrepareSetupRegistersBeforeAllowingOwnerBootstrap(t *testing.T) {
	t.Parallel()

	native := &fakeNative{statuses: []NativeStatus{NativeNotRegistered, NativeEnabled}}
	admin := &fakeAdmin{result: relayadmin.StatusResult{
		ProtocolVersion: relayadmin.Version,
		HelperVersion:   "1.1.0",
	}}
	service := mustService(t, native, admin)

	gate := service.PrepareSetup(context.Background())
	if gate.Decision != SetupProceed || gate.Observation.State != StateEnabled {
		t.Fatalf("PrepareSetup() = %#v, want enabled proceed", gate)
	}
	if got := native.calls(); got != "status,register,status" {
		t.Fatalf("native calls = %q, want status,register,status", got)
	}
	if admin.calls != 1 {
		t.Fatalf("admin status calls = %d, want 1", admin.calls)
	}
}

func TestPrepareSetupRegistersWhenServiceManagementInitiallyReportsNotFound(t *testing.T) {
	t.Parallel()

	native := &fakeNative{statuses: []NativeStatus{NativeNotFound, NativeEnabled}}
	admin := &fakeAdmin{result: relayadmin.StatusResult{
		ProtocolVersion: relayadmin.Version,
		HelperVersion:   "1.1.0",
	}}
	service := mustService(t, native, admin)

	gate := service.PrepareSetup(context.Background())
	if gate.Decision != SetupProceed || gate.Observation.State != StateEnabled {
		t.Fatalf("PrepareSetup() = %#v, want enabled proceed", gate)
	}
	if got := native.calls(); got != "status,register,status" {
		t.Fatalf("native calls = %q, want status,register,status", got)
	}
}

func TestPrepareSetupOpensLoginItemsAndDoesNotProbeOrProceedWhileApprovalIsPending(t *testing.T) {
	t.Parallel()

	native := &fakeNative{statuses: []NativeStatus{NativeApprovalRequired}}
	admin := &fakeAdmin{result: relayadmin.StatusResult{ProtocolVersion: relayadmin.Version, HelperVersion: "1.1.0"}}
	service := mustService(t, native, admin)

	gate := service.PrepareSetup(context.Background())
	if gate.Decision != SetupAwaitingApproval || gate.Observation.State != StateApprovalRequired {
		t.Fatalf("PrepareSetup() = %#v, want approval-required", gate)
	}
	if got := native.calls(); got != "status,open" {
		t.Fatalf("native calls = %q, want status,open", got)
	}
	if admin.calls != 0 {
		t.Fatalf("admin status calls = %d, want 0", admin.calls)
	}
}

func TestPrepareSetupRefreshesAnEnabledServiceWhoseAdminSocketIsMissing(t *testing.T) {
	t.Parallel()

	native := &fakeNative{statuses: []NativeStatus{NativeEnabled, NativeEnabled}}
	admin := &fakeAdmin{
		errs: []error{relayadmin.ErrTransport, nil},
		results: []relayadmin.StatusResult{
			{},
			{ProtocolVersion: relayadmin.Version, HelperVersion: "1.1.0"},
		},
	}
	service := mustService(t, native, admin)

	gate := service.PrepareSetup(context.Background())
	if gate.Decision != SetupProceed || gate.Observation.State != StateEnabled {
		t.Fatalf("PrepareSetup() = %#v, want enabled proceed after refresh", gate)
	}
	if got := native.calls(); got != "status,refresh,status" {
		t.Fatalf("native calls = %q, want status,refresh,status", got)
	}
	if admin.calls != 2 {
		t.Fatalf("admin status calls = %d, want 2", admin.calls)
	}
}

func TestRotateAndRepairRequireCompatibleInitializedHelper(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		helper      string
		initialized bool
		wantRotate  bool
		wantRepair  bool
	}{
		{name: "exact initialized", helper: "1.1.0", initialized: true, wantRotate: true, wantRepair: true},
		{name: "exact uninitialized", helper: "1.1.0"},
		{name: "mismatched initialized", helper: "1.0.9", initialized: true, wantRepair: true},
		{name: "mismatched uninitialized", helper: "1.0.9"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			admin := &fakeAdmin{results: []relayadmin.StatusResult{
				{ProtocolVersion: relayadmin.Version, HelperVersion: test.helper, Initialized: test.initialized},
				{ProtocolVersion: relayadmin.Version, HelperVersion: test.helper, Initialized: test.initialized},
			}}
			service := mustService(t, &fakeNative{statuses: []NativeStatus{NativeEnabled, NativeEnabled}}, admin)
			if got := service.GateRotate(context.Background()).Proceed; got != test.wantRotate {
				t.Fatalf("GateRotate().Proceed = %v, want %v", got, test.wantRotate)
			}
			if got := service.GateRepair(context.Background()).Proceed; got != test.wantRepair {
				t.Fatalf("GateRepair().Proceed = %v, want %v", got, test.wantRepair)
			}
		})
	}
}

func TestWaitForExactHelperRetriesTransportAndInitializedVersionMismatch(t *testing.T) {
	t.Parallel()

	admin := &fakeAdmin{
		errs: []error{relayadmin.ErrTransport, nil, nil},
		results: []relayadmin.StatusResult{
			{},
			{ProtocolVersion: relayadmin.Version, HelperVersion: "1.0.9", Initialized: true},
			{ProtocolVersion: relayadmin.Version, HelperVersion: "1.1.0", Initialized: true},
		},
	}
	service := mustService(t, &fakeNative{statuses: []NativeStatus{NativeEnabled, NativeEnabled, NativeEnabled}}, admin)
	service.retryDelay = time.Millisecond

	observation := service.WaitForExactHelper(context.Background())
	if observation.State != StateEnabled || !observation.ExactHelper || !observation.Initialized {
		t.Fatalf("WaitForExactHelper() = %#v, want exact initialized helper", observation)
	}
	if admin.calls != 3 {
		t.Fatalf("admin calls = %d, want 3", admin.calls)
	}
}

func TestNativeGateWaitIsCancellable(t *testing.T) {
	t.Parallel()

	entered := make(chan struct{})
	release := make(chan struct{})
	native := &fakeNative{statuses: []NativeStatus{NativeEnabled}, blockStatus: entered, releaseStatus: release}
	service := mustService(t, native, &fakeAdmin{result: relayadmin.StatusResult{ProtocolVersion: relayadmin.Version, HelperVersion: "1.1.0"}})

	firstDone := make(chan Observation, 1)
	go func() { firstDone <- service.Observe(context.Background()) }()
	<-entered

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if got := service.Observe(ctx); got.Failure != FailureCancelled {
		t.Fatalf("cancelled Observe() = %#v, want cancelled failure", got)
	}
	close(release)
	<-firstDone
}

func mustService(t *testing.T, native Native, admin Admin) *Service {
	t.Helper()
	service, err := New(native, admin, "1.1.0")
	if err != nil {
		t.Fatal(err)
	}
	return service
}

type fakeNative struct {
	mu            sync.Mutex
	statuses      []NativeStatus
	statusErrors  []NativeErrorClass
	registerError NativeErrorClass
	refreshError  NativeErrorClass
	openError     NativeErrorClass
	log           []string
	blockStatus   chan struct{}
	releaseStatus chan struct{}
}

func (native *fakeNative) Status(context.Context) (NativeStatus, NativeErrorClass) {
	native.mu.Lock()
	native.log = append(native.log, "status")
	index := len(native.logStatusOnly()) - 1
	status := NativeUnknown
	if index >= 0 && index < len(native.statuses) {
		status = native.statuses[index]
	}
	var class NativeErrorClass
	if index >= 0 && index < len(native.statusErrors) {
		class = native.statusErrors[index]
	}
	block, release := native.blockStatus, native.releaseStatus
	native.mu.Unlock()
	if block != nil {
		select {
		case block <- struct{}{}:
		default:
		}
		<-release
	}
	return status, class
}

func (native *fakeNative) Register(context.Context) NativeErrorClass {
	native.mu.Lock()
	defer native.mu.Unlock()
	native.log = append(native.log, "register")
	return native.registerError
}

func (native *fakeNative) Refresh(context.Context) NativeErrorClass {
	native.mu.Lock()
	defer native.mu.Unlock()
	native.log = append(native.log, "refresh")
	return native.refreshError
}

func (native *fakeNative) OpenLoginItems(context.Context) NativeErrorClass {
	native.mu.Lock()
	defer native.mu.Unlock()
	native.log = append(native.log, "open")
	return native.openError
}

func (native *fakeNative) calls() string {
	native.mu.Lock()
	defer native.mu.Unlock()
	result := ""
	for i, call := range native.log {
		if i > 0 {
			result += ","
		}
		result += call
	}
	return result
}

func (native *fakeNative) logStatusOnly() []string {
	var result []string
	for _, call := range native.log {
		if call == "status" {
			result = append(result, call)
		}
	}
	return result
}

type fakeAdmin struct {
	mu      sync.Mutex
	result  relayadmin.StatusResult
	results []relayadmin.StatusResult
	err     error
	errs    []error
	calls   int
}

func (admin *fakeAdmin) Status(context.Context) (relayadmin.StatusResult, error) {
	admin.mu.Lock()
	defer admin.mu.Unlock()
	index := admin.calls
	admin.calls++
	result, err := admin.result, admin.err
	if index < len(admin.results) {
		result = admin.results[index]
	}
	if index < len(admin.errs) {
		err = admin.errs[index]
	}
	return result, err
}
