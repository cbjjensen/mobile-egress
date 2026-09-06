package desktop

import (
	"context"
	"errors"
	"mobile-egress/windows-client/internal/tailscale"
	"sync/atomic"
	"testing"
	"time"
)

func awaitMonitor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("monitor condition timed out")
		}
		time.Sleep(time.Millisecond)
	}
}

// Tests of collector output deliberately collect without starting a scheduler;
// display bindings themselves must stay free of dependency operations.
func collectControllerForTest(t *testing.T, app *DesktopApp) {
	t.Helper()
	if app.monitor == nil {
		app.monitor = app.newStatusMonitor()
	}
	for _, key := range controllerComponents {
		timeout := 5 * time.Second
		if key == componentTailscale {
			timeout = 15 * time.Second
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		result, err := app.monitor.checks[key](ctx)
		cancel()
		app.monitor.publish(key, result, err)
	}
}

func TestMonitorIndependentSingleFlightAndSnapshotReads(t *testing.T) {
	blocked, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	checks := monitorChecks{}
	checks[componentTailscale] = func(context.Context) (componentResult, error) {
		if calls.Add(1) == 1 {
			close(blocked)
		}
		<-release
		return componentResult{}, nil
	}
	checks[componentHelper] = func(context.Context) (componentResult, error) {
		return componentResult{helper: relayServiceEnabled}, nil
	}
	m := newStatusMonitor(platformMacOS, checks)
	m.start(context.Background())
	defer m.stop(time.Second)
	<-blocked
	awaitMonitor(t, func() bool { return !m.snapshot().Components[componentHelper].Checking })
	app := &DesktopApp{platform: platformMacOS, monitor: m}
	for range 100 {
		_ = app.GetBridgeStatus()
		_ = app.GetControllerSnapshot()
		m.request(componentTailscale)
	}
	if calls.Load() != 1 {
		t.Fatal("overlapping collection")
	}
	close(release)
	awaitMonitor(t, func() bool { return calls.Load() == 2 })
	if calls.Load() != 2 {
		t.Fatal("requests were not coalesced")
	}
}

func TestMonitorFreshnessRetainsInformationAndExcludesFailures(t *testing.T) {
	m := newStatusMonitor(platformWindows, monitorChecks{})
	if !m.snapshot().Components[componentTailscale].Checking {
		t.Fatal("initial state is not Checking")
	}
	now := time.Now()
	m.now = func() time.Time { return now }
	m.publish(componentTailscale, componentResult{}, nil)
	now = now.Add(12 * time.Second)
	if !m.snapshot().Components[componentTailscale].Stale {
		t.Fatal("success did not become stale")
	}
	m.publish(componentHelper, componentResult{helper: relayServiceEnabled}, nil)
	m.publish(componentHelper, componentResult{}, errors.New("sensitive dependency detail"))
	snapshot := m.snapshot()
	if snapshot.Bridge.RelayServiceState != "enabled" || snapshot.Components[componentHelper].Error == "" || snapshot.Bridge.Ready {
		t.Fatalf("bad failed snapshot: %+v", snapshot)
	}
	if snapshot.Components[componentHelper].Error == "sensitive dependency detail" {
		t.Fatal("raw dependency error exposed")
	}
}

func TestMonitorMutationRejectsObsoleteResults(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	checks := monitorChecks{}
	checks[componentHelper] = func(context.Context) (componentResult, error) {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
			return componentResult{helper: relayServiceEnabled}, nil
		}
		return componentResult{helper: relayServiceApprovalRequired}, nil
	}
	m := newStatusMonitor(platformMacOS, checks)
	m.start(context.Background())
	defer m.stop(time.Second)
	<-entered
	finish := m.beginAction(componentHelper)
	m.publish(componentHelper, componentResult{helper: relayServiceApprovalRequired}, nil)
	close(release)
	awaitMonitor(t, func() bool { m.mu.Lock(); defer m.mu.Unlock(); return !m.components[componentHelper].running })
	if m.snapshot().Bridge.RelayServiceState != "approval-required" {
		t.Fatal("old check overwrote action")
	}
	finish()
	awaitMonitor(t, func() bool { return calls.Load() == 2 })
}

func TestMonitorActionCannotRestoreOldReadiness(t *testing.T) {
	m := newStatusMonitor(platformWindows, monitorChecks{})
	for _, key := range controllerComponents {
		m.publish(key, componentResult{}, nil)
	}
	m.publish(componentHelper, componentResult{helper: relayServiceNotRequired}, nil)
	m.publish(componentTailscale, componentResult{tailscale: tailscale.Status{Online: true, FunnelReady: true, PublicURL: "https://relay.example"}}, nil)
	m.publish(componentRelay, componentResult{ownerReady: true, ownerURL: "https://relay.example", relayReady: true}, nil)
	if !m.snapshot().Bridge.Ready {
		t.Fatal("fixture not ready")
	}
	finish := m.beginAction(componentRelay)
	finish()
	if m.snapshot().Bridge.Ready {
		t.Fatal("pre-action success restored readiness")
	}
	m.publish(componentRelay, componentResult{ownerReady: true, ownerURL: "https://relay.example", relayReady: true}, nil)
	if !m.snapshot().Bridge.Ready {
		t.Fatal("new success did not restore readiness")
	}
}
