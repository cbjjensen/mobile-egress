package adminservice

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"mobile-egress/internal/relayadmin"
	"mobile-egress/relay/internal/service"
)

const (
	testAdminGID      = uint32(80)
	testOwnerUID      = uint32(501)
	testHelperVersion = "1.1.0-test"
)

func TestHandlerStatusSetupAndCachedSuccessAreTypedAndRedacted(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "Relay")
	var supervisor *Supervisor
	var reconcileMu sync.Mutex
	var reconcileErrors []error
	state, err := service.OpenAdminState(service.AdminStateOptions{
		StateDir: stateDir,
		MutationFinished: func(relayadmin.ReplayKey) {
			if supervisor == nil {
				return
			}
			reconcileErr := supervisor.Reconcile(context.Background())
			reconcileMu.Lock()
			reconcileErrors = append(reconcileErrors, reconcileErr)
			reconcileMu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("OpenAdminState() error = %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	rawRuntimeError := "RAW_RELAY_BIND_SECRET"
	supervisor, err = NewSupervisor(SupervisorConfig{
		StateDir: stateDir,
		Address:  "127.0.0.1:8443",
		Listen: func(string, string) (net.Listener, error) {
			return nil, errors.New(rawRuntimeError)
		},
		Open: func(path string) (RelayInstance, error) {
			return service.Open(path)
		},
	})
	if err != nil {
		t.Fatalf("NewSupervisor() error = %v", err)
	}
	handler, err := NewHandler(HandlerConfig{
		State: state, Supervisor: supervisor, AdminGID: testAdminGID, HelperVersion: testHelperVersion,
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	server := &relayadmin.Server{Authorize: handler.Authorize, Handler: handler, Replay: state.ReplayStore()}
	admin := relayadmin.NewPeer(testOwnerUID, []uint32{20, testAdminGID})

	statusRequest := mustAdminRequest(t, "10101010101010101010101010101010", relayadmin.OperationStatus, relayadmin.StatusRequest{})
	statusRaw, _ := exchangeAdminServer(t, server, admin, statusRequest)
	status := mustAdminResponse(t, statusRaw)
	statusResult, ok := status.Result.(relayadmin.StatusResult)
	if !ok {
		t.Fatalf("status result type = %T", status.Result)
	}
	if statusResult != (relayadmin.StatusResult{
		ProtocolVersion: relayadmin.Version, HelperVersion: testHelperVersion, Initialized: false, RelayRunning: false,
	}) {
		t.Fatalf("status = %#v", statusResult)
	}
	assertExactAdminJSONKeys(t, statusRaw,
		[]string{"version", "requestId", "operation", "ok", "result"},
		[]string{"protocolVersion", "helperVersion", "initialized", "relayRunning"},
	)

	csr := newAdminCSR(t, "OWNER_CSR_SHOULD_NOT_RETURN")
	setupRequest := mustAdminRequest(t, "20202020202020202020202020202020", relayadmin.OperationSetup, relayadmin.SetupRequest{
		PublicName: "relay.example.test", PublicURL: "https://relay.example.test:8443", OwnerCSRPEM: csr,
	})
	setupRaw, _ := exchangeAdminServer(t, server, admin, setupRequest)
	setup := mustAdminResponse(t, setupRaw)
	owner, ok := setup.Result.(relayadmin.OwnerBootstrapResult)
	if !ok || !setup.OK {
		t.Fatalf("setup response = %#v", setup)
	}
	if owner.CertificatePEM == "" || owner.CACertificatePEM == "" || owner.Serial == "" || owner.Role != "owner" {
		t.Fatalf("setup result missing public Owner material: %#v", owner)
	}
	assertExactAdminJSONKeys(t, setupRaw,
		[]string{"version", "requestId", "operation", "ok", "result"},
		[]string{"certificatePem", "caCertificatePem", "serial", "role"},
	)
	for _, forbidden := range []string{
		"PRIVATE KEY", "OWNER_CSR_SHOULD_NOT_RETURN", csr, "relay.example.test", rawRuntimeError,
		stateDir, "AWS_SECRET_ACCESS_KEY", "nodeMetadata", "administrativeOwnerUid",
	} {
		if bytes.Contains(setupRaw, []byte(forbidden)) {
			t.Fatalf("setup response exposed forbidden value %q", forbidden)
		}
	}

	setupCached, _ := exchangeAdminServer(t, server, admin, setupRequest)
	if !bytes.Equal(setupCached, setupRaw) {
		t.Fatal("cached setup response changed after failed postcommit reconcile")
	}
	if supervisor.Snapshot().RelayRunning {
		t.Fatal("failed postcommit reconcile reported relay running")
	}
	reconcileMu.Lock()
	if len(reconcileErrors) != 1 || reconcileErrors[0] == nil {
		t.Fatalf("reconcile errors = %v, want one failure", reconcileErrors)
	}
	reconcileMu.Unlock()

	readyStatusRequest := mustAdminRequest(t, "30303030303030303030303030303030", relayadmin.OperationStatus, relayadmin.StatusRequest{})
	readyStatusRaw, _ := exchangeAdminServer(t, server, admin, readyStatusRequest)
	readyStatus := mustAdminResponse(t, readyStatusRaw)
	readyResult := readyStatus.Result.(relayadmin.StatusResult)
	if !readyResult.Initialized || readyResult.RelayRunning {
		t.Fatalf("ready status = %#v", readyResult)
	}
	for _, forbidden := range []string{stateDir, "relay.example.test", "8443", "501", rawRuntimeError, "uid", "path"} {
		if bytes.Contains(bytes.ToLower(readyStatusRaw), bytes.ToLower([]byte(forbidden))) {
			t.Fatalf("status response exposed forbidden value %q: %s", forbidden, readyStatusRaw)
		}
	}
}

func TestHandlerMapsOnlyFixedStateErrorsAndRedactsRawFailures(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "Relay")
	state, supervisor, handler := newDormantAdminHandler(t, stateDir, nil)
	server := &relayadmin.Server{Authorize: handler.Authorize, Handler: handler, Replay: state.ReplayStore()}
	admin := relayadmin.NewPeer(testOwnerUID, []uint32{testAdminGID})
	root := relayadmin.NewPeer(0, []uint32{0})

	tests := []struct {
		name      string
		peer      relayadmin.Peer
		requestID string
		operation relayadmin.Operation
		params    any
		want      relayadmin.ErrorCode
	}{
		{"absent rotate", admin, "40404040404040404040404040404040", relayadmin.OperationRotate, relayadmin.RotateRequest{PublicName: "relay.example.test", PublicURL: "https://relay.example.test:8443"}, relayadmin.ErrorNotInitialized},
		{"absent repair root", root, "50505050505050505050505050505050", relayadmin.OperationRepair, relayadmin.RepairRequest{}, relayadmin.ErrorNotInitialized},
		{"raw setup failure", admin, "60606060606060606060606060606060", relayadmin.OperationSetup, relayadmin.SetupRequest{PublicName: "relay.example.test", PublicURL: "https://relay.example.test:8443", OwnerCSRPEM: "RAW_CSR_SECRET"}, relayadmin.ErrorOperationFailed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw, _ := exchangeAdminServer(t, server, test.peer, mustAdminRequest(t, test.requestID, test.operation, test.params))
			response := mustAdminResponse(t, raw)
			if response.OK || response.ErrorCode != test.want {
				t.Fatalf("response = %#v, want %q", response, test.want)
			}
			if bytes.Contains(raw, []byte("RAW_CSR_SECRET")) || bytes.Contains(raw, []byte(stateDir)) {
				t.Fatalf("error response exposed raw input/path: %s", raw)
			}
		})
	}
	if supervisor.Snapshot().RelayRunning {
		t.Fatal("dormant supervisor unexpectedly running")
	}

	setupRequest := relayadmin.SetupRequest{
		PublicName: "relay.example.test", PublicURL: "https://relay.example.test:8443", OwnerCSRPEM: newAdminCSR(t, "owner"),
	}
	raw, _ := exchangeAdminServer(t, server, admin, mustAdminRequest(t, "70707070707070707070707070707070", relayadmin.OperationSetup, setupRequest))
	if response := mustAdminResponse(t, raw); !response.OK {
		t.Fatalf("valid setup failed: %#v", response)
	}
	raw, _ = exchangeAdminServer(t, server, admin, mustAdminRequest(t, "80808080808080808080808080808080", relayadmin.OperationSetup, setupRequest))
	if response := mustAdminResponse(t, raw); response.OK || response.ErrorCode != relayadmin.ErrorAlreadyInitialized {
		t.Fatalf("second setup response = %#v", response)
	}
}

func TestHandlerConcurrentFirstSetupRechecksAuthorizationInsideReservation(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "Relay")
	state, _, handler := newDormantAdminHandler(t, stateDir, nil)
	peerA := relayadmin.NewPeer(501, []uint32{testAdminGID})
	peerB := relayadmin.NewPeer(502, []uint32{testAdminGID})
	requestARaw := mustAdminRequest(t, "91919191919191919191919191919191", relayadmin.OperationSetup, relayadmin.SetupRequest{
		PublicName: "relay.example.test", PublicURL: "https://relay.example.test:8443", OwnerCSRPEM: newAdminCSR(t, "owner-a"),
	})
	requestA, err := relayadmin.ParseRequest(requestARaw)
	if err != nil {
		t.Fatal(err)
	}
	requestBRaw := mustAdminRequest(t, "92929292929292929292929292929292", relayadmin.OperationSetup, relayadmin.SetupRequest{
		PublicName: "relay.example.test", PublicURL: "https://relay.example.test:8443", OwnerCSRPEM: newAdminCSR(t, "owner-b"),
	})
	requestB, err := relayadmin.ParseRequest(requestBRaw)
	if err != nil {
		t.Fatal(err)
	}
	gates := map[string]*replayReserveGate{
		requestA.RequestID: newReplayReserveGate(),
		requestB.RequestID: newReplayReserveGate(),
	}
	store := &gatedReplayStore{ReplayStore: state.ReplayStore(), gates: gates}
	loserEnteredHandler := make(chan struct{})
	observed := &observedSetupHandler{
		Handler: handler, observedUID: peerB.UID(), entered: loserEnteredHandler,
	}
	server := &relayadmin.Server{
		Authorize: handler.Authorize, Handler: observed, Replay: store,
		OperationLimit: 2 * time.Second, IOLimit: 2 * time.Second,
	}
	connectionA := newScriptedAdminConn(t, requestARaw, -1)
	connectionB := newScriptedAdminConn(t, requestBRaw, -1)
	doneA := make(chan relayadmin.ServeOutcome, 1)
	doneB := make(chan relayadmin.ServeOutcome, 1)
	go func() { doneA <- server.ServeConn(context.Background(), connectionA, peerA) }()
	go func() { doneB <- server.ServeConn(context.Background(), connectionB, peerB) }()
	waitSignal(t, gates[requestA.RequestID].entered, "UID A absent-state precheck")
	waitSignal(t, gates[requestB.RequestID].entered, "UID B absent-state precheck")

	// Both requests reached Replay.Reserve only after the server's authorization
	// precheck while state was absent. Let A reserve/execute/commit first.
	close(gates[requestA.RequestID].release)
	waitSignal(t, gates[requestA.RequestID].reserved, "UID A mutation reservation")
	waitAdminServeOutcome(t, doneA, "UID A setup")
	if !connectionA.isClosed() {
		t.Fatal("UID A connection remained open after ServeConn")
	}
	parsedA := scriptedAdminResponse(t, connectionA)
	if !parsedA.OK {
		t.Fatalf("administrator A response = %#v", parsedA)
	}
	if got := countAdminOwnerIdentities(t, stateDir); got != 1 {
		t.Fatalf("Owner identities after UID A = %d, want 1", got)
	}

	// B's precheck is already behind it. Its reservation begins against the now
	// ready state, and its handler callback is invoked only from Execute while
	// AdminState owns the mutation gate. The handler's second snapshot must deny
	// B before any second Owner work occurs.
	close(gates[requestB.RequestID].release)
	waitSignal(t, gates[requestB.RequestID].reserved, "UID B mutation reservation")
	waitSignal(t, loserEnteredHandler, "UID B in-gate handler callback")
	waitAdminServeOutcome(t, doneB, "UID B setup")
	if !connectionB.isClosed() {
		t.Fatal("UID B connection remained open after ServeConn")
	}
	parsedB := scriptedAdminResponse(t, connectionB)
	if parsedB.OK || parsedB.ErrorCode != relayadmin.ErrorUnauthorized {
		t.Fatalf("administrator B response = %#v", parsedB)
	}
	if got := countAdminOwnerIdentities(t, stateDir); got != 1 {
		t.Fatalf("Owner identities after losing UID B = %d, want exactly 1", got)
	}
	snapshot, err := state.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Class != service.AdminStateReady || !snapshot.OwnerUIDBound || snapshot.AdministrativeOwnerUID != peerA.UID() {
		t.Fatalf("snapshot = %#v, want exactly UID A", snapshot)
	}
}

func TestHandlerAuthorizeDeniesClosedStateAndRootFirstSetup(t *testing.T) {
	t.Parallel()

	state, _, handler := newDormantAdminHandler(t, filepath.Join(t.TempDir(), "Relay"), nil)
	root := relayadmin.NewPeer(0, []uint32{0, testAdminGID})
	if handler.Authorize(context.Background(), root, relayadmin.OperationSetup) {
		t.Fatal("root was authorized for first setup")
	}
	if !handler.Authorize(context.Background(), root, relayadmin.OperationStatus) {
		t.Fatal("root was denied absent-state status")
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	if handler.Authorize(context.Background(), root, relayadmin.OperationStatus) {
		t.Fatal("closed-state snapshot error was authorized")
	}
}

func TestHandlerUnsafeStateExposesOnlyFixedRootStatusAndRepairErrors(t *testing.T) {
	t.Parallel()

	const rawGuardError = "RAW_PATH_GUARD_SECRET"
	stateDir := filepath.Join(t.TempDir(), "Relay")
	state, _, handler := newDormantAdminHandler(t, stateDir, failingAdminPathGuard{
		validateErr: errors.New(rawGuardError), repairErr: errors.New(rawGuardError),
	})
	server := &relayadmin.Server{Authorize: handler.Authorize, Handler: handler, Replay: state.ReplayStore()}
	root := relayadmin.NewPeer(0, []uint32{0})
	admin := relayadmin.NewPeer(testOwnerUID, []uint32{testAdminGID})

	statusRaw, _ := exchangeAdminServer(t, server, root, mustAdminRequest(t,
		"93939393939393939393939393939393", relayadmin.OperationStatus, relayadmin.StatusRequest{}))
	status := mustAdminResponse(t, statusRaw)
	if status.OK || status.ErrorCode != relayadmin.ErrorStateIncompatible {
		t.Fatalf("unsafe root status = %#v", status)
	}
	assertExactAdminErrorKeys(t, statusRaw)

	nonrootRaw, _ := exchangeAdminServer(t, server, admin, mustAdminRequest(t,
		"94949494949494949494949494949494", relayadmin.OperationStatus, relayadmin.StatusRequest{}))
	if response := mustAdminResponse(t, nonrootRaw); response.OK || response.ErrorCode != relayadmin.ErrorUnauthorized {
		t.Fatalf("unsafe nonroot status = %#v", response)
	}
	assertExactAdminErrorKeys(t, nonrootRaw)

	repairRaw, outcome := exchangeAdminServer(t, server, root, mustAdminRequest(t,
		"95959595959595959595959595959595", relayadmin.OperationRepair, relayadmin.RepairRequest{}))
	repair := mustAdminResponse(t, repairRaw)
	if repair.OK || repair.ErrorCode != relayadmin.ErrorOperationFailed || outcome.RepairRestartReady {
		t.Fatalf("unsafe root repair = %#v, outcome %#v", repair, outcome)
	}
	assertExactAdminErrorKeys(t, repairRaw)

	setupRaw, _ := exchangeAdminServer(t, server, root, mustAdminRequest(t,
		"96969696969696969696969696969696", relayadmin.OperationSetup, relayadmin.SetupRequest{
			PublicName: "forbidden.example.test", PublicURL: "https://forbidden.example.test:8443", OwnerCSRPEM: "RAW_CSR",
		}))
	if response := mustAdminResponse(t, setupRaw); response.OK || response.ErrorCode != relayadmin.ErrorUnauthorized {
		t.Fatalf("unsafe root setup = %#v", response)
	}
	assertExactAdminErrorKeys(t, setupRaw)

	for _, raw := range [][]byte{statusRaw, nonrootRaw, repairRaw, setupRaw} {
		for _, forbidden := range []string{rawGuardError, stateDir, "RAW_CSR", "forbidden.example.test", "AWS_SECRET_ACCESS_KEY", "nodeMetadata"} {
			if bytes.Contains(raw, []byte(forbidden)) {
				t.Fatalf("fixed error response exposed %q: %s", forbidden, raw)
			}
		}
	}
}

func TestHandlerSerializesMutationCallbacksWhileStatusSnapshotsRemainRaceSafe(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "Relay")
	state, _, handler := newDormantAdminHandler(t, stateDir, nil)
	observed := &serializedMutationHandler{Handler: handler, delay: 3 * time.Millisecond}
	server := &relayadmin.Server{
		Authorize: handler.Authorize, Handler: observed, Replay: state.ReplayStore(),
		OperationLimit: 20 * time.Second, IOLimit: 20 * time.Second,
	}
	owner := relayadmin.NewPeer(testOwnerUID, []uint32{testAdminGID})
	setupRaw, _ := exchangeAdminServer(t, server, owner, mustAdminRequest(t,
		"97979797979797979797979797979797", relayadmin.OperationSetup, relayadmin.SetupRequest{
			PublicName: "relay.example.test", PublicURL: "https://relay.example.test:8443", OwnerCSRPEM: newAdminCSR(t, "owner"),
		}))
	if response := mustAdminResponse(t, setupRaw); !response.OK {
		t.Fatalf("initial setup response = %#v", response)
	}

	type requestCase struct {
		operation relayadmin.Operation
		params    any
	}
	requests := make([]requestCase, 0, 14)
	for index := 0; index < 4; index++ {
		requests = append(requests,
			requestCase{operation: relayadmin.OperationRotate, params: relayadmin.RotateRequest{
				PublicName: fmt.Sprintf("relay-%d.example.test", index),
				PublicURL:  fmt.Sprintf("https://relay-%d.example.test:8443", index),
			}},
			requestCase{operation: relayadmin.OperationRepair, params: relayadmin.RepairRequest{}},
		)
	}
	for index := 0; index < 6; index++ {
		requests = append(requests, requestCase{operation: relayadmin.OperationStatus, params: relayadmin.StatusRequest{}})
	}

	completed := make(chan *scriptedAdminConn, len(requests))
	for index, request := range requests {
		raw := mustAdminRequest(t, fmt.Sprintf("%032x", 0x1000+index), request.operation, request.params)
		connection := newScriptedAdminConn(t, raw, -1)
		go func() {
			server.ServeConn(context.Background(), connection, owner)
			completed <- connection
		}()
	}
	for range requests {
		select {
		case connection := <-completed:
			response := scriptedAdminResponse(t, connection)
			if !response.OK {
				t.Fatalf("concurrent %s response = %#v", response.Operation, response)
			}
			if response.Operation == relayadmin.OperationStatus {
				status, ok := response.Result.(relayadmin.StatusResult)
				if !ok || !status.Initialized {
					t.Fatalf("concurrent status result = %#v", response.Result)
				}
			}
		case <-time.After(30 * time.Second):
			t.Fatal("timed out waiting for serialized mutation/status stress")
		}
	}
	if observed.overlap.Load() {
		t.Fatal("setup/rotate/repair callbacks overlapped inside AdminState mutation execution")
	}
	if maximum := observed.maximum.Load(); maximum != 1 {
		t.Fatalf("maximum concurrent mutation callbacks = %d, want 1", maximum)
	}
	snapshot, err := state.Snapshot(context.Background())
	if err != nil || snapshot.Class != service.AdminStateReady || snapshot.AdministrativeOwnerUID != testOwnerUID {
		t.Fatalf("post-stress snapshot = %#v, %v", snapshot, err)
	}
}

func newDormantAdminHandler(t *testing.T, stateDir string, guard service.AdminPathGuard) (*service.AdminState, *Supervisor, *Handler) {
	t.Helper()
	state, err := service.OpenAdminState(service.AdminStateOptions{StateDir: stateDir, PathGuard: guard})
	if err != nil {
		t.Fatalf("OpenAdminState() error = %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	supervisor, err := NewSupervisor(SupervisorConfig{
		StateDir: stateDir,
		Address:  "127.0.0.1:0",
		Listen:   net.Listen,
		Open: func(path string) (RelayInstance, error) {
			return service.Open(path)
		},
	})
	if err != nil {
		t.Fatalf("NewSupervisor() error = %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = supervisor.Stop(ctx)
	})
	handler, err := NewHandler(HandlerConfig{
		State: state, Supervisor: supervisor, AdminGID: testAdminGID, HelperVersion: testHelperVersion,
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	return state, supervisor, handler
}

func mustAdminRequest(t *testing.T, requestID string, operation relayadmin.Operation, params any) []byte {
	t.Helper()
	raw, err := relayadmin.MarshalRequest(requestID, operation, params)
	if err != nil {
		t.Fatalf("MarshalRequest() error = %v", err)
	}
	return raw
}

func mustAdminResponse(t *testing.T, raw []byte) relayadmin.Response {
	t.Helper()
	response, err := relayadmin.ParseResponse(raw)
	if err != nil {
		t.Fatalf("ParseResponse(%s) error = %v", raw, err)
	}
	return response
}

func exchangeAdminServer(t *testing.T, server *relayadmin.Server, peer relayadmin.Peer, request []byte) ([]byte, relayadmin.ServeOutcome) {
	t.Helper()
	listener := mustLoopbackListener(t)
	serverConn := make(chan net.Conn, 1)
	acceptErr := make(chan error, 1)
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			acceptErr <- err
			return
		}
		serverConn <- connection
	}()
	clientConnection, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("net.Dial() error = %v", err)
	}
	defer clientConnection.Close()
	var accepted net.Conn
	select {
	case accepted = <-serverConn:
	case err := <-acceptErr:
		t.Fatalf("Accept() error = %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out accepting admin connection")
	}
	outcomeChannel := make(chan relayadmin.ServeOutcome, 1)
	go func() { outcomeChannel <- server.ServeConn(context.Background(), accepted, peer) }()
	if err := relayadmin.WriteFrame(clientConnection, request); err != nil {
		t.Fatalf("WriteFrame() error = %v", err)
	}
	halfCloser, ok := clientConnection.(interface{ CloseWrite() error })
	if !ok {
		t.Fatal("TCP client does not support CloseWrite")
	}
	if err := halfCloser.CloseWrite(); err != nil {
		t.Fatalf("CloseWrite() error = %v", err)
	}
	response, err := relayadmin.ReadFrameExact(clientConnection)
	if err != nil {
		t.Fatalf("ReadFrameExact() error = %v", err)
	}
	select {
	case outcome := <-outcomeChannel:
		return response, outcome
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for admin server")
		return nil, relayadmin.ServeOutcome{}
	}
}

func assertExactAdminJSONKeys(t *testing.T, raw []byte, envelopeKeys, resultKeys []string) {
	t.Helper()
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	assertStringSet(t, mapKeys(envelope), envelopeKeys)
	var result map[string]json.RawMessage
	if err := json.Unmarshal(envelope["result"], &result); err != nil {
		t.Fatal(err)
	}
	assertStringSet(t, mapKeys(result), resultKeys)
}

func assertExactAdminErrorKeys(t *testing.T, raw []byte) {
	t.Helper()
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	assertStringSet(t, mapKeys(envelope), []string{"version", "requestId", "operation", "ok", "errorCode"})
}

func mapKeys(values map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}

func assertStringSet(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("keys = %v, want %v", got, want)
	}
	for _, expected := range want {
		found := false
		for _, actual := range got {
			found = found || actual == expected
		}
		if !found {
			t.Fatalf("keys = %v, missing %q", got, expected)
		}
	}
}

func newAdminCSR(t *testing.T, commonName string) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: commonName}}, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}))
}

type replayReserveGate struct {
	entered      chan struct{}
	release      chan struct{}
	reserved     chan struct{}
	enterOnce    sync.Once
	reservedOnce sync.Once
}

func newReplayReserveGate() *replayReserveGate {
	return &replayReserveGate{
		entered: make(chan struct{}), release: make(chan struct{}), reserved: make(chan struct{}),
	}
}

type gatedReplayStore struct {
	relayadmin.ReplayStore
	gates map[string]*replayReserveGate
}

func (store *gatedReplayStore) Reserve(ctx context.Context, key relayadmin.ReplayKey) (relayadmin.ReplayReservation, error) {
	gate := store.gates[key.RequestID]
	if gate != nil {
		gate.enterOnce.Do(func() { close(gate.entered) })
		select {
		case <-gate.release:
		case <-ctx.Done():
			return relayadmin.ReplayReservation{}, ctx.Err()
		}
	}
	reservation, err := store.ReplayStore.Reserve(ctx, key)
	if gate != nil && err == nil && reservation.Decision == relayadmin.ReplayExecute {
		gate.reservedOnce.Do(func() { close(gate.reserved) })
	}
	return reservation, err
}

type observedSetupHandler struct {
	*Handler
	observedUID uint32
	entered     chan struct{}
	once        sync.Once
}

func (handler *observedSetupHandler) Setup(
	ctx context.Context,
	peer relayadmin.Peer,
	mutation relayadmin.Mutation,
	request relayadmin.SetupRequest,
) (relayadmin.OwnerBootstrapResult, error) {
	if peer.UID() == handler.observedUID {
		handler.once.Do(func() { close(handler.entered) })
	}
	return handler.Handler.Setup(ctx, peer, mutation, request)
}

func scriptedAdminResponse(t *testing.T, connection *scriptedAdminConn) relayadmin.Response {
	t.Helper()
	raw, err := relayadmin.ReadFrameExact(bytes.NewReader(connection.outputBytes()))
	if err != nil {
		t.Fatalf("ReadFrameExact(scripted response) error = %v", err)
	}
	return mustAdminResponse(t, raw)
}

func countAdminOwnerIdentities(t *testing.T, stateDir string) int {
	t.Helper()
	database, err := sql.Open("sqlite", filepath.Join(stateDir, "state.db"))
	if err != nil {
		t.Fatalf("sql.Open(state.db) error = %v", err)
	}
	defer database.Close()
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM identities WHERE role = 'owner'`).Scan(&count); err != nil {
		t.Fatalf("count Owner identities error = %v", err)
	}
	return count
}

func adminReplayResponse(t *testing.T, stateDir, requestID string) []byte {
	t.Helper()
	database, err := sql.Open("sqlite", filepath.Join(stateDir, "state.db"))
	if err != nil {
		t.Fatalf("sql.Open(state.db) error = %v", err)
	}
	defer database.Close()
	var response []byte
	if err := database.QueryRow(`SELECT response FROM admin_mutation_replay WHERE request_id = ? AND state = 'completed'`, requestID).Scan(&response); err != nil {
		t.Fatalf("read durable replay %s error = %v", requestID, err)
	}
	return append([]byte(nil), response...)
}

func waitSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func waitAdminServeOutcome(t *testing.T, outcome <-chan relayadmin.ServeOutcome, description string) relayadmin.ServeOutcome {
	t.Helper()
	select {
	case result := <-outcome:
		return result
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s ServeConn", description)
		return relayadmin.ServeOutcome{}
	}
}

type failingAdminPathGuard struct {
	validateErr error
	repairErr   error
}

func (guard failingAdminPathGuard) Validate(context.Context) error { return guard.validateErr }
func (guard failingAdminPathGuard) Repair(context.Context) error   { return guard.repairErr }

type serializedMutationHandler struct {
	*Handler
	active  atomic.Int32
	maximum atomic.Int32
	overlap atomic.Bool
	delay   time.Duration
}

func (handler *serializedMutationHandler) Setup(
	ctx context.Context,
	peer relayadmin.Peer,
	mutation relayadmin.Mutation,
	request relayadmin.SetupRequest,
) (relayadmin.OwnerBootstrapResult, error) {
	done := handler.enterMutation()
	defer done()
	return handler.Handler.Setup(ctx, peer, mutation, request)
}

func (handler *serializedMutationHandler) Rotate(
	ctx context.Context,
	peer relayadmin.Peer,
	mutation relayadmin.Mutation,
	request relayadmin.RotateRequest,
) (relayadmin.EndpointRotationResult, error) {
	done := handler.enterMutation()
	defer done()
	return handler.Handler.Rotate(ctx, peer, mutation, request)
}

func (handler *serializedMutationHandler) Repair(
	ctx context.Context,
	peer relayadmin.Peer,
	mutation relayadmin.Mutation,
) (relayadmin.RepairResult, error) {
	done := handler.enterMutation()
	defer done()
	return handler.Handler.Repair(ctx, peer, mutation)
}

func (handler *serializedMutationHandler) enterMutation() func() {
	active := handler.active.Add(1)
	if active > 1 {
		handler.overlap.Store(true)
	}
	for {
		maximum := handler.maximum.Load()
		if active <= maximum || handler.maximum.CompareAndSwap(maximum, active) {
			break
		}
	}
	time.Sleep(handler.delay)
	return func() { handler.active.Add(-1) }
}
