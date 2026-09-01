package service

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"mobile-egress/internal/relayadmin"
	"mobile-egress/relay/internal/enrollment"
)

func TestAdminBootstrapCommitsOwnerURLUIDAndReplayAtomically(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "Relay")
	state, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
	if err != nil {
		t.Fatal(err)
	}
	key := adminReplayKey("88888888888888888888888888888888", relayadmin.OperationSetup, "bootstrap")
	_, csr := newDeviceCSR(t)
	response, result := executeAdminBootstrap(t, state, key, AdminBootstrapOwnerOptions{
		PublicName: "relay.example.ts.net", PublicURL: "https://relay.example.ts.net:8443",
		CSRPEM: csr, AdministrativeOwnerUID: 501,
	})

	snapshot, err := state.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Class != AdminStateReady || !snapshot.OwnerUIDBound || snapshot.AdministrativeOwnerUID != 501 {
		t.Fatalf("Snapshot() = %#v, want ready UID 501", snapshot)
	}
	var ownerCount int
	if err := state.database.db.QueryRow(`SELECT COUNT(*) FROM identities WHERE role = 'owner'`).Scan(&ownerCount); err != nil {
		t.Fatal(err)
	}
	if ownerCount != 1 {
		t.Fatalf("Owner count = %d, want 1", ownerCount)
	}
	var relayURL, uid, replayState string
	var persistedResponse []byte
	if err := state.database.db.QueryRow(`SELECT value FROM settings WHERE key='relay_url'`).Scan(&relayURL); err != nil {
		t.Fatal(err)
	}
	if err := state.database.db.QueryRow(`SELECT value FROM settings WHERE key='administrative_owner_uid'`).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	if err := state.database.db.QueryRow(`SELECT state, response FROM admin_mutation_replay WHERE request_id=?`, key.RequestID).Scan(&replayState, &persistedResponse); err != nil {
		t.Fatal(err)
	}
	if relayURL != "https://relay.example.ts.net:8443" || uid != "501" || replayState != "completed" || !bytes.Equal(persistedResponse, response) {
		t.Fatalf("atomic state = url %q uid %q replay %q response-equal %v", relayURL, uid, replayState, bytes.Equal(persistedResponse, response))
	}
	if result.Role != enrollment.RoleOwner || result.Serial == "" {
		t.Fatalf("BootstrapOwner() result = %#v", result)
	}

	caKey, err := os.ReadFile(filepath.Join(stateDir, caKeyFilename))
	if err != nil {
		t.Fatal(err)
	}
	relayKey, err := os.ReadFile(filepath.Join(stateDir, relayKeyFilename))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(response, caKey) || bytes.Contains(response, relayKey) || bytes.Contains(persistedResponse, caKey) || bytes.Contains(persistedResponse, relayKey) {
		t.Fatal("replay response persisted a relay private key")
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{caCertFilename, caKeyFilename, relayCertFilename, relayKeyFilename, databaseFilename} {
		contents, err := os.ReadFile(filepath.Join(stateDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(contents, []byte(csr)) {
			t.Fatalf("%s persisted the Owner CSR", name)
		}
	}

	reopened, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	cached, err := reopened.ReplayStore().Reserve(context.Background(), key)
	if err != nil || cached.Decision != relayadmin.ReplayCached || !bytes.Equal(cached.Response, response) {
		t.Fatalf("reopened Reserve() = (%#v, %v), want exact Owner response", cached, err)
	}
	called := false
	if cached.Mutation != nil {
		_, _ = cached.Mutation.Execute(context.Background(), func(context.Context, relayadmin.MutationTransaction) ([]byte, error) {
			called = true
			return nil, nil
		})
	}
	if called {
		t.Fatal("cached setup reexecuted Owner generation")
	}
}

func TestAdminBootstrapRejectsRootAndDifferentRequestAfterInitialization(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "Relay")
	state, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	_, csr := newDeviceCSR(t)
	rootKey := adminReplayKey("99999999999999999999999999999999", relayadmin.OperationSetup, "root")
	rootReservation, err := state.ReplayStore().Reserve(context.Background(), rootKey)
	if err != nil {
		t.Fatal(err)
	}
	_, err = rootReservation.Mutation.Execute(context.Background(), func(ctx context.Context, transaction relayadmin.MutationTransaction) ([]byte, error) {
		_, err := state.BootstrapOwner(ctx, transaction, AdminBootstrapOwnerOptions{
			PublicName: "relay.example.ts.net", PublicURL: "https://relay.example.ts.net:8443", CSRPEM: csr,
		})
		return nil, err
	})
	if err == nil {
		t.Fatal("BootstrapOwner() accepted UID 0")
	}
	if _, err := os.Stat(stateDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("root setup created authoritative state: %v", err)
	}

	validKey := adminReplayKey("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", relayadmin.OperationSetup, "valid")
	executeAdminBootstrap(t, state, validKey, AdminBootstrapOwnerOptions{
		PublicName: "relay.example.ts.net", PublicURL: "https://relay.example.ts.net:8443", CSRPEM: csr, AdministrativeOwnerUID: 501,
	})
	otherKey := adminReplayKey("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", relayadmin.OperationSetup, "other")
	other, err := state.ReplayStore().Reserve(context.Background(), otherKey)
	if err != nil || other.Decision != relayadmin.ReplayExecute {
		t.Fatalf("Reserve(different setup) = (%#v, %v)", other, err)
	}
	_, err = other.Mutation.Execute(context.Background(), func(ctx context.Context, transaction relayadmin.MutationTransaction) ([]byte, error) {
		_, bootstrapErr := state.BootstrapOwner(ctx, transaction, AdminBootstrapOwnerOptions{
			PublicName: "relay.example.ts.net", PublicURL: "https://relay.example.ts.net:8443", CSRPEM: csr, AdministrativeOwnerUID: 502,
		})
		if !errors.Is(bootstrapErr, ErrAdminAlreadyInitialized) {
			t.Fatalf("BootstrapOwner() error = %v, want ErrAdminAlreadyInitialized", bootstrapErr)
		}
		return relayadmin.MarshalErrorResponse(otherKey.RequestID, otherKey.Operation, relayadmin.ErrorAlreadyInitialized)
	})
	if err != nil {
		t.Fatalf("cache already-initialized response: %v", err)
	}
}

func TestAdminBootstrapDeterminateRejectionSameIDIsSafelyRetryable(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "Relay")
	var finished atomic.Int32
	state, err := OpenAdminState(AdminStateOptions{
		StateDir: stateDir, MutationCapacity: 1,
		MutationFinished: func(relayadmin.ReplayKey) { finished.Add(1) },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	key := adminReplayKey("16161616161616161616161616161616", relayadmin.OperationSetup, "invalid")
	_, csr := newDeviceCSR(t)
	for attempt := 0; attempt < 2; attempt++ {
		reserved, err := state.ReplayStore().Reserve(context.Background(), key)
		if err != nil || reserved.Decision != relayadmin.ReplayExecute {
			t.Fatalf("attempt %d Reserve() = (%#v, %v), want safe revalidation", attempt, reserved, err)
		}
		response, err := reserved.Mutation.Execute(context.Background(), func(ctx context.Context, transaction relayadmin.MutationTransaction) ([]byte, error) {
			_, bootstrapErr := state.BootstrapOwner(ctx, transaction, AdminBootstrapOwnerOptions{
				PublicName: "relay.example.ts.net", PublicURL: "https://different.example.ts.net:8443",
				CSRPEM: csr, AdministrativeOwnerUID: 501,
			})
			if bootstrapErr == nil {
				t.Fatal("BootstrapOwner() accepted mismatched public origin")
			}
			return relayadmin.MarshalErrorResponse(key.RequestID, key.Operation, relayadmin.ErrorInvalidRequest)
		})
		if err != nil || len(response) == 0 {
			t.Fatalf("attempt %d determinate rejection = (%q, %v)", attempt, response, err)
		}
		if _, err := os.Stat(stateDir); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("attempt %d created authoritative state: %v", attempt, err)
		}
	}
	if finished.Load() != 2 {
		t.Fatalf("MutationFinished calls = %d, want one per rejected attempt", finished.Load())
	}
}

func TestAdminBootstrapConcurrentFirstOwnerBindsOnlyOneUID(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "Relay")
	state, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	_, csrA := newDeviceCSR(t)
	_, csrB := newDeviceCSR(t)
	type attempt struct {
		key relayadmin.ReplayKey
		uid uint32
		csr string
	}
	attempts := []attempt{
		{key: adminReplayKey("cccccccccccccccccccccccccccccccc", relayadmin.OperationSetup, "a"), uid: 501, csr: csrA},
		{key: adminReplayKey("dddddddddddddddddddddddddddddddd", relayadmin.OperationSetup, "b"), uid: 502, csr: csrB},
	}
	reservations := make([]relayadmin.MutationReservation, len(attempts))
	for index, attempt := range attempts {
		reserved, err := state.ReplayStore().Reserve(context.Background(), attempt.key)
		if err != nil || reserved.Decision != relayadmin.ReplayExecute {
			t.Fatalf("Reserve(%d) = (%#v, %v)", index, reserved, err)
		}
		reservations[index] = reserved.Mutation
	}
	start := make(chan struct{})
	var callbacks atomic.Int32
	errorsByAttempt := make([]error, len(attempts))
	var wait sync.WaitGroup
	for index := range attempts {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			_, errorsByAttempt[index] = reservations[index].Execute(context.Background(), func(ctx context.Context, transaction relayadmin.MutationTransaction) ([]byte, error) {
				callbacks.Add(1)
				result, err := state.BootstrapOwner(ctx, transaction, AdminBootstrapOwnerOptions{
					PublicName: "relay.example.ts.net", PublicURL: "https://relay.example.ts.net:8443",
					CSRPEM: attempts[index].csr, AdministrativeOwnerUID: attempts[index].uid,
				})
				if err != nil {
					return nil, err
				}
				return marshalAdminBootstrapResponse(attempts[index].key, result)
			})
		}(index)
	}
	close(start)
	wait.Wait()
	if callbacks.Load() != 1 {
		t.Fatalf("Owner callbacks = %d, want exactly 1", callbacks.Load())
	}
	successes := 0
	for _, err := range errorsByAttempt {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("successful setup attempts = %d, want 1; errors=%v", successes, errorsByAttempt)
	}
	snapshot, err := state.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Class != AdminStateReady || (snapshot.AdministrativeOwnerUID != 501 && snapshot.AdministrativeOwnerUID != 502) {
		t.Fatalf("winning Snapshot() = %#v", snapshot)
	}
}

func TestAdminBootstrapFaultsBeforeAuthorityAreRetryable(t *testing.T) {
	for _, fault := range []string{"before commit", "before rename"} {
		t.Run(fault, func(t *testing.T) {
			stateDir := filepath.Join(t.TempDir(), "Relay")
			state, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
			if err != nil {
				t.Fatal(err)
			}
			defer state.Close()
			injected := errors.New("injected setup fault")
			if fault == "before commit" {
				state.beforeSetupCommit = func() error { return injected }
			} else {
				state.beforeSetupRename = func() error { return injected }
			}
			key := adminReplayKey("eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", relayadmin.OperationSetup, fault)
			_, csr := newDeviceCSR(t)
			reservation, err := state.ReplayStore().Reserve(context.Background(), key)
			if err != nil {
				t.Fatal(err)
			}
			_, err = reservation.Mutation.Execute(context.Background(), func(ctx context.Context, transaction relayadmin.MutationTransaction) ([]byte, error) {
				result, err := state.BootstrapOwner(ctx, transaction, AdminBootstrapOwnerOptions{
					PublicName: "relay.example.ts.net", PublicURL: "https://relay.example.ts.net:8443", CSRPEM: csr, AdministrativeOwnerUID: 501,
				})
				if err != nil {
					return nil, err
				}
				return marshalAdminBootstrapResponse(key, result)
			})
			if !errors.Is(err, injected) {
				t.Fatalf("Execute() error = %v, want injected fault", err)
			}
			if _, err := os.Stat(stateDir); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("fault created authoritative state: %v", err)
			}
			stage := filepath.Join(filepath.Dir(stateDir), ".relay-setup-"+key.RequestID)
			if _, err := os.Stat(stage); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("fault left setup staging directory: %v", err)
			}
			state.beforeSetupCommit = nil
			state.beforeSetupRename = nil
			retry, err := state.ReplayStore().Reserve(context.Background(), key)
			if err != nil || retry.Decision != relayadmin.ReplayExecute {
				t.Fatalf("retry Reserve() = (%#v, %v), want execute", retry, err)
			}
		})
	}
}

func TestAdminBootstrapCleanupFailureStaysLiveFailClosed(t *testing.T) {
	for _, phase := range []string{"before database", "before commit"} {
		t.Run(phase, func(t *testing.T) {
			stateDir := filepath.Join(t.TempDir(), "Relay")
			state, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
			if err != nil {
				t.Fatal(err)
			}
			defer state.Close()
			injected := errors.New("injected setup failure")
			cleanupErr := errors.New("injected cleanup failure")
			if phase == "before database" {
				state.beforeSetupDatabase = func() error { return injected }
			} else {
				state.beforeSetupCommit = func() error { return injected }
			}
			state.removeSetupStage = func(string) error { return cleanupErr }
			key := adminReplayKey("52525252525252525252525252525252", relayadmin.OperationSetup, phase)
			_, csr := newDeviceCSR(t)
			reservation, err := state.ReplayStore().Reserve(context.Background(), key)
			if err != nil || reservation.Decision != relayadmin.ReplayExecute {
				t.Fatalf("Reserve() = (%#v, %v)", reservation, err)
			}
			callbacks := atomic.Int32{}
			_, err = reservation.Mutation.Execute(context.Background(), func(ctx context.Context, transaction relayadmin.MutationTransaction) ([]byte, error) {
				callbacks.Add(1)
				result, setupErr := state.BootstrapOwner(ctx, transaction, AdminBootstrapOwnerOptions{
					PublicName: "relay.example.ts.net", PublicURL: "https://relay.example.ts.net:8443",
					CSRPEM: csr, AdministrativeOwnerUID: 501,
				})
				if setupErr != nil {
					return nil, setupErr
				}
				return marshalAdminBootstrapResponse(key, result)
			})
			if !errors.Is(err, relayadmin.ErrMutationIndeterminate) {
				t.Fatalf("Execute() error = %v, want ErrMutationIndeterminate", err)
			}
			snapshot, err := state.Snapshot(context.Background())
			if err != nil || snapshot.Class != AdminStateIncompatible {
				t.Fatalf("Snapshot() = (%#v, %v), want degraded", snapshot, err)
			}
			same, err := state.ReplayStore().Reserve(context.Background(), key)
			if err != nil || same.Decision != relayadmin.ReplayBusy {
				t.Fatalf("same-ID retry = (%#v, %v), want busy", same, err)
			}
			differentKey := adminReplayKey("53535353535353535353535353535353", relayadmin.OperationSetup, phase+"-different")
			different, err := state.ReplayStore().Reserve(context.Background(), differentKey)
			if err != nil || different.Decision != relayadmin.ReplayBusy {
				t.Fatalf("different-ID retry = (%#v, %v), want busy", different, err)
			}
			collisionKey := adminReplayKey(key.RequestID, relayadmin.OperationRepair, phase+"-collision")
			collision, err := state.ReplayStore().Reserve(context.Background(), collisionKey)
			if err != nil || collision.Decision != relayadmin.ReplayDuplicate {
				t.Fatalf("cross-operation same-ID retry = (%#v, %v), want duplicate", collision, err)
			}
			rotateKey := adminReplayKey("54545454545454545454545454545454", relayadmin.OperationRotate, phase+"-rotate")
			rotate, err := state.ReplayStore().Reserve(context.Background(), rotateKey)
			if err != nil || rotate.Decision != relayadmin.ReplayBusy {
				t.Fatalf("rotate while cleanup is uncertain = (%#v, %v), want busy", rotate, err)
			}
			repairKey := adminReplayKey("55555555555555555555555555555555", relayadmin.OperationRepair, phase+"-repair")
			repair, err := state.ReplayStore().Reserve(context.Background(), repairKey)
			if err != nil || repair.Decision != relayadmin.ReplayExecute {
				t.Fatalf("repair while cleanup is uncertain = (%#v, %v), want execute", repair, err)
			}
			repairCallbacks := atomic.Int32{}
			_, err = repair.Mutation.Execute(context.Background(), func(ctx context.Context, transaction relayadmin.MutationTransaction) ([]byte, error) {
				repairCallbacks.Add(1)
				return nil, state.Repair(ctx, transaction)
			})
			if !errors.Is(err, errAdminMutationUnavailable) {
				t.Fatalf("Repair() boundary error = %v, want errAdminMutationUnavailable", err)
			}
			if repairCallbacks.Load() != 1 {
				t.Fatalf("repair callback count = %d, want 1", repairCallbacks.Load())
			}
			sameRepair, err := state.ReplayStore().Reserve(context.Background(), repairKey)
			if err != nil || sameRepair.Decision != relayadmin.ReplayBusy {
				t.Fatalf("same repair after callback = (%#v, %v), want busy", sameRepair, err)
			}
			repairCollisionKey := adminReplayKey(repairKey.RequestID, relayadmin.OperationRepair, phase+"-repair-collision")
			repairCollision, err := state.ReplayStore().Reserve(context.Background(), repairCollisionKey)
			if err != nil || repairCollision.Decision != relayadmin.ReplayDuplicate {
				t.Fatalf("same-ID repair collision after callback = (%#v, %v), want duplicate", repairCollision, err)
			}
			secondRepairKey := adminReplayKey("56565656565656565656565656565656", relayadmin.OperationRepair, phase+"-second-repair")
			secondRepair, err := state.ReplayStore().Reserve(context.Background(), secondRepairKey)
			if err != nil || secondRepair.Decision != relayadmin.ReplayBusy {
				t.Fatalf("second repair after indeterminate recovery = (%#v, %v), want busy", secondRepair, err)
			}
			blockedAfterRepair, err := state.ReplayStore().Reserve(context.Background(), differentKey)
			if err != nil || blockedAfterRepair.Decision != relayadmin.ReplayBusy {
				t.Fatalf("setup after repair boundary = (%#v, %v), want busy", blockedAfterRepair, err)
			}
			if callbacks.Load() != 1 {
				t.Fatalf("setup callback count = %d, want 1", callbacks.Load())
			}
			stageDir := filepath.Join(filepath.Dir(stateDir), ".relay-setup-"+key.RequestID)
			if _, err := os.Stat(stageDir); err != nil {
				t.Fatalf("uncertain setup evidence missing: %v", err)
			}
			if len(state.active) != 0 || len(state.pending) != 0 || len(state.uncertain) != 2 || len(state.mutationKeys) != 2 {
				t.Fatalf("unexpected recovery replay accounting: active=%d pending=%d uncertain=%d keys=%d",
					len(state.active), len(state.pending), len(state.uncertain), len(state.mutationKeys))
			}
		})
	}
}

func TestAdminBootstrapFaultAfterRenameKeepsCachedAuthority(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "Relay")
	state, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
	if err != nil {
		t.Fatal(err)
	}
	injected := errors.New("injected post-rename fault")
	state.afterSetupRename = func() error { return injected }
	key := adminReplayKey("ffffffffffffffffffffffffffffffff", relayadmin.OperationSetup, "after")
	_, csr := newDeviceCSR(t)
	reservation, err := state.ReplayStore().Reserve(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	_, err = reservation.Mutation.Execute(context.Background(), func(ctx context.Context, transaction relayadmin.MutationTransaction) ([]byte, error) {
		result, err := state.BootstrapOwner(ctx, transaction, AdminBootstrapOwnerOptions{
			PublicName: "relay.example.ts.net", PublicURL: "https://relay.example.ts.net:8443", CSRPEM: csr, AdministrativeOwnerUID: 501,
		})
		if err != nil {
			return nil, err
		}
		return marshalAdminBootstrapResponse(key, result)
	})
	if !errors.Is(err, injected) {
		t.Fatalf("Execute() error = %v, want injected post-rename fault", err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, databaseFilename)); err != nil {
		t.Fatalf("authoritative state missing after rename: %v", err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	cached, err := reopened.ReplayStore().Reserve(context.Background(), key)
	if err != nil || cached.Decision != relayadmin.ReplayCached {
		t.Fatalf("post-rename retry = (%#v, %v), want cached", cached, err)
	}
}

func TestAdminBootstrapParentSyncFailureStaysBoundDegradedWithFallback(t *testing.T) {
	parent := t.TempDir()
	stateDir := filepath.Join(parent, "Relay")
	injected := errors.New("injected parent sync failure")
	var state *AdminState
	var finishedSnapshot AdminSnapshot
	var finishedSnapshotErr error
	syncCalls := 0
	opened, err := OpenAdminState(AdminStateOptions{
		StateDir: stateDir,
		syncSetupParent: func(path string) error {
			syncCalls++
			if path != parent {
				t.Fatalf("sync parent = %q, want %q", path, parent)
			}
			return injected
		},
		MutationFinished: func(relayadmin.ReplayKey) {
			finishedSnapshot, finishedSnapshotErr = state.Snapshot(context.Background())
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	state = opened
	defer state.Close()

	key := adminReplayKey("4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a", relayadmin.OperationSetup, "parent-sync")
	_, csr := newDeviceCSR(t)
	reservation, err := state.ReplayStore().Reserve(context.Background(), key)
	if err != nil || reservation.Decision != relayadmin.ReplayExecute {
		t.Fatalf("Reserve() = (%#v, %v), want execute", reservation, err)
	}
	var durableResponse []byte
	_, err = reservation.Mutation.Execute(context.Background(), func(ctx context.Context, transaction relayadmin.MutationTransaction) ([]byte, error) {
		result, setupErr := state.BootstrapOwner(ctx, transaction, AdminBootstrapOwnerOptions{
			PublicName: "relay.example.ts.net", PublicURL: "https://relay.example.ts.net:8443",
			CSRPEM: csr, AdministrativeOwnerUID: 501,
		})
		if setupErr != nil {
			return nil, setupErr
		}
		durableResponse, setupErr = marshalAdminBootstrapResponse(key, result)
		return durableResponse, setupErr
	})
	if !errors.Is(err, relayadmin.ErrMutationIndeterminate) {
		t.Fatalf("Execute() error = %v, want ErrMutationIndeterminate", err)
	}
	if syncCalls != 1 {
		t.Fatalf("normal setup parent sync calls = %d, want 1", syncCalls)
	}
	if finishedSnapshotErr != nil || finishedSnapshot.Class != AdminStateIncompatible ||
		!finishedSnapshot.OwnerUIDBound || finishedSnapshot.AdministrativeOwnerUID != 501 {
		t.Fatalf("MutationFinished Snapshot() = (%#v, %v), want bound degraded UID 501", finishedSnapshot, finishedSnapshotErr)
	}
	snapshot, err := state.Snapshot(context.Background())
	if err != nil || snapshot.Class != AdminStateIncompatible || !snapshot.OwnerUIDBound || snapshot.AdministrativeOwnerUID != 501 {
		t.Fatalf("Snapshot() = (%#v, %v), want bound degraded UID 501", snapshot, err)
	}
	cached, err := state.ReplayStore().Reserve(context.Background(), key)
	if err != nil || cached.Decision != relayadmin.ReplayCached || !bytes.Equal(cached.Response, durableResponse) {
		t.Fatalf("same-process retry = (%#v, %v), want exact cached fallback", cached, err)
	}
	state.mu.Lock()
	fallbackCount := len(state.fallback)
	replayReady := state.replayReady
	state.mu.Unlock()
	if fallbackCount != 1 || replayReady {
		t.Fatalf("authoritative fallback state = (fallback=%d, replayReady=%t), want (1, false)", fallbackCount, replayReady)
	}
	repairKey := adminReplayKey("4b4b4b4b4b4b4b4b4b4b4b4b4b4b4b4b", relayadmin.OperationRepair, "parent-sync-repair")
	repair, err := state.ReplayStore().Reserve(context.Background(), repairKey)
	if err != nil || repair.Decision != relayadmin.ReplayExecute || repair.Mutation == nil {
		t.Fatalf("repair Reserve() = (%#v, %v), want executable recovery", repair, err)
	}
	if err := repair.Mutation.Abandon(context.Background()); err != nil {
		t.Fatalf("repair Abandon() error = %v", err)
	}
}

func TestAdminBootstrapReopenFailureAfterRenameNeverReexecutes(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "Relay")
	state, err := OpenAdminState(AdminStateOptions{StateDir: stateDir, MutationCapacity: 2})
	if err != nil {
		t.Fatal(err)
	}
	injected := errors.New("injected authoritative reopen failure")
	state.beforeSetupReopen = func() error { return injected }
	key := adminReplayKey("45454545454545454545454545454545", relayadmin.OperationSetup, "reopen")
	_, csr := newDeviceCSR(t)
	reservation, err := state.ReplayStore().Reserve(context.Background(), key)
	if err != nil || reservation.Decision != relayadmin.ReplayExecute {
		t.Fatalf("Reserve() = (%#v, %v)", reservation, err)
	}
	var durableResponse []byte
	_, err = reservation.Mutation.Execute(context.Background(), func(ctx context.Context, transaction relayadmin.MutationTransaction) ([]byte, error) {
		result, err := state.BootstrapOwner(ctx, transaction, AdminBootstrapOwnerOptions{
			PublicName: "relay.example.ts.net", PublicURL: "https://relay.example.ts.net:8443",
			CSRPEM: csr, AdministrativeOwnerUID: 501,
		})
		if err != nil {
			return nil, err
		}
		durableResponse, err = marshalAdminBootstrapResponse(key, result)
		return durableResponse, err
	})
	if !errors.Is(err, relayadmin.ErrMutationIndeterminate) {
		t.Fatalf("Execute() error = %v, want ErrMutationIndeterminate", err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, databaseFilename)); err != nil {
		t.Fatalf("authoritative state missing after rename: %v", err)
	}
	snapshot, err := state.Snapshot(context.Background())
	if err != nil || snapshot.Class != AdminStateIncompatible || !snapshot.OwnerUIDBound || snapshot.AdministrativeOwnerUID != 501 {
		t.Fatalf("Snapshot() after reopen failure = (%#v, %v), want incompatible with bound UID 501", snapshot, err)
	}
	cached, err := state.ReplayStore().Reserve(context.Background(), key)
	if err != nil || cached.Decision != relayadmin.ReplayCached || !bytes.Equal(cached.Response, durableResponse) {
		t.Fatalf("same-process retry = (%#v, %v), want cached authoritative response", cached, err)
	}
	called := false
	if cached.Mutation != nil {
		_, _ = cached.Mutation.Execute(context.Background(), func(context.Context, relayadmin.MutationTransaction) ([]byte, error) {
			called = true
			return nil, nil
		})
	}
	if called {
		t.Fatal("authoritative retry reexecuted setup")
	}
	different := key
	different.Digest[0] ^= 0xff
	duplicate, err := state.ReplayStore().Reserve(context.Background(), different)
	if err != nil || duplicate.Decision != relayadmin.ReplayDuplicate {
		t.Fatalf("different digest retry = (%#v, %v), want duplicate", duplicate, err)
	}
	for index, operation := range []relayadmin.Operation{relayadmin.OperationSetup, relayadmin.OperationRotate} {
		requestID := []string{"46464646464646464646464646464646", "47474747474747474747474747474747"}[index]
		blockedKey := adminReplayKey(requestID, operation, "blocked-after-authority")
		blocked, err := state.ReplayStore().Reserve(context.Background(), blockedKey)
		if err != nil || blocked.Decision != relayadmin.ReplayExecute {
			t.Fatalf("%s reservation after authoritative reopen failure = (%#v, %v), want guarded execute", operation, blocked, err)
		}
		callbackCalled := false
		_, err = blocked.Mutation.Execute(context.Background(), func(context.Context, relayadmin.MutationTransaction) ([]byte, error) {
			callbackCalled = true
			return nil, nil
		})
		if !errors.Is(err, relayadmin.ErrReplayState) || callbackCalled {
			t.Fatalf("%s guarded execution = (callback=%t, error=%v), want callback false and ErrReplayState", operation, callbackCalled, err)
		}
	}
	repairKey := adminReplayKey("48484848484848484848484848484848", relayadmin.OperationRepair, "repair-authoritative-fallback")
	repair, err := state.ReplayStore().Reserve(context.Background(), repairKey)
	if err != nil || repair.Decision != relayadmin.ReplayExecute {
		t.Fatalf("repair reservation after authoritative reopen failure = (%#v, %v), want execute", repair, err)
	}
	cachedWhileRepairActive, err := state.ReplayStore().Reserve(context.Background(), key)
	if err != nil || cachedWhileRepairActive.Decision != relayadmin.ReplayCached || !bytes.Equal(cachedWhileRepairActive.Response, durableResponse) {
		t.Fatalf("fallback while repair active = (%#v, %v), want cached", cachedWhileRepairActive, err)
	}
	activeFallbackCollisionKey := adminReplayKey(key.RequestID, relayadmin.OperationRepair, "active-fallback-collision")
	activeFallbackCollision, err := state.ReplayStore().Reserve(context.Background(), activeFallbackCollisionKey)
	if err != nil || activeFallbackCollision.Decision != relayadmin.ReplayDuplicate {
		t.Fatalf("fallback collision while repair active = (%#v, %v), want duplicate", activeFallbackCollision, err)
	}
	repairCalled := false
	_, err = repair.Mutation.Execute(context.Background(), func(ctx context.Context, transaction relayadmin.MutationTransaction) ([]byte, error) {
		repairCalled = true
		return nil, state.Repair(ctx, transaction)
	})
	if !errors.Is(err, errAdminMutationUnavailable) || !repairCalled {
		t.Fatalf("repair boundary after authoritative reopen failure = (callback=%t, error=%v), want callback and unavailable boundary", repairCalled, err)
	}
	sameRepair, err := state.ReplayStore().Reserve(context.Background(), repairKey)
	if err != nil || sameRepair.Decision != relayadmin.ReplayBusy {
		t.Fatalf("same repair retry = (%#v, %v), want busy", sameRepair, err)
	}
	repairCollisionKey := adminReplayKey(repairKey.RequestID, relayadmin.OperationRepair, "repair-authoritative-collision")
	repairCollision, err := state.ReplayStore().Reserve(context.Background(), repairCollisionKey)
	if err != nil || repairCollision.Decision != relayadmin.ReplayDuplicate {
		t.Fatalf("repair collision = (%#v, %v), want duplicate", repairCollision, err)
	}
	capacityKey := adminReplayKey("49494949494949494949494949494949", relayadmin.OperationRepair, "repair-authoritative-capacity")
	capacity, err := state.ReplayStore().Reserve(context.Background(), capacityKey)
	if err != nil || capacity.Decision != relayadmin.ReplayBusy {
		t.Fatalf("repair after fallback and indeterminate capacity = (%#v, %v), want busy", capacity, err)
	}
	cached, err = state.ReplayStore().Reserve(context.Background(), key)
	if err != nil || cached.Decision != relayadmin.ReplayCached || !bytes.Equal(cached.Response, durableResponse) {
		t.Fatalf("fallback retry after repair boundary = (%#v, %v), want cached", cached, err)
	}
	if len(state.active) != 0 || len(state.pending) != 0 || len(state.fallback) != 1 || len(state.uncertain) != 1 || len(state.mutationKeys) != 2 {
		t.Fatalf("unexpected fallback repair accounting: active=%d pending=%d fallback=%d uncertain=%d keys=%d",
			len(state.active), len(state.pending), len(state.fallback), len(state.uncertain), len(state.mutationKeys))
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	cached, err = reopened.ReplayStore().Reserve(context.Background(), key)
	if err != nil || cached.Decision != relayadmin.ReplayCached || !bytes.Equal(cached.Response, durableResponse) {
		t.Fatalf("process-restart retry = (%#v, %v), want cached", cached, err)
	}
}

func TestAdminBootstrapReopenPromotesCompletedExactStage(t *testing.T) {
	parent := t.TempDir()
	stateDir := filepath.Join(parent, "Relay")
	state, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
	if err != nil {
		t.Fatal(err)
	}
	key := adminReplayKey("19191919191919191919191919191919", relayadmin.OperationSetup, "crash-stage")
	_, csr := newDeviceCSR(t)
	response, _ := executeAdminBootstrap(t, state, key, AdminBootstrapOwnerOptions{
		PublicName: "relay.example.ts.net", PublicURL: "https://relay.example.ts.net:8443", CSRPEM: csr, AdministrativeOwnerUID: 501,
	})
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	stageDir := filepath.Join(parent, ".relay-setup-"+key.RequestID)
	if err := os.Rename(stateDir, stageDir); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
	if err != nil {
		t.Fatalf("OpenAdminState(completed stage) error = %v", err)
	}
	defer reopened.Close()
	if _, err := os.Stat(stateDir); err != nil {
		t.Fatalf("completed setup stage was not promoted: %v", err)
	}
	if _, err := os.Stat(stageDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("completed setup stage remains after promotion: %v", err)
	}
	cached, err := reopened.ReplayStore().Reserve(context.Background(), key)
	if err != nil || cached.Decision != relayadmin.ReplayCached || !bytes.Equal(cached.Response, response) {
		t.Fatalf("promoted stage replay = (%#v, %v), want exact cached response", cached, err)
	}
}

func TestAdminBootstrapPromotionParentSyncFailureKeepsBoundCachedAuthority(t *testing.T) {
	parent := t.TempDir()
	stateDir := filepath.Join(parent, "Relay")
	state, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
	if err != nil {
		t.Fatal(err)
	}
	key := adminReplayKey("57575757575757575757575757575757", relayadmin.OperationSetup, "promotion-sync")
	_, csr := newDeviceCSR(t)
	response, _ := executeAdminBootstrap(t, state, key, AdminBootstrapOwnerOptions{
		PublicName: "relay.example.ts.net", PublicURL: "https://relay.example.ts.net:8443",
		CSRPEM: csr, AdministrativeOwnerUID: 501,
	})
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	stageDir := filepath.Join(parent, ".relay-setup-"+key.RequestID)
	if err := os.Rename(stateDir, stageDir); err != nil {
		t.Fatal(err)
	}
	injected := errors.New("injected parent sync failure")
	syncCalls := 0
	reopened, err := OpenAdminState(AdminStateOptions{
		StateDir: stateDir,
		syncSetupParent: func(path string) error {
			syncCalls++
			if path != parent {
				t.Fatalf("sync parent = %q, want %q", path, parent)
			}
			return injected
		},
	})
	if err != nil {
		t.Fatalf("OpenAdminState(post-promotion sync failure) error = %v", err)
	}
	defer reopened.Close()
	if syncCalls != 1 {
		t.Fatalf("parent sync calls = %d, want 1", syncCalls)
	}
	snapshot, err := reopened.Snapshot(context.Background())
	if err != nil || snapshot.Class != AdminStateIncompatible || !snapshot.OwnerUIDBound || snapshot.AdministrativeOwnerUID != 501 {
		t.Fatalf("Snapshot() = (%#v, %v), want degraded bound UID 501", snapshot, err)
	}
	cached, err := reopened.ReplayStore().Reserve(context.Background(), key)
	if err != nil || cached.Decision != relayadmin.ReplayCached || !bytes.Equal(cached.Response, response) {
		t.Fatalf("post-promotion retry = (%#v, %v), want cached", cached, err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, databaseFilename)); err != nil {
		t.Fatalf("promoted authoritative database missing: %v", err)
	}
	if _, err := os.Stat(stageDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("promoted stage still exists: %v", err)
	}
}

func TestAdminBootstrapStagePromotionRequiresCoherentAtomicTuple(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, string, relayadmin.ReplayKey)
	}{
		{name: "missing Owner", mutate: func(t *testing.T, stateDir string, _ relayadmin.ReplayKey) {
			execAdminStateSQL(t, stateDir, `DELETE FROM identities WHERE role = 'owner'`)
		}},
		{name: "missing administrative UID", mutate: func(t *testing.T, stateDir string, _ relayadmin.ReplayKey) {
			execAdminStateSQL(t, stateDir, `DELETE FROM settings WHERE key = 'administrative_owner_uid'`)
		}},
		{name: "zero administrative UID", mutate: func(t *testing.T, stateDir string, _ relayadmin.ReplayKey) {
			execAdminStateSQL(t, stateDir, `UPDATE settings SET value = '0' WHERE key = 'administrative_owner_uid'`)
		}},
		{name: "missing relay URL", mutate: func(t *testing.T, stateDir string, _ relayadmin.ReplayKey) {
			execAdminStateSQL(t, stateDir, `DELETE FROM settings WHERE key = 'relay_url'`)
		}},
		{name: "mismatched response serial", mutate: func(t *testing.T, stateDir string, key relayadmin.ReplayKey) {
			rewriteAdminSetupResult(t, stateDir, key, func(result *relayadmin.OwnerBootstrapResult) {
				result.Serial = "DEADBEEF"
			})
		}},
		{name: "mismatched response certificate", mutate: func(t *testing.T, stateDir string, key relayadmin.ReplayKey) {
			rewriteAdminSetupResult(t, stateDir, key, func(result *relayadmin.OwnerBootstrapResult) {
				result.CertificatePEM = result.CACertificatePEM
			})
		}},
		{name: "mismatched response CA", mutate: func(t *testing.T, stateDir string, key relayadmin.ReplayKey) {
			rewriteAdminSetupResult(t, stateDir, key, func(result *relayadmin.OwnerBootstrapResult) {
				result.CACertificatePEM = "not-the-relay-ca"
			})
		}},
		{name: "invalid relay TLS state", mutate: func(t *testing.T, stateDir string, _ relayadmin.ReplayKey) {
			if err := os.WriteFile(filepath.Join(stateDir, relayCertFilename), []byte("not-a-certificate"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parent := t.TempDir()
			stateDir := filepath.Join(parent, "Relay")
			state, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
			if err != nil {
				t.Fatal(err)
			}
			requestID := []string{
				"31313131313131313131313131313131", "32323232323232323232323232323232",
				"33333333333333333333333333333333", "34343434343434343434343434343434",
				"35353535353535353535353535353535", "36363636363636363636363636363636",
				"37373737373737373737373737373737", "38383838383838383838383838383838",
			}[index]
			key := adminReplayKey(requestID, relayadmin.OperationSetup, test.name)
			_, csr := newDeviceCSR(t)
			executeAdminBootstrap(t, state, key, AdminBootstrapOwnerOptions{
				PublicName: "relay.example.ts.net", PublicURL: "https://relay.example.ts.net:8443",
				CSRPEM: csr, AdministrativeOwnerUID: 501,
			})
			if err := state.Close(); err != nil {
				t.Fatal(err)
			}
			test.mutate(t, stateDir, key)
			stageDir := filepath.Join(parent, ".relay-setup-"+key.RequestID)
			if err := os.Rename(stateDir, stageDir); err != nil {
				t.Fatal(err)
			}

			reopened, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
			if err != nil {
				t.Fatalf("OpenAdminState(incoherent stage) error = %v", err)
			}
			defer reopened.Close()
			snapshot, err := reopened.Snapshot(context.Background())
			if err != nil || snapshot.Class != AdminStateIncompatible {
				t.Fatalf("Snapshot(incoherent stage) = (%#v, %v), want incompatible", snapshot, err)
			}
			if _, err := os.Stat(stageDir); err != nil {
				t.Fatalf("incoherent evidence was removed: %v", err)
			}
			if _, err := os.Stat(stateDir); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("incoherent stage was promoted: %v", err)
			}
		})
	}
}

func TestAdminSnapshotRequiresRelayURLAndCompletedOwnerCoherence(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "Relay")
	state, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
	if err != nil {
		t.Fatal(err)
	}
	key := adminReplayKey("39393939393939393939393939393939", relayadmin.OperationSetup, "snapshot")
	_, csr := newDeviceCSR(t)
	executeAdminBootstrap(t, state, key, AdminBootstrapOwnerOptions{
		PublicName: "relay.example.ts.net", PublicURL: "https://relay.example.ts.net:8443",
		CSRPEM: csr, AdministrativeOwnerUID: 501,
	})
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	execAdminStateSQL(t, stateDir, `DELETE FROM settings WHERE key = 'relay_url'`)

	reopened, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
	if err != nil {
		t.Fatalf("OpenAdminState(incoherent authority) error = %v", err)
	}
	defer reopened.Close()
	snapshot, err := reopened.Snapshot(context.Background())
	if err != nil || snapshot.Class != AdminStateIncompatible || !snapshot.OwnerUIDBound || snapshot.AdministrativeOwnerUID != 501 {
		t.Fatalf("Snapshot(incoherent authority) = (%#v, %v), want incompatible with bound UID 501", snapshot, err)
	}
}

func TestAdminSnapshotNeverTrustsMalformedUIDInDegradedState(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "Relay")
	state, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
	if err != nil {
		t.Fatal(err)
	}
	key := adminReplayKey("49494949494949494949494949494949", relayadmin.OperationSetup, "malformed-uid")
	_, csr := newDeviceCSR(t)
	executeAdminBootstrap(t, state, key, AdminBootstrapOwnerOptions{
		PublicName: "relay.example.ts.net", PublicURL: "https://relay.example.ts.net:8443",
		CSRPEM: csr, AdministrativeOwnerUID: 501,
	})
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	execAdminStateSQL(t, stateDir, `UPDATE settings SET value = '0501' WHERE key = 'administrative_owner_uid'`)
	reopened := requireDegradedAdminState(t, stateDir)
	defer reopened.Close()
	snapshot, err := reopened.Snapshot(context.Background())
	if err != nil || snapshot.OwnerUIDBound || snapshot.AdministrativeOwnerUID != 0 {
		t.Fatalf("Snapshot(malformed UID) = (%#v, %v), want untrusted binding", snapshot, err)
	}
}

func TestAdminStateOpenRejectsUnexpectedSQLiteObjectsWithoutDeletingThem(t *testing.T) {
	for _, object := range unexpectedAdminSQLiteObjects() {
		t.Run(object.name, func(t *testing.T) {
			stateDir := filepath.Join(t.TempDir(), "Relay")
			state, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
			if err != nil {
				t.Fatal(err)
			}
			key := adminReplayKey("60606060606060606060606060606060", relayadmin.OperationSetup, object.name+"-normal")
			_, csr := newDeviceCSR(t)
			executeAdminBootstrap(t, state, key, AdminBootstrapOwnerOptions{
				PublicName: "relay.example.ts.net", PublicURL: "https://relay.example.ts.net:8443",
				CSRPEM: csr, AdministrativeOwnerUID: 501,
			})
			if err := state.Close(); err != nil {
				t.Fatal(err)
			}
			execAdminStateSQL(t, stateDir, object.createSQL)

			reopened, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
			if err != nil {
				t.Fatalf("OpenAdminState() error = %v, want degraded state", err)
			}
			defer reopened.Close()
			snapshot, err := reopened.Snapshot(context.Background())
			if err != nil || snapshot.Class != AdminStateIncompatible || !snapshot.OwnerUIDBound || snapshot.AdministrativeOwnerUID != 501 {
				t.Fatalf("Snapshot() = (%#v, %v), want incompatible with bound UID 501", snapshot, err)
			}
			requireAdminSQLiteObject(t, filepath.Join(stateDir, databaseFilename), object.objectType, object.objectName)
		})
	}
}

func TestAdminSetupStagePromotionRejectsUnexpectedSQLiteObjectsWithoutDeletingThem(t *testing.T) {
	for _, object := range unexpectedAdminSQLiteObjects() {
		t.Run(object.name, func(t *testing.T) {
			parent := t.TempDir()
			stateDir := filepath.Join(parent, "Relay")
			state, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
			if err != nil {
				t.Fatal(err)
			}
			key := adminReplayKey("61616161616161616161616161616161", relayadmin.OperationSetup, object.name+"-stage")
			_, csr := newDeviceCSR(t)
			executeAdminBootstrap(t, state, key, AdminBootstrapOwnerOptions{
				PublicName: "relay.example.ts.net", PublicURL: "https://relay.example.ts.net:8443",
				CSRPEM: csr, AdministrativeOwnerUID: 501,
			})
			if err := state.Close(); err != nil {
				t.Fatal(err)
			}
			execAdminStateSQL(t, stateDir, object.createSQL)
			stageDir := filepath.Join(parent, ".relay-setup-"+key.RequestID)
			if err := os.Rename(stateDir, stageDir); err != nil {
				t.Fatal(err)
			}

			reopened, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
			if err != nil {
				t.Fatalf("OpenAdminState(completed stage) error = %v, want degraded state", err)
			}
			defer reopened.Close()
			snapshot, err := reopened.Snapshot(context.Background())
			if err != nil || snapshot.Class != AdminStateIncompatible {
				t.Fatalf("Snapshot() = (%#v, %v), want incompatible", snapshot, err)
			}
			if _, err := os.Stat(stateDir); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("unexpected SQLite %s stage was promoted: %v", object.objectType, err)
			}
			if _, err := os.Stat(stageDir); err != nil {
				t.Fatalf("unexpected SQLite %s evidence was removed: %v", object.objectType, err)
			}
			requireAdminSQLiteObject(t, filepath.Join(stageDir, databaseFilename), object.objectType, object.objectName)
		})
	}
}

func TestAdminBootstrapReopenCleansOnlyExactPreAuthorityStage(t *testing.T) {
	parent := t.TempDir()
	stateDir := filepath.Join(parent, "Relay")
	requestID := "20202020202020202020202020202020"
	stageDir := filepath.Join(parent, ".relay-setup-"+requestID)
	if err := os.Mkdir(stageDir, 0o700); err != nil {
		t.Fatal(err)
	}
	state, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
	if err != nil {
		t.Fatalf("OpenAdminState(pre-authority stage) error = %v", err)
	}
	defer state.Close()
	snapshot, err := state.Snapshot(context.Background())
	if err != nil || snapshot.Class != AdminStateAbsent {
		t.Fatalf("Snapshot() after safe stage cleanup = (%#v, %v)", snapshot, err)
	}
	if _, err := os.Stat(stageDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("exact pre-authority stage remains: %v", err)
	}
}

func TestAdminBootstrapReopenCleansPreSchemaDatabaseCrash(t *testing.T) {
	for _, fixture := range []struct {
		name string
		make func(*testing.T, string)
	}{
		{name: "empty database", make: func(t *testing.T, path string) {
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "truncated SQLite header", make: func(t *testing.T, path string) {
			if err := os.WriteFile(path, []byte("SQLite format 3\x00partial"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "valid version zero without schema", make: func(t *testing.T, path string) {
			database, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := database.Exec(`PRAGMA application_id = 1`); err != nil {
				database.Close()
				t.Fatal(err)
			}
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			parent := t.TempDir()
			stateDir := filepath.Join(parent, "Relay")
			stageDir := filepath.Join(parent, ".relay-setup-25252525252525252525252525252525")
			if err := os.Mkdir(stageDir, 0o700); err != nil {
				t.Fatal(err)
			}
			fixture.make(t, filepath.Join(stageDir, databaseFilename))
			state, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
			if err != nil {
				t.Fatalf("OpenAdminState(pre-schema crash) error = %v", err)
			}
			defer state.Close()
			snapshot, err := state.Snapshot(context.Background())
			if err != nil || snapshot.Class != AdminStateAbsent {
				t.Fatalf("Snapshot() = (%#v, %v), want absent", snapshot, err)
			}
			if _, err := os.Stat(stageDir); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("pre-schema stage remains: %v", err)
			}
		})
	}
}

func TestAdminBootstrapReopenPreservesReplaylessStageWithUnexpectedState(t *testing.T) {
	parent := t.TempDir()
	stateDir := filepath.Join(parent, "Relay")
	stageDir := filepath.Join(parent, ".relay-setup-48484848484848484848484848484848")
	if err := os.Mkdir(stageDir, 0o700); err != nil {
		t.Fatal(err)
	}
	database, err := createStore(filepath.Join(stageDir, databaseFilename))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.Exec(`INSERT INTO settings(key, value) VALUES ('unexpected', 'evidence')`); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	state := requireDegradedAdminState(t, stateDir)
	defer state.Close()
	if _, err := os.Stat(stageDir); err != nil {
		t.Fatalf("ambiguous replayless evidence was removed: %v", err)
	}
}

func TestAdminBootstrapReopenPreservesAmbiguousVersionZeroDatabase(t *testing.T) {
	parent := t.TempDir()
	stateDir := filepath.Join(parent, "Relay")
	stageDir := filepath.Join(parent, ".relay-setup-46464646464646464646464646464646")
	if err := os.Mkdir(stageDir, 0o700); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", filepath.Join(stageDir, databaseFilename))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TABLE survived(value TEXT) STRICT`); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	state := requireDegradedAdminState(t, stateDir)
	defer state.Close()
	if _, err := os.Stat(stageDir); err != nil {
		t.Fatalf("ambiguous version-zero evidence was removed: %v", err)
	}
}

func TestAdminBootstrapReopenRejectsAmbiguousStage(t *testing.T) {
	parent := t.TempDir()
	stateDir := filepath.Join(parent, "Relay")
	if err := os.Mkdir(filepath.Join(parent, ".relay-setup-not-a-request-id"), 0o700); err != nil {
		t.Fatal(err)
	}
	state := requireDegradedAdminState(t, stateDir)
	defer state.Close()
}

func TestAdminBootstrapReopenRejectsUnknownCompletedStageContent(t *testing.T) {
	parent := t.TempDir()
	stateDir := filepath.Join(parent, "Relay")
	state, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
	if err != nil {
		t.Fatal(err)
	}
	key := adminReplayKey("21212121212121212121212121212121", relayadmin.OperationSetup, "unknown")
	_, csr := newDeviceCSR(t)
	executeAdminBootstrap(t, state, key, AdminBootstrapOwnerOptions{
		PublicName: "relay.example.ts.net", PublicURL: "https://relay.example.ts.net:8443", CSRPEM: csr, AdministrativeOwnerUID: 501,
	})
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	stageDir := filepath.Join(parent, ".relay-setup-"+key.RequestID)
	if err := os.Rename(stateDir, stageDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stageDir, "unexpected"), []byte("do not promote"), 0o600); err != nil {
		t.Fatal(err)
	}
	reopened := requireDegradedAdminState(t, stateDir)
	defer reopened.Close()
	if _, err := os.Stat(stageDir); err != nil {
		t.Fatalf("unknown stage evidence was removed: %v", err)
	}
}

func TestAdminBootstrapReopenRejectsStageBesideAuthoritativeState(t *testing.T) {
	parent := t.TempDir()
	stateDir := filepath.Join(parent, "Relay")
	state, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
	if err != nil {
		t.Fatal(err)
	}
	key := adminReplayKey("22222222222222222222222222222222", relayadmin.OperationSetup, "authoritative")
	_, csr := newDeviceCSR(t)
	executeAdminBootstrap(t, state, key, AdminBootstrapOwnerOptions{
		PublicName: "relay.example.ts.net", PublicURL: "https://relay.example.ts.net:8443", CSRPEM: csr, AdministrativeOwnerUID: 501,
	})
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(parent, ".relay-setup-23232323232323232323232323232323"), 0o700); err != nil {
		t.Fatal(err)
	}
	reopened := requireDegradedAdminState(t, stateDir)
	defer reopened.Close()
}

func executeAdminBootstrap(t *testing.T, state *AdminState, key relayadmin.ReplayKey, options AdminBootstrapOwnerOptions) ([]byte, EnrollmentResult) {
	t.Helper()
	reservation, err := state.ReplayStore().Reserve(context.Background(), key)
	if err != nil || reservation.Decision != relayadmin.ReplayExecute || reservation.Mutation == nil {
		t.Fatalf("Reserve() = (%#v, %v), want executable setup", reservation, err)
	}
	var enrollmentResult EnrollmentResult
	response, err := reservation.Mutation.Execute(context.Background(), func(ctx context.Context, transaction relayadmin.MutationTransaction) ([]byte, error) {
		result, err := state.BootstrapOwner(ctx, transaction, options)
		if err != nil {
			return nil, err
		}
		enrollmentResult = result
		return marshalAdminBootstrapResponse(key, result)
	})
	if err != nil {
		t.Fatalf("Execute(BootstrapOwner) error = %v", err)
	}
	return response, enrollmentResult
}

func marshalAdminBootstrapResponse(key relayadmin.ReplayKey, result EnrollmentResult) ([]byte, error) {
	return relayadmin.MarshalSuccessResponse(key.RequestID, key.Operation, relayadmin.OwnerBootstrapResult{
		CertificatePEM: result.CertificatePEM, CACertificatePEM: result.CACertificatePEM,
		Serial: result.Serial, Role: string(result.Role),
	})
}

func execAdminStateSQL(t *testing.T, stateDir, statement string) {
	t.Helper()
	database, err := sql.Open("sqlite", filepath.Join(stateDir, databaseFilename))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(statement); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
}

func rewriteAdminSetupResult(t *testing.T, stateDir string, key relayadmin.ReplayKey, rewrite func(*relayadmin.OwnerBootstrapResult)) {
	t.Helper()
	database, err := sql.Open("sqlite", filepath.Join(stateDir, databaseFilename))
	if err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err := database.QueryRow(`SELECT response FROM admin_mutation_replay WHERE request_id = ?`, key.RequestID).Scan(&raw); err != nil {
		database.Close()
		t.Fatal(err)
	}
	parsed, err := relayadmin.ParseResponse(raw)
	if err != nil {
		database.Close()
		t.Fatal(err)
	}
	result, ok := parsed.Result.(relayadmin.OwnerBootstrapResult)
	if !ok {
		database.Close()
		t.Fatalf("setup result type = %T", parsed.Result)
	}
	rewrite(&result)
	raw, err = relayadmin.MarshalSuccessResponse(key.RequestID, key.Operation, result)
	if err != nil {
		database.Close()
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE admin_mutation_replay SET response = ? WHERE request_id = ?`, raw, key.RequestID); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
}

func requireDegradedAdminState(t *testing.T, stateDir string) *AdminState {
	t.Helper()
	state, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
	if err != nil {
		t.Fatalf("OpenAdminState(degraded) error = %v", err)
	}
	if state == nil {
		t.Fatal("OpenAdminState(degraded) returned nil state")
	}
	snapshot, err := state.Snapshot(context.Background())
	if err != nil || snapshot.Class != AdminStateIncompatible {
		state.Close()
		t.Fatalf("degraded Snapshot() = (%#v, %v), want incompatible", snapshot, err)
	}
	return state
}

type unexpectedAdminSQLiteObject struct {
	name       string
	objectType string
	objectName string
	createSQL  string
}

func unexpectedAdminSQLiteObjects() []unexpectedAdminSQLiteObject {
	return []unexpectedAdminSQLiteObject{
		{name: "table", objectType: "table", objectName: "unexpected_table", createSQL: `CREATE TABLE unexpected_table(value TEXT) STRICT`},
		{name: "view", objectType: "view", objectName: "unexpected_view", createSQL: `CREATE VIEW unexpected_view AS SELECT key, value FROM settings`},
		{name: "trigger", objectType: "trigger", objectName: "unexpected_trigger", createSQL: `CREATE TRIGGER unexpected_trigger AFTER UPDATE ON settings BEGIN UPDATE metrics SET byte_count = byte_count WHERE singleton_id = 1; END`},
		{name: "user index", objectType: "index", objectName: "unexpected_index", createSQL: `CREATE INDEX unexpected_index ON settings(value)`},
	}
}

func requireAdminSQLiteObject(t *testing.T, databasePath, objectType, objectName string) {
	t.Helper()
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = ? AND name = ?`, objectType, objectName).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("sqlite_master %s %q count = %d, want preserved object", objectType, objectName, count)
	}
}
