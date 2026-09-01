package adminservice

import (
	"bytes"
	"context"
	"errors"
	"net"
	"path/filepath"
	"sync/atomic"
	"testing"

	"mobile-egress/internal/relayadmin"
	"mobile-egress/relay/internal/service"
)

func TestMutationFinishedCallbackReconcilesOnlyReadyStateAndSwallowsListenerFailures(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "Relay")
	var listenCalls atomic.Int32
	supervisor, err := NewSupervisor(SupervisorConfig{
		StateDir: stateDir,
		Address:  "127.0.0.1:8443",
		Listen: func(string, string) (net.Listener, error) {
			listenCalls.Add(1)
			return nil, errors.New("RAW_RECONCILE_SECRET")
		},
		Open: func(path string) (RelayInstance, error) { return service.Open(path) },
	})
	if err != nil {
		t.Fatal(err)
	}
	var state *service.AdminState
	finished, err := newMutationFinishedCallback(func() *service.AdminState { return state }, supervisor)
	if err != nil {
		t.Fatalf("newMutationFinishedCallback() error = %v", err)
	}
	state, err = service.OpenAdminState(service.AdminStateOptions{StateDir: stateDir, MutationFinished: finished})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })
	handler, err := NewHandler(HandlerConfig{
		State: state, Supervisor: supervisor, AdminGID: testAdminGID, HelperVersion: testHelperVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := &relayadmin.Server{Authorize: handler.Authorize, Handler: handler, Replay: state.ReplayStore()}
	admin := relayadmin.NewPeer(testOwnerUID, []uint32{testAdminGID})

	failedSetup := mustAdminRequest(t, "a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1", relayadmin.OperationSetup, relayadmin.SetupRequest{
		PublicName: "relay.example.test", PublicURL: "https://relay.example.test:8443", OwnerCSRPEM: "RAW_BAD_CSR",
	})
	failedRaw, _ := exchangeAdminServer(t, server, admin, failedSetup)
	if response := mustAdminResponse(t, failedRaw); response.OK || response.ErrorCode != relayadmin.ErrorOperationFailed {
		t.Fatalf("failed setup response = %#v", response)
	}
	if got := listenCalls.Load(); got != 0 {
		t.Fatalf("absent-state completion attempted %d reconciles, want 0", got)
	}

	setupCSR := newAdminCSR(t, "owner-success-csr-secret")
	setup := mustAdminRequest(t, "a2a2a2a2a2a2a2a2a2a2a2a2a2a2a2a2", relayadmin.OperationSetup, relayadmin.SetupRequest{
		PublicName: "relay.example.test", PublicURL: "https://relay.example.test:8443", OwnerCSRPEM: setupCSR,
	})
	setupRaw, _ := exchangeAdminServer(t, server, admin, setup)
	if response := mustAdminResponse(t, setupRaw); !response.OK {
		t.Fatalf("setup response = %#v", response)
	}
	if durable := adminReplayResponse(t, stateDir, "a2a2a2a2a2a2a2a2a2a2a2a2a2a2a2a2"); !bytes.Equal(durable, setupRaw) {
		t.Fatal("durable setup replay differs from the typed response frame")
	}
	for _, forbidden := range []string{setupCSR, "relay.example.test", "PRIVATE KEY"} {
		if bytes.Contains(setupRaw, []byte(forbidden)) {
			t.Fatalf("typed setup response exposed forbidden value %q", forbidden)
		}
	}
	if got := listenCalls.Load(); got != 1 {
		t.Fatalf("ready setup attempted %d reconciles, want 1", got)
	}
	setupCached, _ := exchangeAdminServer(t, server, admin, setup)
	if !bytes.Equal(setupCached, setupRaw) {
		t.Fatal("setup listener failure changed cached completed response")
	}

	rotate := mustAdminRequest(t, "a3a3a3a3a3a3a3a3a3a3a3a3a3a3a3a3", relayadmin.OperationRotate, relayadmin.RotateRequest{
		PublicName: "rotated.example.test", PublicURL: "https://rotated.example.test:8443",
	})
	rotateRaw, _ := exchangeAdminServer(t, server, admin, rotate)
	rotateResponse := mustAdminResponse(t, rotateRaw)
	if !rotateResponse.OK {
		t.Fatalf("rotate response = %#v", rotateResponse)
	}
	rotated, ok := rotateResponse.Result.(relayadmin.EndpointRotationResult)
	if !ok || rotated.PublicURL != "https://rotated.example.test:8443" || rotated.Serial == "" {
		t.Fatalf("rotate result = %#v", rotateResponse.Result)
	}
	assertExactAdminJSONKeys(t, rotateRaw,
		[]string{"version", "requestId", "operation", "ok", "result"},
		[]string{"publicUrl", "serial"},
	)
	if durable := adminReplayResponse(t, stateDir, "a3a3a3a3a3a3a3a3a3a3a3a3a3a3a3a3"); !bytes.Equal(durable, rotateRaw) {
		t.Fatal("durable rotate replay differs from the typed response frame")
	}
	if got := listenCalls.Load(); got != 2 {
		t.Fatalf("ready rotate attempted %d reconciles, want 2", got)
	}
	rotateCached, _ := exchangeAdminServer(t, server, admin, rotate)
	if !bytes.Equal(rotateCached, rotateRaw) {
		t.Fatal("rotate listener failure changed cached completed response")
	}

	repair := mustAdminRequest(t, "a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4", relayadmin.OperationRepair, relayadmin.RepairRequest{})
	repairRaw, repairOutcome := exchangeAdminServer(t, server, admin, repair)
	repairResponse := mustAdminResponse(t, repairRaw)
	repaired, ok := repairResponse.Result.(relayadmin.RepairResult)
	if !repairResponse.OK || !ok || repaired != (relayadmin.RepairResult{Ready: true, Restarting: true}) {
		t.Fatalf("repair response = %#v", repairResponse)
	}
	if !repairOutcome.RepairRestartReady {
		t.Fatal("fully flushed successful repair did not request restart")
	}
	assertExactAdminJSONKeys(t, repairRaw,
		[]string{"version", "requestId", "operation", "ok", "result"},
		[]string{"ready", "restarting"},
	)
	if durable := adminReplayResponse(t, stateDir, "a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4"); !bytes.Equal(durable, repairRaw) {
		t.Fatal("durable repair replay differs from the typed response frame")
	}
	repairCached, cachedOutcome := exchangeAdminServer(t, server, admin, repair)
	if !bytes.Equal(repairCached, repairRaw) || !cachedOutcome.RepairRestartReady {
		t.Fatal("cached repair did not preserve exact response and restart outcome")
	}
	if got := listenCalls.Load(); got != 3 {
		t.Fatalf("ready repair attempted %d total reconciles, want 3", got)
	}
	if supervisor.Snapshot().RelayRunning {
		t.Fatal("failed reconciliation reported relay running")
	}
	status, err := state.Snapshot(context.Background())
	if err != nil || status.Class != service.AdminStateReady {
		t.Fatalf("durable state after listener failures = %#v, %v", status, err)
	}
	for _, requestID := range []string{
		"a2a2a2a2a2a2a2a2a2a2a2a2a2a2a2a2",
		"a3a3a3a3a3a3a3a3a3a3a3a3a3a3a3a3",
		"a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4",
	} {
		durable := adminReplayResponse(t, stateDir, requestID)
		for _, forbidden := range []string{
			"PRIVATE KEY", "RAW_BAD_CSR", "RAW_RECONCILE_SECRET", stateDir,
			"AWS_SECRET_ACCESS_KEY", "nodeMetadata", "administrativeOwnerUid",
		} {
			if bytes.Contains(durable, []byte(forbidden)) {
				t.Fatalf("durable replay %s exposed forbidden value %q", requestID, forbidden)
			}
		}
	}
}

func TestMutationFinishedCallbackRejectsInvalidConstructionAndToleratesLateNilState(t *testing.T) {
	t.Parallel()

	supervisor, err := NewSupervisor(SupervisorConfig{
		StateDir: "state", Address: "127.0.0.1:8443",
		Listen: net.Listen,
		Open:   func(string) (RelayInstance, error) { return nil, errors.New("unused") },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newMutationFinishedCallback(nil, supervisor); err == nil {
		t.Fatal("nil state getter was accepted")
	}
	if _, err := newMutationFinishedCallback(func() *service.AdminState { return nil }, nil); err == nil {
		t.Fatal("nil supervisor was accepted")
	}
	callback, err := newMutationFinishedCallback(func() *service.AdminState { return nil }, supervisor)
	if err != nil {
		t.Fatal(err)
	}
	callback(relayadmin.ReplayKey{})
	if supervisor.Snapshot().RelayRunning {
		t.Fatal("late nil state started relay")
	}
}
