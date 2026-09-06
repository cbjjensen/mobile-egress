package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"mobile-egress/windows-client/internal/awssdk"
	"mobile-egress/windows-client/internal/client"
	"mobile-egress/windows-client/internal/relayclient"
	"mobile-egress/windows-client/internal/securestore"
	"mobile-egress/windows-client/internal/tailscale"
)

type cancellableRepairBridge struct {
	desktopBridgeSpy
	entered chan context.Context
	release chan struct{}
}

func (bridge *cancellableRepairBridge) Repair(ctx context.Context) error {
	bridge.entered <- ctx
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-bridge.release:
		return nil
	}
}

func TestCancelSetupReachesOwnerBridgeRepairAndClearsAgentEvidence(t *testing.T) {
	store := securestore.NewMemoryStore()
	owner := relayclient.Identity{RelayURL: "https://relay.example", Role: "owner", Serial: "OWNER", PrivateKeyPEM: "key", CertificatePEM: "cert", CACertificatePEM: "ca"}
	if err := client.NewRepository(store).SaveOwnerIdentity(context.Background(), owner); err != nil {
		t.Fatal(err)
	}
	bridge := &cancellableRepairBridge{entered: make(chan context.Context, 1), release: make(chan struct{})}
	defer close(bridge.release)
	app, err := newDesktopApp(context.Background(), desktopControllerConfig{Platform: platformWindows, Store: store, Gateway: contractGateway{}, Bridge: bridge})
	if err != nil {
		t.Fatal(err)
	}
	paired := true
	app.monitor.publish(componentRelay, componentResult{agentConnected: true, agentPaired: &paired}, nil)
	done := make(chan error, 1)
	go func() { _, err := app.RepairLocalBridge(); done <- err }()
	var repairContext context.Context
	select {
	case repairContext = <-bridge.entered:
	case <-time.After(time.Second):
		t.Fatal("repair did not begin")
	}
	if app.GetSetupProgress().Stage != "bridge" {
		t.Fatal("owner repair did not expose active progress")
	}
	if view := app.GetBridgeStatus(); view.AgentConnected || view.AgentPaired != nil {
		t.Fatal("repair retained prior live Agent evidence")
	}
	if _, err := app.SetupLocalBridge(); err == nil {
		t.Fatal("repair allowed overlapping setup")
	}
	app.CancelSetup()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || !errors.Is(repairContext.Err(), context.Canceled) {
			t.Fatalf("repair cancellation = %v / %v", err, repairContext.Err())
		}
	case <-time.After(time.Second):
		t.Fatal("repair ignored cancellation")
	}
	if app.GetSetupProgress() != (SetupProgress{}) {
		t.Fatal("repair retained progress after returning")
	}
}

func TestSetupProgressCancellationRetainsExclusiveActionUntilReturn(t *testing.T) {
	app := &DesktopApp{}
	ctx, finish, err := app.beginSetup(time.Minute, "checking", "Checking Tailscale.")
	if err != nil {
		t.Fatal(err)
	}
	tailscale.ReportSetupProgress(ctx, "download", "Downloading Tailscale.")
	if app.GetSetupProgress().Stage != "download" {
		t.Fatal("missing operation progress")
	}
	var readers sync.WaitGroup
	for range 4 {
		readers.Go(func() {
			for range 100 {
				app.GetSetupProgress()
				app.CancelSetup()
			}
		})
	}
	readers.Wait()
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal("CancelSetup did not cancel operation context")
	}
	if _, _, err := app.beginSetup(time.Minute, "connect", "Connecting."); err == nil {
		t.Fatal("canceled action allowed overlapping native work")
	}
	tailscale.ReportSetupProgress(ctx, "install", "Installing.")
	if app.GetSetupProgress().Stage == "install" {
		t.Fatal("late callback overwrote cancellation")
	}
	finish()
	if app.GetSetupProgress() != (SetupProgress{}) {
		t.Fatal("finished progress was retained")
	}
	next, nextFinish, err := app.beginSetup(time.Minute, "connect", "Connecting.")
	if err != nil {
		t.Fatal(err)
	}
	defer nextFinish()
	finish()
	tailscale.ReportSetupProgress(ctx, "install", "Old action.")
	if next.Err() != nil || app.GetSetupProgress().Stage != "connect" {
		t.Fatal("old action interfered with new action")
	}
}

func TestCancelSetupReachesActiveBridgeWithoutStartingAnotherAction(t *testing.T) {
	bridge := &desktopBridgeSpy{setupEntered: make(chan struct{}, 1), setupRelease: make(chan struct{})}
	app := &DesktopApp{bridge: bridge}
	done := make(chan error, 1)
	go func() { _, err := app.SetupLocalBridge(); done <- err }()
	select {
	case <-bridge.setupEntered:
	case <-time.After(time.Second):
		t.Fatal("bridge setup did not begin")
	}
	if app.GetSetupProgress().Stage != "bridge" {
		t.Fatal("bridge action did not expose progress")
	}
	if _, err := app.ConnectTailscale(); err == nil {
		t.Fatal("overlapping action accepted")
	}
	app.CancelSetup()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("setup cancellation = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("bridge setup did not observe cancellation")
	}
	if app.GetSetupProgress() != (SetupProgress{}) {
		t.Fatal("completed operation progress retained")
	}
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	if bridge.setupCalls != 1 {
		t.Fatal("cancellation repeated bridge setup")
	}
}

func TestSnapshotAWSConfiguredUsesMemoryAndNoCredentialMaterial(t *testing.T) {
	app := &DesktopApp{}
	if app.GetControllerSnapshot().AWSConfigured {
		t.Fatal("missing AWS client reported configured")
	}
	app.mu.Lock()
	app.awsClient = &awssdk.Client{}
	app.mu.Unlock()
	raw, err := json.Marshal(app.GetControllerSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	var snapshot map[string]any
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot["awsConfigured"] != true {
		t.Fatal("configured AWS client absent from snapshot")
	}
}

func TestSnapshotAgentEvidenceRequiresFreshSuccessfulRelayObservation(t *testing.T) {
	now := time.Now()
	m := newStatusMonitor(platformWindows, nil)
	m.now = func() time.Time { return now }
	paired := true
	result := componentResult{agentConnected: true, agentPaired: &paired}
	m.publish(componentRelay, result, nil)
	if bridge := m.snapshot().Bridge; !bridge.AgentConnected || bridge.AgentPaired == nil || !*bridge.AgentPaired {
		t.Fatal("fresh evidence missing")
	}
	now = now.Add(12 * time.Second)
	if bridge := m.snapshot().Bridge; bridge.AgentConnected || bridge.AgentPaired != nil {
		t.Fatal("stale observation reported agent success")
	}
	m.publish(componentRelay, result, nil)
	m.publish(componentRelay, componentResult{}, errors.New("offline"))
	if bridge := m.snapshot().Bridge; bridge.AgentConnected || bridge.AgentPaired != nil {
		t.Fatal("failed observation retained agent success")
	}
	m.publish(componentRelay, componentResult{agentConnected: true}, nil)
	if bridge := m.snapshot().Bridge; !bridge.AgentConnected || bridge.AgentPaired != nil {
		t.Fatal("older relay missing field was not unknown")
	}
}
