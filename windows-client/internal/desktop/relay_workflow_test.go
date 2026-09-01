package desktop

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"mobile-egress/windows-client/internal/client"
	"mobile-egress/windows-client/internal/localbridge"
	"mobile-egress/windows-client/internal/relayclient"
	"mobile-egress/windows-client/internal/relayservice"
	"mobile-egress/windows-client/internal/securestore"
)

func TestGetBridgeStatusUsesVerifiedMacRelayServiceObservation(t *testing.T) {
	t.Parallel()

	service := &desktopRelayServiceFake{observations: []relayservice.Observation{{
		State: relayservice.StateVersionMismatch, StrictV1: true, Initialized: true, Repairable: true,
	}}}
	app := newMacWorkflowTestApp(t, securestore.NewMemoryStore(), service, &desktopBridgeSpy{})

	status := app.GetBridgeStatus()
	if got, want := status.RelayServiceState, "version-mismatch"; got != want {
		t.Fatalf("GetBridgeStatus().RelayServiceState = %q, want %q", got, want)
	}
	if service.observeCalls != 1 {
		t.Fatalf("Observe calls = %d, want 1", service.observeCalls)
	}
}

func TestSetupLocalBridgeReturnsApprovalStateWithoutCreatingOwner(t *testing.T) {
	t.Parallel()

	service := &desktopRelayServiceFake{setupGates: []relayservice.SetupGate{{
		Observation: relayservice.Observation{State: relayservice.StateApprovalRequired},
		Decision:    relayservice.SetupAwaitingApproval,
	}}}
	bridge := &desktopBridgeSpy{}
	app := newMacWorkflowTestApp(t, securestore.NewMemoryStore(), service, bridge)

	status, err := app.SetupLocalBridge()
	if err != nil {
		t.Fatal(err)
	}
	if status.RelayServiceState != "approval-required" || status.OwnerReady {
		t.Fatalf("SetupLocalBridge() = %#v, want approval-required without Owner", status)
	}
	if bridge.setupCalls != 0 {
		t.Fatalf("bridge Setup calls = %d, want 0", bridge.setupCalls)
	}
	if service.observeCalls != 0 {
		t.Fatalf("Observe calls = %d, want known gate response without a second probe", service.observeCalls)
	}
}

func TestSetupLocalBridgeCallsBridgeOnlyAfterExactHelperIsEnabled(t *testing.T) {
	t.Parallel()

	service := &desktopRelayServiceFake{
		setupGates: []relayservice.SetupGate{{
			Observation: relayservice.Observation{State: relayservice.StateEnabled, StrictV1: true, ExactHelper: true},
			Decision:    relayservice.SetupProceed,
		}},
		observations: []relayservice.Observation{{State: relayservice.StateEnabled, StrictV1: true, ExactHelper: true, Initialized: true}},
	}
	bridge := &desktopBridgeSpy{}
	app := newMacWorkflowTestApp(t, securestore.NewMemoryStore(), service, bridge)

	if _, err := app.SetupLocalBridge(); err != nil {
		t.Fatal(err)
	}
	if bridge.setupCalls != 1 {
		t.Fatalf("bridge Setup calls = %d, want 1", bridge.setupCalls)
	}
}

func TestRotateLocalBridgeChecksRelayServiceBeforeLoadingOwnerOrNodes(t *testing.T) {
	t.Parallel()

	service := &desktopRelayServiceFake{rotateGates: []relayservice.RotateGate{{
		Observation: relayservice.Observation{State: relayservice.StateVersionMismatch},
	}}}
	app := newMacWorkflowTestApp(t, securestore.NewMemoryStore(), service, &desktopBridgeSpy{})
	app.ownerRepository = nil
	app.cloudRepository = nil

	_, err := app.RotateLocalBridge()
	if err == nil || !strings.Contains(err.Error(), "enabled") {
		t.Fatalf("RotateLocalBridge() error = %v, want fixed relay-service guidance", err)
	}
}

func TestRepairLocalBridgeAllowsInitializedVersionMismatchAndWaitsForExactHelper(t *testing.T) {
	t.Parallel()

	store := securestore.NewMemoryStore()
	owner := relayclient.Identity{
		RelayURL: "https://relay.example", DialAddress: "127.0.0.1:8443", Role: "owner", Serial: "OWNER",
		PrivateKeyPEM: "owner-key", CertificatePEM: "owner-chain", CACertificatePEM: "ca",
	}
	if err := client.NewRepository(store).SaveOwnerIdentity(context.Background(), owner); err != nil {
		t.Fatal(err)
	}
	service := &desktopRelayServiceFake{
		repairGates: []relayservice.RepairGate{{
			Observation: relayservice.Observation{State: relayservice.StateVersionMismatch, StrictV1: true, Initialized: true, Repairable: true},
			Proceed:     true,
		}},
		wait:         relayservice.Observation{State: relayservice.StateEnabled, StrictV1: true, ExactHelper: true, Initialized: true},
		observations: []relayservice.Observation{{State: relayservice.StateEnabled, StrictV1: true, ExactHelper: true, Initialized: true}},
	}
	bridge := &desktopBridgeSpy{}
	app := newMacWorkflowTestApp(t, store, service, bridge)

	if _, err := app.RepairLocalBridge(); err != nil {
		t.Fatal(err)
	}
	if bridge.repairCalls != 1 || service.waitCalls != 1 {
		t.Fatalf("repair calls = %d, wait calls = %d; want 1/1", bridge.repairCalls, service.waitCalls)
	}
	stored, _, err := client.NewRepository(store).LoadOwnerIdentity(context.Background())
	if err != nil || stored != owner {
		t.Fatalf("Owner changed during repair: %#v/%v", stored, err)
	}
}

func TestMacBridgeWorkflowWaitCanBeCancelledWithoutCallingBridge(t *testing.T) {
	t.Parallel()

	service := &desktopRelayServiceFake{setupGates: []relayservice.SetupGate{{
		Observation: relayservice.Observation{State: relayservice.StateEnabled, StrictV1: true, ExactHelper: true},
		Decision:    relayservice.SetupProceed,
	}}}
	bridge := &desktopBridgeSpy{setupEntered: make(chan struct{}), setupRelease: make(chan struct{})}
	app := newMacWorkflowTestApp(t, securestore.NewMemoryStore(), service, bridge)

	firstDone := make(chan error, 1)
	go func() {
		_, err := app.setupLocalBridge(context.Background())
		firstDone <- err
	}()
	<-bridge.setupEntered

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := app.setupLocalBridge(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled setup error = %v, want context cancellation", err)
	}
	close(bridge.setupRelease)
	<-firstDone
	if bridge.setupCalls != 1 {
		t.Fatalf("bridge Setup calls = %d, want only the first call", bridge.setupCalls)
	}
}

func newMacWorkflowTestApp(t *testing.T, store securestore.Store, service relayservice.Controller, bridge localBridgeController) *DesktopApp {
	t.Helper()
	app, err := newDesktopApp(context.Background(), desktopControllerConfig{
		Platform:     platformMacOS,
		Store:        store,
		Gateway:      contractGateway{},
		RelayService: service,
		Bridge:       bridge,
	})
	if err != nil {
		t.Fatal(err)
	}
	return app
}

type desktopRelayServiceFake struct {
	mu           sync.Mutex
	observations []relayservice.Observation
	setupGates   []relayservice.SetupGate
	rotateGates  []relayservice.RotateGate
	repairGates  []relayservice.RepairGate
	wait         relayservice.Observation
	observeCalls int
	setupCalls   int
	rotateCalls  int
	repairCalls  int
	waitCalls    int
}

func (service *desktopRelayServiceFake) Observe(context.Context) relayservice.Observation {
	service.mu.Lock()
	defer service.mu.Unlock()
	result := relayservice.Observation{State: relayservice.StateUnavailable}
	if service.observeCalls < len(service.observations) {
		result = service.observations[service.observeCalls]
	}
	service.observeCalls++
	return result
}

func (service *desktopRelayServiceFake) PrepareSetup(context.Context) relayservice.SetupGate {
	service.mu.Lock()
	defer service.mu.Unlock()
	result := relayservice.SetupGate{Observation: relayservice.Observation{State: relayservice.StateUnavailable}}
	if service.setupCalls < len(service.setupGates) {
		result = service.setupGates[service.setupCalls]
	}
	service.setupCalls++
	return result
}

func (service *desktopRelayServiceFake) GateRotate(context.Context) relayservice.RotateGate {
	service.mu.Lock()
	defer service.mu.Unlock()
	result := relayservice.RotateGate{Observation: relayservice.Observation{State: relayservice.StateUnavailable}}
	if service.rotateCalls < len(service.rotateGates) {
		result = service.rotateGates[service.rotateCalls]
	}
	service.rotateCalls++
	return result
}

func (service *desktopRelayServiceFake) GateRepair(context.Context) relayservice.RepairGate {
	service.mu.Lock()
	defer service.mu.Unlock()
	result := relayservice.RepairGate{Observation: relayservice.Observation{State: relayservice.StateUnavailable}}
	if service.repairCalls < len(service.repairGates) {
		result = service.repairGates[service.repairCalls]
	}
	service.repairCalls++
	return result
}

func (service *desktopRelayServiceFake) WaitForExactHelper(context.Context) relayservice.Observation {
	service.mu.Lock()
	defer service.mu.Unlock()
	service.waitCalls++
	return service.wait
}

type desktopBridgeSpy struct {
	mu           sync.Mutex
	setupCalls   int
	rotateCalls  int
	repairCalls  int
	setupEntered chan struct{}
	setupRelease chan struct{}
}

func (bridge *desktopBridgeSpy) Setup(ctx context.Context) (localbridge.BridgeStatus, error) {
	bridge.mu.Lock()
	bridge.setupCalls++
	entered, release := bridge.setupEntered, bridge.setupRelease
	bridge.mu.Unlock()
	if entered != nil {
		select {
		case entered <- struct{}{}:
		default:
		}
	}
	if release != nil {
		select {
		case <-ctx.Done():
			return localbridge.BridgeStatus{}, ctx.Err()
		case <-release:
		}
	}
	return localbridge.BridgeStatus{}, nil
}

func (bridge *desktopBridgeSpy) Rotate(context.Context, relayclient.Identity) (localbridge.BridgeStatus, relayclient.Identity, error) {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	bridge.rotateCalls++
	return localbridge.BridgeStatus{}, relayclient.Identity{}, nil
}

func (bridge *desktopBridgeSpy) Repair(context.Context) error {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	bridge.repairCalls++
	return nil
}
