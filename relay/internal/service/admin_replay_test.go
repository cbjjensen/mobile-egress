package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"mobile-egress/internal/relayadmin"
)

func TestAdministrativeOwnerUIDCanonicalEncoding(t *testing.T) {
	state, err := createStore(filepath.Join(t.TempDir(), databaseFilename))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	for _, uid := range []uint32{0, 1, ^uint32(0)} {
		if err := state.setAdministrativeOwnerUID(context.Background(), uid); err != nil {
			t.Fatalf("setAdministrativeOwnerUID(%d) error = %v", uid, err)
		}
		got, bound, err := state.administrativeOwnerUID(context.Background())
		if err != nil {
			t.Fatalf("administrativeOwnerUID(%d) error = %v", uid, err)
		}
		if !bound || got != uid {
			t.Fatalf("administrativeOwnerUID() = (%d, %v), want (%d, true)", got, bound, uid)
		}
	}

	for _, value := range []string{"", " 1", "1 ", "+1", "-1", "01", "00", "4294967296", "1x"} {
		t.Run(value, func(t *testing.T) {
			if _, err := state.db.Exec(`INSERT INTO settings(key, value) VALUES ('administrative_owner_uid', ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, value); err != nil {
				t.Fatal(err)
			}
			if _, _, err := state.administrativeOwnerUID(context.Background()); !errors.Is(err, ErrAdminStateIncompatible) {
				t.Fatalf("administrativeOwnerUID(%q) error = %v, want ErrAdminStateIncompatible", value, err)
			}
		})
	}
}

func TestAdminReplayPersistsCompletedResponseAcrossReopen(t *testing.T) {
	stateDir := createAdminStateDatabase(t)
	finished := 0
	state, err := OpenAdminState(AdminStateOptions{
		StateDir: stateDir,
		MutationFinished: func(relayadmin.ReplayKey) {
			finished++
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	key := adminReplayKey("11111111111111111111111111111111", relayadmin.OperationSetup, "first")
	reservation, err := state.ReplayStore().Reserve(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	if reservation.Decision != relayadmin.ReplayExecute || reservation.Mutation == nil {
		t.Fatalf("Reserve() = %#v, want executable mutation", reservation)
	}
	response, err := relayadmin.MarshalErrorResponse(key.RequestID, key.Operation, relayadmin.ErrorAlreadyInitialized)
	if err != nil {
		t.Fatal(err)
	}
	callbackCalled := false
	got, err := reservation.Mutation.Execute(context.Background(), func(_ context.Context, transaction relayadmin.MutationTransaction) ([]byte, error) {
		callbackCalled = true
		if transaction.ReplayKey() != key {
			t.Fatalf("transaction key = %#v, want %#v", transaction.ReplayKey(), key)
		}
		return response, nil
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !callbackCalled || !bytes.Equal(got, response) || finished != 1 {
		t.Fatalf("Execute() = (%q, callback=%v, finished=%d), want exact response and one callback", got, callbackCalled, finished)
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
	if err != nil {
		t.Fatal(err)
	}
	if cached.Decision != relayadmin.ReplayCached || !bytes.Equal(cached.Response, response) {
		t.Fatalf("reopened Reserve() = %#v, want exact cached response", cached)
	}

	differentDigest := key
	differentDigest.Digest = sha256.Sum256([]byte("different"))
	duplicate, err := reopened.ReplayStore().Reserve(context.Background(), differentDigest)
	if err != nil || duplicate.Decision != relayadmin.ReplayDuplicate {
		t.Fatalf("different digest Reserve() = (%#v, %v), want duplicate", duplicate, err)
	}
	differentOperation := key
	differentOperation.Operation = relayadmin.OperationRotate
	duplicate, err = reopened.ReplayStore().Reserve(context.Background(), differentOperation)
	if err != nil || duplicate.Decision != relayadmin.ReplayDuplicate {
		t.Fatalf("different operation Reserve() = (%#v, %v), want duplicate", duplicate, err)
	}
}

func TestAdminReplayRequestIDNamespaceSpansStatusAndMutations(t *testing.T) {
	stateDir := createAdminStateDatabase(t)
	state, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	requestID := "24242424242424242424242424242424"
	statusKey := adminReplayKey(requestID, relayadmin.OperationStatus, "status")
	statusReservation, err := state.ReplayStore().Reserve(context.Background(), statusKey)
	if err != nil || statusReservation.Decision != relayadmin.ReplayExecute {
		t.Fatalf("status Reserve() = (%#v, %v)", statusReservation, err)
	}
	mutationKey := adminReplayKey(requestID, relayadmin.OperationRepair, "repair")
	mutationReservation, err := state.ReplayStore().Reserve(context.Background(), mutationKey)
	if err != nil || mutationReservation.Decision != relayadmin.ReplayDuplicate {
		t.Fatalf("mutation sharing active status ID = (%#v, %v), want duplicate", mutationReservation, err)
	}
	if err := state.ReplayStore().AbandonStatus(context.Background(), statusKey); err != nil {
		t.Fatal(err)
	}
	completedStatusKey := adminReplayKey("47474747474747474747474747474747", relayadmin.OperationStatus, "completed-status")
	completedStatus, err := state.ReplayStore().Reserve(context.Background(), completedStatusKey)
	if err != nil || completedStatus.Decision != relayadmin.ReplayExecute {
		t.Fatalf("completed status Reserve() = (%#v, %v)", completedStatus, err)
	}
	statusResponse, err := relayadmin.MarshalSuccessResponse(completedStatusKey.RequestID, completedStatusKey.Operation, relayadmin.StatusResult{
		ProtocolVersion: relayadmin.Version, HelperVersion: "test", Initialized: false, RelayRunning: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.ReplayStore().CompleteStatus(context.Background(), completedStatusKey, statusResponse); err != nil {
		t.Fatal(err)
	}
	completedCollision := completedStatusKey
	completedCollision.Operation = relayadmin.OperationRepair
	completedCollision.Digest[0] ^= 0xff
	completedMutation, err := state.ReplayStore().Reserve(context.Background(), completedCollision)
	if err != nil || completedMutation.Decision != relayadmin.ReplayDuplicate {
		t.Fatalf("mutation sharing completed status ID = (%#v, %v), want duplicate", completedMutation, err)
	}
	mutationReservation, err = state.ReplayStore().Reserve(context.Background(), mutationKey)
	if err != nil || mutationReservation.Decision != relayadmin.ReplayExecute {
		t.Fatalf("mutation after status abandon = (%#v, %v), want execute", mutationReservation, err)
	}
	statusReservation, err = state.ReplayStore().Reserve(context.Background(), statusKey)
	if err != nil || statusReservation.Decision != relayadmin.ReplayDuplicate {
		t.Fatalf("status sharing durable mutation ID = (%#v, %v), want duplicate", statusReservation, err)
	}
	response, _ := relayadmin.MarshalErrorResponse(mutationKey.RequestID, mutationKey.Operation, relayadmin.ErrorNotInitialized)
	if _, err := mutationReservation.Mutation.Execute(context.Background(), func(context.Context, relayadmin.MutationTransaction) ([]byte, error) {
		return response, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	statusReservation, err = reopened.ReplayStore().Reserve(context.Background(), statusKey)
	if err != nil || statusReservation.Decision != relayadmin.ReplayDuplicate {
		t.Fatalf("status sharing reopened mutation ID = (%#v, %v), want duplicate", statusReservation, err)
	}
}

func TestAdminReplayUnfinishedRowsFailClosedAfterReopen(t *testing.T) {
	for _, replayState := range []string{"reserved", "executing", "indeterminate"} {
		t.Run(replayState, func(t *testing.T) {
			stateDir := createAdminStateDatabase(t)
			state, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
			if err != nil {
				t.Fatal(err)
			}
			key := adminReplayKey("22222222222222222222222222222222", relayadmin.OperationRotate, replayState)
			reservation, err := state.ReplayStore().Reserve(context.Background(), key)
			if err != nil || reservation.Decision != relayadmin.ReplayExecute {
				t.Fatalf("initial Reserve() = (%#v, %v)", reservation, err)
			}
			if err := state.Close(); err != nil {
				t.Fatal(err)
			}
			if replayState != "reserved" {
				database, err := openStore(filepath.Join(stateDir, databaseFilename))
				if err != nil {
					t.Fatal(err)
				}
				if _, err := database.db.Exec(`UPDATE admin_mutation_replay SET state = ? WHERE request_id = ?`, replayState, key.RequestID); err != nil {
					database.Close()
					t.Fatal(err)
				}
				database.Close()
			}
			reopened, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			got, err := reopened.ReplayStore().Reserve(context.Background(), key)
			if err != nil || got.Decision != relayadmin.ReplayBusy {
				t.Fatalf("Reserve(%s after reopen) = (%#v, %v), want busy", replayState, got, err)
			}
		})
	}
}

func TestAdminReplayAbandonAndCancellationSemantics(t *testing.T) {
	stateDir := createAdminStateDatabase(t)
	state, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	key := adminReplayKey("33333333333333333333333333333333", relayadmin.OperationRepair, "abandon")
	first, err := state.ReplayStore().Reserve(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	activeDuplicate, err := state.ReplayStore().Reserve(context.Background(), key)
	if err != nil || activeDuplicate.Decision != relayadmin.ReplayDuplicate {
		t.Fatalf("active duplicate = (%#v, %v), want duplicate", activeDuplicate, err)
	}
	if err := first.Mutation.Abandon(context.Background()); err != nil {
		t.Fatalf("pre-execution Abandon() error = %v", err)
	}
	second, err := state.ReplayStore().Reserve(context.Background(), key)
	if err != nil || second.Decision != relayadmin.ReplayExecute {
		t.Fatalf("Reserve() after abandon = (%#v, %v), want execute", second, err)
	}
	response, _ := relayadmin.MarshalErrorResponse(key.RequestID, key.Operation, relayadmin.ErrorOperationFailed)
	if _, err := second.Mutation.Execute(context.Background(), func(context.Context, relayadmin.MutationTransaction) ([]byte, error) {
		return response, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := second.Mutation.Abandon(context.Background()); !errors.Is(err, relayadmin.ErrReplayState) {
		t.Fatalf("post-execution Abandon() error = %v, want ErrReplayState", err)
	}

	cancelKey := adminReplayKey("44444444444444444444444444444444", relayadmin.OperationRepair, "cancel")
	cancelReservation, err := state.ReplayStore().Reserve(context.Background(), cancelKey)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	_, err = cancelReservation.Mutation.Execute(ctx, func(context.Context, relayadmin.MutationTransaction) ([]byte, error) {
		cancel()
		return nil, context.Canceled
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Execute() error = %v, want context.Canceled", err)
	}
	got, err := state.ReplayStore().Reserve(context.Background(), cancelKey)
	if err != nil || got.Decision != relayadmin.ReplayBusy {
		t.Fatalf("canceled mutation retry = (%#v, %v), want busy", got, err)
	}
}

func TestAdminReplayCancellationAfterGateAcquisitionCleansReservation(t *testing.T) {
	for _, test := range []struct {
		name     string
		stateDir func(*testing.T) string
	}{
		{name: "durable", stateDir: createAdminStateDatabase},
		{name: "fresh", stateDir: func(t *testing.T) string { return filepath.Join(t.TempDir(), "Relay") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			state, err := OpenAdminState(AdminStateOptions{StateDir: test.stateDir(t), MutationCapacity: 1})
			if err != nil {
				t.Fatal(err)
			}
			defer state.Close()
			key := adminReplayKey("50505050505050505050505050505050", relayadmin.OperationRepair, test.name)
			reservation, err := state.ReplayStore().Reserve(context.Background(), key)
			if err != nil || reservation.Decision != relayadmin.ReplayExecute {
				t.Fatalf("Reserve() = (%#v, %v)", reservation, err)
			}
			acquired := make(chan struct{})
			proceed := make(chan struct{})
			state.afterMutationGateAcquire = func() {
				close(acquired)
				<-proceed
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			called := atomic.Bool{}
			go func() {
				_, executeErr := reservation.Mutation.Execute(ctx, func(context.Context, relayadmin.MutationTransaction) ([]byte, error) {
					called.Store(true)
					return nil, nil
				})
				done <- executeErr
			}()
			<-acquired
			cancel()
			close(proceed)
			if err := <-done; !errors.Is(err, context.Canceled) {
				t.Fatalf("Execute() error = %v, want context.Canceled", err)
			}
			if called.Load() {
				t.Fatal("canceled queued mutation invoked handler")
			}
			state.afterMutationGateAcquire = nil
			nextKey := adminReplayKey("51515151515151515151515151515151", relayadmin.OperationRepair, test.name+"-next")
			next, err := state.ReplayStore().Reserve(context.Background(), nextKey)
			if err != nil || next.Decision != relayadmin.ReplayExecute {
				t.Fatalf("capacity after queued cancellation = (%#v, %v), want execute", next, err)
			}
		})
	}
}

func TestAdminReplayCapacityRejectsBeforeExecution(t *testing.T) {
	stateDir := createAdminStateDatabase(t)
	state, err := OpenAdminState(AdminStateOptions{StateDir: stateDir, MutationCapacity: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	for index, requestID := range []string{
		"55555555555555555555555555555555",
		"66666666666666666666666666666666",
		"77777777777777777777777777777777",
	} {
		key := adminReplayKey(requestID, relayadmin.OperationRepair, requestID)
		reservation, err := state.ReplayStore().Reserve(context.Background(), key)
		if err != nil {
			t.Fatal(err)
		}
		if index < 2 && reservation.Decision != relayadmin.ReplayExecute {
			t.Fatalf("reservation %d decision = %v, want execute", index, reservation.Decision)
		}
		if index == 2 && reservation.Decision != relayadmin.ReplayBusy {
			t.Fatalf("third reservation decision = %v, want busy", reservation.Decision)
		}
	}
}

func TestAdminReplayAbandonReleasesCapacity(t *testing.T) {
	stateDir := createAdminStateDatabase(t)
	state, err := OpenAdminState(AdminStateOptions{StateDir: stateDir, MutationCapacity: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	firstKey := adminReplayKey("12121212121212121212121212121212", relayadmin.OperationRepair, "first")
	first, err := state.ReplayStore().Reserve(context.Background(), firstKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Mutation.Abandon(context.Background()); err != nil {
		t.Fatal(err)
	}
	secondKey := adminReplayKey("13131313131313131313131313131313", relayadmin.OperationRepair, "second")
	second, err := state.ReplayStore().Reserve(context.Background(), secondKey)
	if err != nil || second.Decision != relayadmin.ReplayExecute {
		t.Fatalf("Reserve() after abandoned cap slot = (%#v, %v), want execute", second, err)
	}
}

func TestAdminAbsentRotateAndRepairAreNotInitialized(t *testing.T) {
	state, err := OpenAdminState(AdminStateOptions{StateDir: filepath.Join(t.TempDir(), "Relay")})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	tests := []struct {
		requestID string
		operation relayadmin.Operation
		invoke    func(context.Context, relayadmin.MutationTransaction) error
	}{
		{
			requestID: "14141414141414141414141414141414", operation: relayadmin.OperationRotate,
			invoke: func(ctx context.Context, transaction relayadmin.MutationTransaction) error {
				_, err := state.RotateEndpoint(ctx, transaction, RotateEndpointOptions{})
				return err
			},
		},
		{
			requestID: "15151515151515151515151515151515", operation: relayadmin.OperationRepair,
			invoke: func(ctx context.Context, transaction relayadmin.MutationTransaction) error {
				return state.Repair(ctx, transaction)
			},
		},
	}
	for _, test := range tests {
		t.Run(string(test.operation), func(t *testing.T) {
			key := adminReplayKey(test.requestID, test.operation, string(test.operation))
			reserved, err := state.ReplayStore().Reserve(context.Background(), key)
			if err != nil || reserved.Decision != relayadmin.ReplayExecute {
				t.Fatalf("Reserve() = (%#v, %v)", reserved, err)
			}
			_, err = reserved.Mutation.Execute(context.Background(), func(ctx context.Context, transaction relayadmin.MutationTransaction) ([]byte, error) {
				return nil, test.invoke(ctx, transaction)
			})
			if !errors.Is(err, ErrAdminNotInitialized) {
				t.Fatalf("absent %s error = %v, want ErrAdminNotInitialized", test.operation, err)
			}
		})
	}
}

func TestAdminAbsentReplayCapacityAndCleanupStayBounded(t *testing.T) {
	state, err := OpenAdminState(AdminStateOptions{
		StateDir: filepath.Join(t.TempDir(), "Relay"), MutationCapacity: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	firstKey := adminReplayKey("41414141414141414141414141414141", relayadmin.OperationSetup, "first")
	first, err := state.ReplayStore().Reserve(context.Background(), firstKey)
	if err != nil || first.Decision != relayadmin.ReplayExecute {
		t.Fatalf("first Reserve() = (%#v, %v)", first, err)
	}
	secondKey := adminReplayKey("42424242424242424242424242424242", relayadmin.OperationSetup, "second")
	second, err := state.ReplayStore().Reserve(context.Background(), secondKey)
	if err != nil || second.Decision != relayadmin.ReplayBusy {
		t.Fatalf("second Reserve() = (%#v, %v), want busy", second, err)
	}
	if err := first.Mutation.Abandon(context.Background()); err != nil {
		t.Fatal(err)
	}
	second, err = state.ReplayStore().Reserve(context.Background(), secondKey)
	if err != nil || second.Decision != relayadmin.ReplayExecute {
		t.Fatalf("Reserve() after abandon = (%#v, %v), want execute", second, err)
	}
	if err := second.Mutation.Abandon(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(state.active) != 0 || len(state.mutationKeys) != 0 {
		t.Fatalf("fresh replay slots leaked: active=%d mutationKeys=%d", len(state.active), len(state.mutationKeys))
	}
}

func TestAdminDatabaseLessDegradedRepairRemainsIndeterminateAndChargesCapacity(t *testing.T) {
	callbackErr := errors.New("injected recovery failure")
	tests := []struct {
		name      string
		execution func(context.Context, relayadmin.ReplayKey) ([]byte, error)
		wantErr   error
	}{
		{
			name: "callback failure",
			execution: func(_ context.Context, _ relayadmin.ReplayKey) ([]byte, error) {
				return nil, callbackErr
			},
			wantErr: callbackErr,
		},
		{
			name: "cancellation after callback begins",
			execution: func(ctx context.Context, _ relayadmin.ReplayKey) ([]byte, error) {
				return nil, ctx.Err()
			},
			wantErr: context.Canceled,
		},
		{
			name: "apparent success without durable completion",
			execution: func(_ context.Context, key relayadmin.ReplayKey) ([]byte, error) {
				return relayadmin.MarshalSuccessResponse(key.RequestID, key.Operation, relayadmin.RepairResult{Ready: true, Restarting: true})
			},
			wantErr: relayadmin.ErrMutationIndeterminate,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			guard := &recordingAdminPathGuard{validateErr: errors.New("incompatible state")}
			state, err := OpenAdminState(AdminStateOptions{
				StateDir: filepath.Join(t.TempDir(), "Relay"), PathGuard: guard, MutationCapacity: 1,
			})
			if err != nil {
				t.Fatal(err)
			}
			defer state.Close()
			key := adminReplayKey("57575757575757575757575757575757", relayadmin.OperationRepair, test.name)
			reserved, err := state.ReplayStore().Reserve(context.Background(), key)
			if err != nil || reserved.Decision != relayadmin.ReplayExecute {
				t.Fatalf("Reserve() = (%#v, %v), want execute", reserved, err)
			}
			callbackCount := atomic.Int32{}
			executeContext := context.Background()
			execution := test.execution
			if test.name == "cancellation after callback begins" {
				var cancel context.CancelFunc
				executeContext, cancel = context.WithCancel(context.Background())
				defer cancel()
				originalExecution := execution
				execution = func(ctx context.Context, key relayadmin.ReplayKey) ([]byte, error) {
					cancel()
					return originalExecution(ctx, key)
				}
			}
			_, err = reserved.Mutation.Execute(executeContext, func(ctx context.Context, _ relayadmin.MutationTransaction) ([]byte, error) {
				callbackCount.Add(1)
				return execution(ctx, key)
			})
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("Execute() error = %v, want %v", err, test.wantErr)
			}
			if callbackCount.Load() != 1 {
				t.Fatalf("callback count = %d, want 1", callbackCount.Load())
			}
			same, err := state.ReplayStore().Reserve(context.Background(), key)
			if err != nil || same.Decision != relayadmin.ReplayBusy {
				t.Fatalf("same-ID retry = (%#v, %v), want busy", same, err)
			}
			collisionKey := adminReplayKey(key.RequestID, relayadmin.OperationRepair, test.name+"-collision")
			collision, err := state.ReplayStore().Reserve(context.Background(), collisionKey)
			if err != nil || collision.Decision != relayadmin.ReplayDuplicate {
				t.Fatalf("same-ID collision = (%#v, %v), want duplicate", collision, err)
			}
			nextKey := adminReplayKey("58585858585858585858585858585858", relayadmin.OperationRepair, test.name+"-next")
			next, err := state.ReplayStore().Reserve(context.Background(), nextKey)
			if err != nil || next.Decision != relayadmin.ReplayBusy {
				t.Fatalf("capacity after indeterminate repair = (%#v, %v), want busy", next, err)
			}
			if err := reserved.Mutation.Abandon(context.Background()); !errors.Is(err, relayadmin.ErrReplayState) {
				t.Fatalf("Abandon() after callback error = %v, want ErrReplayState", err)
			}
			if len(state.active) != 0 || len(state.pending) != 0 || len(state.uncertain) != 1 || len(state.mutationKeys) != 1 {
				t.Fatalf("unexpected degraded repair accounting: active=%d pending=%d uncertain=%d keys=%d",
					len(state.active), len(state.pending), len(state.uncertain), len(state.mutationKeys))
			}
		})
	}
}

func TestAdminPreReservedDegradedRepairsRevalidateBeforeSecondCallback(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "Relay")
	state, err := OpenAdminState(AdminStateOptions{StateDir: stateDir, MutationCapacity: 3})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	firstRepairKey := adminReplayKey("59595959595959595959595959595959", relayadmin.OperationRepair, "first-recovery")
	secondRepairKey := adminReplayKey("5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a", relayadmin.OperationRepair, "second-recovery")
	firstRepair, err := state.ReplayStore().Reserve(context.Background(), firstRepairKey)
	if err != nil || firstRepair.Decision != relayadmin.ReplayExecute {
		t.Fatalf("first repair Reserve() = (%#v, %v), want execute", firstRepair, err)
	}
	secondRepair, err := state.ReplayStore().Reserve(context.Background(), secondRepairKey)
	if err != nil || secondRepair.Decision != relayadmin.ReplayExecute {
		t.Fatalf("pre-degraded second repair Reserve() = (%#v, %v), want execute", secondRepair, err)
	}

	setupKey := adminReplayKey("5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b", relayadmin.OperationSetup, "cleanup-uncertain")
	setup, err := state.ReplayStore().Reserve(context.Background(), setupKey)
	if err != nil || setup.Decision != relayadmin.ReplayExecute {
		t.Fatalf("setup Reserve() = (%#v, %v), want execute", setup, err)
	}
	state.beforeSetupDatabase = func() error { return errors.New("injected setup failure") }
	state.removeSetupStage = func(string) error { return errors.New("injected cleanup failure") }
	_, csr := newDeviceCSR(t)
	_, err = setup.Mutation.Execute(context.Background(), func(ctx context.Context, transaction relayadmin.MutationTransaction) ([]byte, error) {
		_, setupErr := state.BootstrapOwner(ctx, transaction, AdminBootstrapOwnerOptions{
			PublicName: "relay.example.ts.net", PublicURL: "https://relay.example.ts.net:8443",
			CSRPEM: csr, AdministrativeOwnerUID: 501,
		})
		return nil, setupErr
	})
	if !errors.Is(err, relayadmin.ErrMutationIndeterminate) {
		t.Fatalf("uncertain setup Execute() error = %v, want ErrMutationIndeterminate", err)
	}

	firstCallbacks := atomic.Int32{}
	_, err = firstRepair.Mutation.Execute(context.Background(), func(ctx context.Context, transaction relayadmin.MutationTransaction) ([]byte, error) {
		firstCallbacks.Add(1)
		return nil, state.Repair(ctx, transaction)
	})
	if !errors.Is(err, ErrAdminStateIncompatible) || firstCallbacks.Load() != 1 {
		t.Fatalf("first recovery repair = (callbacks=%d, error=%v), want one callback and incompatible state", firstCallbacks.Load(), err)
	}
	secondCallbacks := atomic.Int32{}
	_, err = secondRepair.Mutation.Execute(context.Background(), func(context.Context, relayadmin.MutationTransaction) ([]byte, error) {
		secondCallbacks.Add(1)
		return nil, errors.New("second repair must not run")
	})
	if !errors.Is(err, relayadmin.ErrReplayState) || secondCallbacks.Load() != 0 {
		t.Fatalf("pre-reserved second repair = (callbacks=%d, error=%v), want no callback and ErrReplayState", secondCallbacks.Load(), err)
	}
	firstRetry, err := state.ReplayStore().Reserve(context.Background(), firstRepairKey)
	if err != nil || firstRetry.Decision != relayadmin.ReplayBusy {
		t.Fatalf("first repair retry = (%#v, %v), want busy", firstRetry, err)
	}
	secondRetry, err := state.ReplayStore().Reserve(context.Background(), secondRepairKey)
	if err != nil || secondRetry.Decision != relayadmin.ReplayBusy {
		t.Fatalf("pre-execution second repair retry under uncertainty = (%#v, %v), want busy", secondRetry, err)
	}
	if _, retained := state.mutationKeys[secondRepairKey.RequestID]; retained {
		t.Fatal("pre-execution second repair incorrectly retained a durable mutation key")
	}
	if len(state.active) != 0 || len(state.pending) != 0 || len(state.uncertain) != 2 || len(state.mutationKeys) != 2 {
		t.Fatalf("unexpected concurrent recovery accounting: active=%d pending=%d uncertain=%d keys=%d",
			len(state.active), len(state.pending), len(state.uncertain), len(state.mutationKeys))
	}
}

func TestAdminDatabaseLessDegradedRepairAllowsOnlyOneLiveReservation(t *testing.T) {
	state, err := OpenAdminState(AdminStateOptions{
		StateDir: filepath.Join(t.TempDir(), "Relay"), MutationCapacity: 2,
		PathGuard: &recordingAdminPathGuard{validateErr: errors.New("incompatible state")},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	olderUncertainKey := adminReplayKey("5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e", relayadmin.OperationSetup, "older-uncertain-setup")
	state.retainAdminUncertain(olderUncertainKey)
	firstKey := adminReplayKey("5c5c5c5c5c5c5c5c5c5c5c5c5c5c5c5c", relayadmin.OperationRepair, "first-live-repair")
	secondKey := adminReplayKey("5d5d5d5d5d5d5d5d5d5d5d5d5d5d5d5d", relayadmin.OperationRepair, "second-live-repair")
	first, err := state.ReplayStore().Reserve(context.Background(), firstKey)
	if err != nil || first.Decision != relayadmin.ReplayExecute {
		t.Fatalf("first Reserve() = (%#v, %v), want execute", first, err)
	}
	olderExact, err := state.ReplayStore().Reserve(context.Background(), olderUncertainKey)
	if err != nil || olderExact.Decision != relayadmin.ReplayBusy {
		t.Fatalf("older exact indeterminate while repair active = (%#v, %v), want busy", olderExact, err)
	}
	olderCollisionKey := adminReplayKey(olderUncertainKey.RequestID, relayadmin.OperationRepair, "older-uncertain-collision")
	olderCollision, err := state.ReplayStore().Reserve(context.Background(), olderCollisionKey)
	if err != nil || olderCollision.Decision != relayadmin.ReplayDuplicate {
		t.Fatalf("older uncertain collision while repair active = (%#v, %v), want duplicate", olderCollision, err)
	}
	second, err := state.ReplayStore().Reserve(context.Background(), secondKey)
	if err != nil || second.Decision != relayadmin.ReplayBusy {
		t.Fatalf("second live degraded repair Reserve() = (%#v, %v), want busy", second, err)
	}
	if err := first.Mutation.Abandon(context.Background()); err != nil {
		t.Fatalf("first Abandon() error = %v", err)
	}
	second, err = state.ReplayStore().Reserve(context.Background(), secondKey)
	if err != nil || second.Decision != relayadmin.ReplayExecute {
		t.Fatalf("second Reserve() after pre-execution abandon = (%#v, %v), want execute", second, err)
	}
	if err := second.Mutation.Abandon(context.Background()); err != nil {
		t.Fatalf("second Abandon() error = %v", err)
	}
	if len(state.active) != 0 || len(state.mutationKeys) != 1 || len(state.uncertain) != 1 {
		t.Fatalf("live degraded repair slots leaked: active=%d keys=%d uncertain=%d",
			len(state.active), len(state.mutationKeys), len(state.uncertain))
	}
}

func TestAdminReadyMutationReadsSnapshotThroughOwnedTransaction(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "Relay")
	state, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	_, csr := newDeviceCSR(t)
	setupKey := adminReplayKey("17171717171717171717171717171717", relayadmin.OperationSetup, "setup")
	executeAdminBootstrap(t, state, setupKey, AdminBootstrapOwnerOptions{
		PublicName: "relay.example.ts.net", PublicURL: "https://relay.example.ts.net:8443", CSRPEM: csr, AdministrativeOwnerUID: 501,
	})
	rotateKey := adminReplayKey("18181818181818181818181818181818", relayadmin.OperationRotate, "rotate")
	reserved, err := state.ReplayStore().Reserve(context.Background(), rotateKey)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_, err = reserved.Mutation.Execute(ctx, func(ctx context.Context, transaction relayadmin.MutationTransaction) ([]byte, error) {
		snapshot, snapshotErr := state.Snapshot(ctx)
		if snapshotErr != nil || snapshot.Class != AdminStateReady || snapshot.AdministrativeOwnerUID != 501 {
			return nil, errors.New("second authorization snapshot unavailable")
		}
		result, err := state.RotateEndpoint(ctx, transaction, RotateEndpointOptions{
			StateDir: stateDir, PublicName: "new.example.ts.net", PublicURL: "https://new.example.ts.net:8443",
		})
		if err != nil {
			return nil, err
		}
		return relayadmin.MarshalSuccessResponse(rotateKey.RequestID, rotateKey.Operation, relayadmin.EndpointRotationResult{
			PublicURL: result.PublicURL, Serial: result.Serial,
		})
	})
	if err != nil {
		t.Fatalf("ready RotateEndpoint() error = %v", err)
	}
}

func TestAdminReplayConcurrentReserveNeverHoldsStateLockWhileWaitingForSQLite(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "Relay")
	state, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	_, csr := newDeviceCSR(t)
	setupKey := adminReplayKey("54545454545454545454545454545454", relayadmin.OperationSetup, "setup")
	executeAdminBootstrap(t, state, setupKey, AdminBootstrapOwnerOptions{
		PublicName: "relay.example.ts.net", PublicURL: "https://relay.example.ts.net:8443",
		CSRPEM: csr, AdministrativeOwnerUID: 501,
	})
	firstKey := adminReplayKey("55555555555555555555555555555550", relayadmin.OperationRotate, "first")
	first, err := state.ReplayStore().Reserve(context.Background(), firstKey)
	if err != nil || first.Decision != relayadmin.ReplayExecute {
		t.Fatalf("first Reserve() = (%#v, %v)", first, err)
	}
	txHeld := make(chan struct{})
	checkReady := make(chan struct{})
	firstDone := make(chan error, 1)
	firstContext, cancelFirst := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelFirst()
	go func() {
		_, executeErr := first.Mutation.Execute(firstContext, func(ctx context.Context, transaction relayadmin.MutationTransaction) ([]byte, error) {
			owned, ok := transaction.(*adminMutationTransaction)
			if !ok {
				return nil, errors.New("unexpected mutation transaction type")
			}
			if err := owned.ensureTransaction(ctx); err != nil {
				return nil, err
			}
			close(txHeld)
			<-checkReady
			if err := state.requireAdminReady(ctx, owned); err != nil {
				return nil, err
			}
			return relayadmin.MarshalErrorResponse(firstKey.RequestID, firstKey.Operation, relayadmin.ErrorOperationFailed)
		})
		firstDone <- executeErr
	}()
	<-txHeld

	secondKey := adminReplayKey("56565656565656565656565656565656", relayadmin.OperationRepair, "second")
	databasePhase := make(chan struct{})
	state.beforeReplayDatabase = func(key relayadmin.ReplayKey) {
		if key == secondKey {
			close(databasePhase)
		}
	}
	waitsBefore := state.database.db.Stats().WaitCount
	secondDone := make(chan struct {
		reservation relayadmin.ReplayReservation
		err         error
	}, 1)
	secondContext, cancelSecond := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelSecond()
	go func() {
		reservation, reserveErr := state.ReplayStore().Reserve(secondContext, secondKey)
		secondDone <- struct {
			reservation relayadmin.ReplayReservation
			err         error
		}{reservation: reservation, err: reserveErr}
	}()
	<-databasePhase
	deadline := time.Now().Add(time.Second)
	for state.database.db.Stats().WaitCount == waitsBefore && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if state.database.db.Stats().WaitCount == waitsBefore {
		t.Fatal("second Reserve() did not block on the held SQLite connection")
	}
	close(checkReady)
	if err := <-firstDone; err != nil {
		t.Fatalf("first Execute() error = %v", err)
	}
	second := <-secondDone
	if second.err != nil || second.reservation.Decision != relayadmin.ReplayExecute {
		t.Fatalf("second Reserve() = (%#v, %v), want progress after first commit", second.reservation, second.err)
	}
	if err := second.reservation.Mutation.Abandon(context.Background()); err != nil {
		t.Fatal(err)
	}
	state.beforeReplayDatabase = nil
}

func TestAdminStateSnapshotClassifiesAbsentAndLegacyMissingUID(t *testing.T) {
	absentDir := filepath.Join(t.TempDir(), "Relay")
	absent, err := OpenAdminState(AdminStateOptions{StateDir: absentDir})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := absent.Snapshot(context.Background())
	if err != nil || snapshot.Class != AdminStateAbsent || snapshot.OwnerUIDBound {
		t.Fatalf("absent Snapshot() = (%#v, %v)", snapshot, err)
	}
	absent.Close()

	legacyDir := filepath.Join(t.TempDir(), "legacy")
	_, csr := newDeviceCSR(t)
	if _, err := BootstrapOwner(context.Background(), BootstrapOwnerOptions{
		StateDir: legacyDir, PublicName: "relay.example.ts.net", PublicURL: "https://relay.example.ts.net:8443", CSRPEM: csr,
	}); err != nil {
		t.Fatal(err)
	}
	legacy, err := OpenAdminState(AdminStateOptions{StateDir: legacyDir})
	if err != nil {
		t.Fatal(err)
	}
	defer legacy.Close()
	snapshot, err = legacy.Snapshot(context.Background())
	if err != nil || snapshot.Class != AdminStateIncompatible || snapshot.OwnerUIDBound {
		t.Fatalf("legacy Snapshot() = (%#v, %v), want incompatible unbound", snapshot, err)
	}
}

func TestAdminStatePathGuardAndForeignTransactionsFailClosed(t *testing.T) {
	guardErr := errors.New("unsafe state path")
	guard := &recordingAdminPathGuard{validateErr: guardErr}
	guardedDir := filepath.Join(t.TempDir(), "Relay")
	guarded, err := OpenAdminState(AdminStateOptions{StateDir: guardedDir, PathGuard: guard})
	if err != nil {
		t.Fatalf("OpenAdminState() error = %v, want degraded state", err)
	}
	defer guarded.Close()
	snapshot, err := guarded.Snapshot(context.Background())
	if err != nil || snapshot.Class != AdminStateIncompatible {
		t.Fatalf("guarded Snapshot() = (%#v, %v), want incompatible", snapshot, err)
	}
	if guard.validateCalls != 1 {
		t.Fatalf("PathGuard.Validate calls = %d, want 1", guard.validateCalls)
	}
	statusKey := adminReplayKey("43434343434343434343434343434343", relayadmin.OperationStatus, "status")
	status, err := guarded.ReplayStore().Reserve(context.Background(), statusKey)
	if err != nil || status.Decision != relayadmin.ReplayExecute {
		t.Fatalf("guarded status Reserve() = (%#v, %v), want execute", status, err)
	}
	setupKey := adminReplayKey("44444444444444444444444444444444", relayadmin.OperationSetup, "setup")
	setup, err := guarded.ReplayStore().Reserve(context.Background(), setupKey)
	if err != nil || setup.Decision != relayadmin.ReplayExecute {
		t.Fatalf("guarded setup Reserve() = (%#v, %v), want determinate execution", setup, err)
	}
	_, csr := newDeviceCSR(t)
	_, err = setup.Mutation.Execute(context.Background(), func(ctx context.Context, transaction relayadmin.MutationTransaction) ([]byte, error) {
		_, bootstrapErr := guarded.BootstrapOwner(ctx, transaction, AdminBootstrapOwnerOptions{
			PublicName: "relay.example.ts.net", PublicURL: "https://relay.example.ts.net:8443",
			CSRPEM: csr, AdministrativeOwnerUID: 501,
		})
		return nil, bootstrapErr
	})
	if !errors.Is(err, ErrAdminStateIncompatible) {
		t.Fatalf("guarded BootstrapOwner() error = %v, want ErrAdminStateIncompatible", err)
	}
	if _, err := os.Stat(guardedDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("guarded setup created state: %v", err)
	}

	state, err := OpenAdminState(AdminStateOptions{StateDir: filepath.Join(t.TempDir(), "Relay")})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	foreign := foreignAdminTransaction{key: adminReplayKey("abababababababababababababababab", relayadmin.OperationSetup, "foreign")}
	if _, err := state.BootstrapOwner(context.Background(), foreign, AdminBootstrapOwnerOptions{}); !errors.Is(err, errAdminForeignTransaction) {
		t.Fatalf("BootstrapOwner(foreign token) error = %v", err)
	}
	if _, err := state.RotateEndpoint(context.Background(), foreign, RotateEndpointOptions{}); !errors.Is(err, errAdminForeignTransaction) {
		t.Fatalf("RotateEndpoint(foreign token) error = %v", err)
	}
	if err := state.Repair(context.Background(), foreign); !errors.Is(err, errAdminForeignTransaction) {
		t.Fatalf("Repair(foreign token) error = %v", err)
	}
}

func TestAdminMutationRollbackUncertaintyMarksStateDegraded(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "Relay")
	state, err := OpenAdminState(AdminStateOptions{StateDir: stateDir})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	_, csr := newDeviceCSR(t)
	executeAdminBootstrap(t, state, adminReplayKey("8c8c8c8c8c8c8c8c8c8c8c8c8c8c8c8c", relayadmin.OperationSetup, "setup"), AdminBootstrapOwnerOptions{
		PublicName: "relay.example.ts.net", PublicURL: "https://relay.example.ts.net:8443", CSRPEM: csr, AdministrativeOwnerUID: 501,
	})

	databaseTransaction, err := state.database.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := databaseTransaction.Rollback(); err != nil {
		t.Fatal(err)
	}
	token := &adminMutationTransaction{state: state, tx: databaseTransaction}
	if err := token.rollbackBeforeCommit(); !errors.Is(err, sql.ErrTxDone) {
		t.Fatalf("rollbackBeforeCommit() error = %v, want sql.ErrTxDone", err)
	}
	snapshot, err := state.Snapshot(context.Background())
	if err != nil || snapshot.Class != AdminStateIncompatible {
		t.Fatalf("Snapshot() = (%#v, %v), want degraded after rollback uncertainty", snapshot, err)
	}
}

type recordingAdminPathGuard struct {
	validateCalls int
	repairCalls   int
	validateErr   error
	repairErr     error
}

func (guard *recordingAdminPathGuard) Validate(context.Context) error {
	guard.validateCalls++
	return guard.validateErr
}

func (guard *recordingAdminPathGuard) Repair(context.Context) error {
	guard.repairCalls++
	return guard.repairErr
}

type foreignAdminTransaction struct{ key relayadmin.ReplayKey }

func (transaction foreignAdminTransaction) ReplayKey() relayadmin.ReplayKey { return transaction.key }

func createAdminStateDatabase(t *testing.T) string {
	t.Helper()
	stateDir := t.TempDir()
	state, err := createStore(filepath.Join(stateDir, databaseFilename))
	if err != nil {
		t.Fatal(err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	return stateDir
}

func adminReplayKey(requestID string, operation relayadmin.Operation, material string) relayadmin.ReplayKey {
	return relayadmin.ReplayKey{RequestID: requestID, Operation: operation, Digest: sha256.Sum256([]byte(material))}
}
