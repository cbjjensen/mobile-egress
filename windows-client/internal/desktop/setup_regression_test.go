package desktop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mobile-egress/windows-client/internal/localbridge"
	"mobile-egress/windows-client/internal/relayservice"
	"mobile-egress/windows-client/internal/securestore"
	"mobile-egress/windows-client/internal/tailscale"
)

type setupStatusRunner struct{ remaining time.Duration }

func (runner *setupStatusRunner) Run(ctx context.Context, _ string, _ ...string) ([]byte, error) {
	deadline, _ := ctx.Deadline()
	runner.remaining = time.Until(deadline)
	return nil, errors.New("daemon unavailable")
}
func TestFailedStatusIsNotReportedAsMissingOrAReasonToReinstall(t *testing.T) {
	app := newMacWorkflowTestApp(t, securestore.NewMemoryStore(), &desktopRelayServiceFake{}, &desktopBridgeSpy{})
	executable := filepath.Join(t.TempDir(), "tailscale")
	if err := os.WriteFile(executable, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	runner := &setupStatusRunner{}
	app.tailscale = tailscale.NewController(executable, runner)
	view := app.GetBridgeStatus()
	if !view.TailscaleInstalled || view.TailscaleError == "" || view.Ready {
		t.Fatalf("status must identify an installed app with an unavailable check: %+v", view)
	}
	if runner.remaining < 14*time.Second {
		t.Fatal("Tailscale did not receive its own status-check budget")
	}
}

type resumableBridgeSpy struct {
	desktopBridgeSpy
	pending    bool
	pendingErr error
	failure    error
}

func (bridge *resumableBridgeSpy) HasPendingSetup(context.Context) (bool, error) {
	return bridge.pending, bridge.pendingErr
}
func (bridge *resumableBridgeSpy) Setup(ctx context.Context) (localbridge.BridgeStatus, error) {
	bridge.desktopBridgeSpy.Setup(ctx)
	return localbridge.BridgeStatus{}, bridge.failure
}
func TestInitializedRelayResumesOnlyWhenPendingSetupIsAvailable(t *testing.T) {
	for _, pending := range []bool{false, true} {
		service := &desktopRelayServiceFake{setupGates: []relayservice.SetupGate{{Observation: relayservice.Observation{State: relayservice.StateEnabled, StrictV1: true, ExactHelper: true, Initialized: true}, Decision: relayservice.SetupBlocked}}}
		bridge := &resumableBridgeSpy{pending: pending}
		app := newMacWorkflowTestApp(t, securestore.NewMemoryStore(), service, bridge)
		_, err := app.SetupLocalBridge()
		if pending && (err != nil || bridge.setupCalls != 1) {
			t.Fatalf("saved setup was not resumed: %v", err)
		}
		if !pending && (err == nil || !strings.Contains(err.Error(), "no saved Owner key") || bridge.setupCalls != 0) {
			t.Fatal("legacy missing key was not explained safely")
		}
	}
}
func TestMacSetupPreservesTheFailingStage(t *testing.T) {
	service := &desktopRelayServiceFake{setupGates: []relayservice.SetupGate{{Decision: relayservice.SetupProceed}}}
	failure := errors.New("Save pending bridge setup before contacting relay: secure storage unavailable")
	bridge := &resumableBridgeSpy{failure: failure}
	app := newMacWorkflowTestApp(t, securestore.NewMemoryStore(), service, bridge)
	_, err := app.SetupLocalBridge()
	if !errors.Is(err, failure) {
		t.Fatalf("Mac setup discarded the failure stage: %v", err)
	}
}
