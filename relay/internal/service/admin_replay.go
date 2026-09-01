package service

import (
	"bytes"
	"container/list"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"mobile-egress/internal/relayadmin"
)

type adminReplayStore struct{ state *AdminState }

func (replay *adminReplayStore) Reserve(ctx context.Context, key relayadmin.ReplayKey) (relayadmin.ReplayReservation, error) {
	if replay == nil || replay.state == nil {
		return relayadmin.ReplayReservation{}, relayadmin.ErrReplayState
	}
	if key.Operation == relayadmin.OperationStatus {
		return replay.reserveStatus(ctx, key)
	}
	if !adminMutationOperation(key.Operation) || relayadmin.ValidateRequestID(key.RequestID) != nil {
		return relayadmin.ReplayReservation{}, relayadmin.ErrReplayState
	}
	if err := ctx.Err(); err != nil {
		return relayadmin.ReplayReservation{}, err
	}
	state := replay.state
	state.mu.Lock()
	if state.closed {
		state.mu.Unlock()
		return relayadmin.ReplayReservation{}, relayadmin.ErrReplayState
	}
	state.pruneStatusLocked(time.Now())
	if _, exists := state.status[key.RequestID]; exists {
		state.mu.Unlock()
		return relayadmin.ReplayReservation{Decision: relayadmin.ReplayDuplicate}, nil
	}
	if active := state.active[key.RequestID]; active != nil {
		state.mu.Unlock()
		return relayadmin.ReplayReservation{Decision: relayadmin.ReplayDuplicate}, nil
	}
	if _, exists := state.pending[key.RequestID]; exists {
		state.mu.Unlock()
		return relayadmin.ReplayReservation{Decision: relayadmin.ReplayDuplicate}, nil
	}
	if uncertain, exists := state.uncertain[key.RequestID]; exists {
		if uncertain != key {
			state.mu.Unlock()
			return relayadmin.ReplayReservation{Decision: relayadmin.ReplayDuplicate}, nil
		}
		state.mu.Unlock()
		return relayadmin.ReplayReservation{Decision: relayadmin.ReplayBusy}, nil
	}
	if completed, exists := state.fallback[key.RequestID]; exists {
		if completed.key != key {
			state.mu.Unlock()
			return relayadmin.ReplayReservation{Decision: relayadmin.ReplayDuplicate}, nil
		}
		state.mu.Unlock()
		return relayadmin.ReplayReservation{
			Decision: relayadmin.ReplayCached, Response: append([]byte(nil), completed.response...),
		}, nil
	}
	if key.Operation == relayadmin.OperationRepair && state.database == nil && state.presence == adminStatePresenceDegraded &&
		state.hasActiveOperationLocked(relayadmin.OperationRepair) {
		state.mu.Unlock()
		return relayadmin.ReplayReservation{Decision: relayadmin.ReplayBusy}, nil
	}
	if len(state.uncertain) != 0 {
		// Cleanup uncertainty is fail-closed for setup and rotation, but the
		// narrow repair transaction must remain reachable for Slice 2 recovery.
		// The uncertain request ID itself was handled above, so a repair that
		// reaches this branch still occupies the shared global namespace. Once
		// a database-less repair callback starts, its own indeterminate key
		// blocks further repair attempts until recovery/reopen can classify it.
		if key.Operation != relayadmin.OperationRepair || state.hasUncertainOperationLocked(relayadmin.OperationRepair) {
			state.mu.Unlock()
			return relayadmin.ReplayReservation{Decision: relayadmin.ReplayBusy}, nil
		}
	}
	if !state.replayReady || state.database == nil {
		reservation, err := state.reserveFreshMutationLocked(ctx, key)
		state.mu.Unlock()
		return reservation, err
	}
	database := state.database
	capacity := state.mutationCapacity
	state.pending[key.RequestID] = key
	hook := state.beforeReplayDatabase
	state.mu.Unlock()
	if hook != nil {
		hook(key)
	}

	outcome, err := reserveDurableAdminMutation(ctx, database, capacity, key)
	state.mu.Lock()
	if state.pending[key.RequestID] == key {
		delete(state.pending, key.RequestID)
	}
	if outcome.storedKey != nil {
		state.mutationKeys[key.RequestID] = *outcome.storedKey
	}
	if err != nil {
		if outcome.commitUncertain {
			state.presence = adminStatePresenceDegraded
			state.degraded.Class = AdminStateIncompatible
			state.mutationKeys[key.RequestID] = key
			state.uncertain[key.RequestID] = key
		}
		state.mu.Unlock()
		return relayadmin.ReplayReservation{}, err
	}
	if outcome.decision != relayadmin.ReplayExecute {
		state.mu.Unlock()
		return relayadmin.ReplayReservation{Decision: outcome.decision, Response: outcome.response}, nil
	}
	if state.closed {
		state.mutationKeys[key.RequestID] = key
		state.mu.Unlock()
		return relayadmin.ReplayReservation{}, relayadmin.ErrReplayState
	}
	reservation := &adminMutationReservation{state: state, key: key, database: database}
	state.active[key.RequestID] = reservation
	state.mutationKeys[key.RequestID] = key
	state.mu.Unlock()
	return relayadmin.ReplayReservation{Decision: relayadmin.ReplayExecute, Mutation: reservation}, nil
}

type durableAdminReservationOutcome struct {
	decision        relayadmin.ReplayDecision
	response        []byte
	storedKey       *relayadmin.ReplayKey
	commitUncertain bool
}

func reserveDurableAdminMutation(ctx context.Context, database *store, capacity int, key relayadmin.ReplayKey) (durableAdminReservationOutcome, error) {
	transaction, err := database.db.BeginTx(ctx, nil)
	if err != nil {
		return durableAdminReservationOutcome{}, fmt.Errorf("begin relay admin replay reservation: %w", err)
	}
	defer transaction.Rollback()
	var digest []byte
	var operation, replayState string
	var response []byte
	err = transaction.QueryRowContext(ctx, `SELECT digest, operation, state, response FROM admin_mutation_replay WHERE request_id = ?`, key.RequestID).
		Scan(&digest, &operation, &replayState, &response)
	if err == nil {
		storedKey, keyErr := persistedAdminReplayKey(key.RequestID, digest, operation)
		if keyErr != nil {
			return durableAdminReservationOutcome{}, keyErr
		}
		outcome := durableAdminReservationOutcome{storedKey: &storedKey}
		if storedKey != key {
			outcome.decision = relayadmin.ReplayDuplicate
			return outcome, nil
		}
		if replayState != "completed" {
			outcome.decision = relayadmin.ReplayBusy
			return outcome, nil
		}
		if !validCachedAdminResponse(key, response) {
			return durableAdminReservationOutcome{}, relayadmin.ErrReplayState
		}
		outcome.decision = relayadmin.ReplayCached
		outcome.response = append([]byte(nil), response...)
		return outcome, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return durableAdminReservationOutcome{}, fmt.Errorf("read relay admin replay reservation: %w", err)
	}
	var count int
	if err := transaction.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin_mutation_replay`).Scan(&count); err != nil {
		return durableAdminReservationOutcome{}, fmt.Errorf("count relay admin replay reservations: %w", err)
	}
	if count >= capacity {
		return durableAdminReservationOutcome{decision: relayadmin.ReplayBusy}, nil
	}
	if _, err := transaction.ExecContext(ctx, `INSERT INTO admin_mutation_replay(request_id, digest, operation, state, response, created_at) VALUES (?, ?, ?, 'reserved', NULL, ?)`,
		key.RequestID, key.Digest[:], string(key.Operation), time.Now().UTC().Unix()); err != nil {
		return durableAdminReservationOutcome{}, fmt.Errorf("persist relay admin replay reservation: %w", err)
	}
	storedKey := key
	if err := transaction.Commit(); err != nil {
		return durableAdminReservationOutcome{storedKey: &storedKey, commitUncertain: true}, fmt.Errorf("commit relay admin replay reservation: %w", err)
	}
	return durableAdminReservationOutcome{decision: relayadmin.ReplayExecute, storedKey: &storedKey}, nil
}

func persistedAdminReplayKey(requestID string, digest []byte, operation string) (relayadmin.ReplayKey, error) {
	parsedOperation := relayadmin.Operation(operation)
	if relayadmin.ValidateRequestID(requestID) != nil || len(digest) != 32 || !adminMutationOperation(parsedOperation) {
		return relayadmin.ReplayKey{}, relayadmin.ErrReplayState
	}
	var parsedDigest [32]byte
	copy(parsedDigest[:], digest)
	return relayadmin.ReplayKey{RequestID: requestID, Digest: parsedDigest, Operation: parsedOperation}, nil
}

func (replay *adminReplayStore) CompleteStatus(ctx context.Context, key relayadmin.ReplayKey, response []byte) error {
	now := time.Now()
	if err := replay.state.statusReplay.CompleteStatus(ctx, key, response); err != nil {
		return err
	}
	state := replay.state
	state.mu.Lock()
	defer state.mu.Unlock()
	entry := state.status[key.RequestID]
	if entry == nil || entry.key != key || entry.completed {
		return relayadmin.ErrReplayState
	}
	entry.completed = true
	entry.expiresAt = now.Add(relayadmin.StatusReplayTTL)
	entry.lru = state.statusLRU.PushBack(key.RequestID)
	for state.statusLRU.Len() > relayadmin.StatusReplayCapacity {
		state.removeOldestStatusLocked()
	}
	return nil
}

func (replay *adminReplayStore) AbandonStatus(ctx context.Context, key relayadmin.ReplayKey) error {
	if err := replay.state.statusReplay.AbandonStatus(ctx, key); err != nil {
		return err
	}
	state := replay.state
	state.mu.Lock()
	defer state.mu.Unlock()
	entry := state.status[key.RequestID]
	if entry == nil || entry.key != key || entry.completed {
		return relayadmin.ErrReplayState
	}
	delete(state.status, key.RequestID)
	return nil
}

func (replay *adminReplayStore) reserveStatus(ctx context.Context, key relayadmin.ReplayKey) (relayadmin.ReplayReservation, error) {
	if relayadmin.ValidateRequestID(key.RequestID) != nil {
		return relayadmin.ReplayReservation{}, relayadmin.ErrReplayState
	}
	if err := ctx.Err(); err != nil {
		return relayadmin.ReplayReservation{}, err
	}
	state := replay.state
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.closed {
		return relayadmin.ReplayReservation{}, relayadmin.ErrReplayState
	}
	state.pruneStatusLocked(time.Now())
	if _, exists := state.mutationKeys[key.RequestID]; exists {
		return relayadmin.ReplayReservation{Decision: relayadmin.ReplayDuplicate}, nil
	}
	if _, exists := state.pending[key.RequestID]; exists {
		return relayadmin.ReplayReservation{Decision: relayadmin.ReplayDuplicate}, nil
	}
	if _, exists := state.active[key.RequestID]; exists {
		return relayadmin.ReplayReservation{Decision: relayadmin.ReplayDuplicate}, nil
	}
	reservation, err := state.statusReplay.Reserve(ctx, key)
	if err != nil {
		return relayadmin.ReplayReservation{}, err
	}
	switch reservation.Decision {
	case relayadmin.ReplayExecute:
		state.status[key.RequestID] = &adminStatusEntry{key: key}
	case relayadmin.ReplayCached:
		entry := state.status[key.RequestID]
		if entry == nil || entry.key != key || !entry.completed {
			return relayadmin.ReplayReservation{}, relayadmin.ErrReplayState
		}
		if entry.lru != nil {
			state.statusLRU.MoveToBack(entry.lru)
		}
	}
	return reservation, nil
}

func (state *AdminState) pruneStatusLocked(now time.Time) {
	for element := state.statusLRU.Front(); element != nil; {
		next := element.Next()
		requestID, _ := element.Value.(string)
		entry := state.status[requestID]
		if entry == nil || !now.Before(entry.expiresAt) {
			state.removeStatusElementLocked(element)
		}
		element = next
	}
}

func (state *AdminState) removeOldestStatusLocked() {
	state.removeStatusElementLocked(state.statusLRU.Front())
}

func (state *AdminState) removeStatusElementLocked(element *list.Element) {
	if element == nil {
		return
	}
	requestID, _ := element.Value.(string)
	entry := state.status[requestID]
	if entry != nil && entry.lru == element && entry.completed {
		delete(state.status, requestID)
	}
	state.statusLRU.Remove(element)
}

type adminMutationReservation struct {
	state                      *AdminState
	key                        relayadmin.ReplayKey
	database                   *store
	started                    bool
	abandoning                 bool
	databaseLessDegradedRepair bool
}

func (reservation *adminMutationReservation) Key() relayadmin.ReplayKey { return reservation.key }

func (reservation *adminMutationReservation) Execute(ctx context.Context, execution relayadmin.MutationExecution) ([]byte, error) {
	if reservation == nil || reservation.state == nil || execution == nil {
		return nil, relayadmin.ErrReplayState
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	state := reservation.state
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-state.mutationGate:
	}
	defer func() { state.mutationGate <- struct{}{} }()
	if hook := state.afterMutationGateAcquire; hook != nil {
		hook()
	}
	if err := ctx.Err(); err != nil {
		cleanupContext, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
		cleanupErr := reservation.Abandon(cleanupContext)
		cancel()
		if cleanupErr != nil {
			return nil, relayadmin.ErrMutationIndeterminate
		}
		return nil, err
	}

	state.mu.Lock()
	current := state.active[reservation.key.RequestID]
	if state.closed || current != reservation || reservation.started || reservation.abandoning {
		state.mu.Unlock()
		return nil, relayadmin.ErrReplayState
	}
	databaseLessDegradedRepair := reservation.database == nil &&
		reservation.key.Operation == relayadmin.OperationRepair && state.presence == adminStatePresenceDegraded
	if databaseLessDegradedRepair && state.hasUncertainOperationLocked(relayadmin.OperationRepair) {
		delete(state.active, reservation.key.RequestID)
		state.mu.Unlock()
		return nil, relayadmin.ErrReplayState
	}
	reservation.started = true
	reservation.databaseLessDegradedRepair = databaseLessDegradedRepair
	if reservation.database == nil && !databaseLessDegradedRepair &&
		(state.replayReady || state.presence == adminStatePresenceReady || len(state.fallback) != 0) {
		delete(state.active, reservation.key.RequestID)
		state.mu.Unlock()
		return nil, relayadmin.ErrReplayState
	}
	state.mu.Unlock()
	if reservation.database == nil {
		return reservation.executeFresh(ctx, execution)
	}
	return reservation.executeExisting(ctx, reservation.database, execution)
}

func (reservation *adminMutationReservation) executeExisting(ctx context.Context, database *store, execution relayadmin.MutationExecution) ([]byte, error) {
	key := reservation.key
	result, err := database.db.ExecContext(ctx, `UPDATE admin_mutation_replay SET state = 'executing' WHERE request_id = ? AND digest = ? AND operation = ? AND state = 'reserved'`,
		key.RequestID, key.Digest[:], string(key.Operation))
	if err != nil {
		reservation.finishActive()
		return nil, fmt.Errorf("start relay admin mutation: %w", err)
	}
	if changed, err := result.RowsAffected(); err != nil || changed != 1 {
		reservation.finishActive()
		return nil, relayadmin.ErrReplayState
	}
	token := &adminMutationTransaction{state: reservation.state, reservation: reservation, database: database, key: key}
	response, executionErr := execution(ctx, token)
	return reservation.completeExecutedMutation(ctx, token, response, executionErr)
}

func (reservation *adminMutationReservation) completeExecutedMutation(
	ctx context.Context,
	token *adminMutationTransaction,
	response []byte,
	executionErr error,
) ([]byte, error) {
	key := reservation.key
	if executionErr != nil || ctx.Err() != nil || !token.validResponse(response) {
		rollbackErr := token.rollbackBeforeCommit()
		reservation.markIndeterminate()
		if rollbackErr != nil {
			return nil, relayadmin.ErrMutationIndeterminate
		}
		if executionErr != nil {
			return nil, executionErr
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, relayadmin.ErrMutationIndeterminate
	}
	if err := token.ensureTransaction(ctx); err != nil {
		reservation.markIndeterminate()
		return nil, relayadmin.ErrMutationIndeterminate
	}
	if _, err := token.tx.ExecContext(ctx, `UPDATE admin_mutation_replay SET state = 'completed', response = ? WHERE request_id = ? AND digest = ? AND operation = ? AND state = 'executing'`,
		response, key.RequestID, key.Digest[:], string(key.Operation)); err != nil {
		_ = token.rollbackBeforeCommit()
		reservation.markIndeterminate()
		return nil, relayadmin.ErrMutationIndeterminate
	}
	if token.endpoint != nil && reservation.state.endpointFault != nil {
		if err := reservation.state.endpointFault(adminEndpointBeforeCommit); err != nil {
			_ = token.rollbackBeforeCommit()
			reservation.markIndeterminate()
			return nil, relayadmin.ErrMutationIndeterminate
		}
	}
	if token.endpoint != nil {
		if err := token.endpoint.validatePromoted(); err != nil {
			_ = token.rollbackBeforeCommit()
			reservation.state.markAdminDegraded()
			reservation.markIndeterminate()
			return nil, relayadmin.ErrMutationIndeterminate
		}
	}
	if err := reservation.state.commitAdminMutation(token.tx); err != nil {
		reservation.state.markAdminDegraded()
		reservation.markIndeterminate()
		return nil, relayadmin.ErrMutationIndeterminate
	}
	if token.endpoint != nil && reservation.state.endpointFault != nil {
		if err := reservation.state.endpointFault(adminEndpointAfterCommit); err != nil {
			reservation.state.markAdminDegraded()
			reservation.finishActive()
			reservation.notifyFinished()
			return nil, relayadmin.ErrMutationIndeterminate
		}
	}
	if err := token.finalizeAfterCommit(); err != nil {
		reservation.state.markAdminDegraded()
		reservation.finishActive()
		reservation.notifyFinished()
		return nil, relayadmin.ErrMutationIndeterminate
	}
	reservation.finishActive()
	reservation.notifyFinished()
	return append([]byte(nil), response...), nil
}

func (reservation *adminMutationReservation) Abandon(ctx context.Context) error {
	if reservation == nil || reservation.state == nil {
		return relayadmin.ErrReplayState
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	state := reservation.state
	state.mu.Lock()
	if state.closed || state.active[reservation.key.RequestID] != reservation || reservation.started || reservation.abandoning {
		state.mu.Unlock()
		return relayadmin.ErrReplayState
	}
	if reservation.database == nil {
		delete(state.active, reservation.key.RequestID)
		delete(state.mutationKeys, reservation.key.RequestID)
		state.mu.Unlock()
		return nil
	}
	reservation.abandoning = true
	state.mu.Unlock()
	result, err := reservation.database.db.ExecContext(ctx, `DELETE FROM admin_mutation_replay WHERE request_id = ? AND digest = ? AND operation = ? AND state = 'reserved'`,
		reservation.key.RequestID, reservation.key.Digest[:], string(reservation.key.Operation))
	if err != nil {
		state.mu.Lock()
		reservation.abandoning = false
		state.mu.Unlock()
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		state.mu.Lock()
		reservation.abandoning = false
		state.mu.Unlock()
		return relayadmin.ErrReplayState
	}
	state.mu.Lock()
	reservation.abandoning = false
	if state.active[reservation.key.RequestID] == reservation && !reservation.started {
		delete(state.active, reservation.key.RequestID)
		delete(state.mutationKeys, reservation.key.RequestID)
	}
	state.mu.Unlock()
	return nil
}

type adminMutationTransaction struct {
	state       *AdminState
	reservation *adminMutationReservation
	database    *store
	tx          *sql.Tx
	key         relayadmin.ReplayKey
	setup       *adminSetupStage
	endpoint    *adminEndpointStage
	repair      *adminRepairStage
	adopted     bool
	cached      []byte
}

func (transaction *adminMutationTransaction) ReplayKey() relayadmin.ReplayKey { return transaction.key }

func (transaction *adminMutationTransaction) ensureTransaction(ctx context.Context) error {
	if transaction.tx != nil {
		return nil
	}
	if transaction.database == nil {
		return relayadmin.ErrReplayState
	}
	databaseTransaction, err := transaction.database.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	transaction.tx = databaseTransaction
	return nil
}

func (transaction *adminMutationTransaction) rollbackBeforeCommit() error {
	var rollbackErr error
	if transaction.tx != nil {
		rollbackErr = transaction.tx.Rollback()
		transaction.tx = nil
	}
	if transaction.endpoint != nil {
		if err := transaction.endpoint.rollback(); err != nil {
			rollbackErr = errors.Join(rollbackErr, err)
		}
	}
	if rollbackErr != nil {
		transaction.state.markAdminDegraded()
	}
	return rollbackErr
}

func (transaction *adminMutationTransaction) finalizeAfterCommit() error {
	transaction.tx = nil
	if transaction.endpoint != nil {
		if err := transaction.endpoint.finalize(); err != nil {
			return err
		}
	}
	if transaction.repair != nil {
		return transaction.repair.finalize()
	}
	return nil
}

func (reservation *adminMutationReservation) finishActive() {
	state := reservation.state
	state.mu.Lock()
	if state.active[reservation.key.RequestID] == reservation {
		delete(state.active, reservation.key.RequestID)
	}
	state.mu.Unlock()
}

func (reservation *adminMutationReservation) markIndeterminate() {
	database := reservation.database
	if database != nil {
		cleanupContext, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
		_, _ = database.db.ExecContext(cleanupContext, `UPDATE admin_mutation_replay SET state = 'indeterminate', response = NULL WHERE request_id = ? AND state <> 'completed'`, reservation.key.RequestID)
		cancel()
	}
	reservation.finishActive()
	reservation.notifyFinished()
}

func (reservation *adminMutationReservation) notifyFinished() {
	if callback := reservation.state.mutationFinished; callback != nil {
		callback(reservation.key)
	}
}

func (state *AdminState) reserveFreshMutationLocked(ctx context.Context, key relayadmin.ReplayKey) (relayadmin.ReplayReservation, error) {
	if err := ctx.Err(); err != nil {
		return relayadmin.ReplayReservation{}, err
	}
	used := len(state.mutationKeys)
	for requestID := range state.active {
		if _, persisted := state.mutationKeys[requestID]; !persisted {
			used++
		}
	}
	if used >= state.mutationCapacity {
		return relayadmin.ReplayReservation{Decision: relayadmin.ReplayBusy}, nil
	}
	// With no usable authoritative journal, only deterministic validation may
	// run. If no setup stage becomes authoritative, no mutation survived and a
	// later same-ID request may safely revalidate. State methods reject degraded
	// namespaces before creating artifacts.
	return relayadmin.ReplayReservation{Decision: relayadmin.ReplayExecute, Mutation: state.newFreshReservationLocked(key)}, nil
}

func (state *AdminState) newFreshReservationLocked(key relayadmin.ReplayKey) *adminMutationReservation {
	reservation := &adminMutationReservation{state: state, key: key}
	state.active[key.RequestID] = reservation
	return reservation
}

func (reservation *adminMutationReservation) executeFresh(ctx context.Context, execution relayadmin.MutationExecution) ([]byte, error) {
	transaction := &adminMutationTransaction{state: reservation.state, reservation: reservation, key: reservation.key}
	response, err := execution(ctx, transaction)
	if transaction.adopted {
		if len(transaction.cached) != 0 {
			if err != nil || ctx.Err() != nil || !bytes.Equal(response, transaction.cached) ||
				!validAdminRepairSuccessResponse(transaction.key, transaction.cached) {
				_ = transaction.rollbackBeforeCommit()
				reservation.finishActive()
				reservation.notifyFinished()
				if err != nil {
					return nil, err
				}
				if ctxErr := ctx.Err(); ctxErr != nil {
					return nil, ctxErr
				}
				return nil, relayadmin.ErrMutationIndeterminate
			}
			if transaction.tx != nil {
				if rollbackErr := transaction.tx.Rollback(); rollbackErr != nil {
					reservation.state.markAdminDegraded()
					reservation.finishActive()
					reservation.notifyFinished()
					return nil, relayadmin.ErrMutationIndeterminate
				}
				transaction.tx = nil
			}
			if finalizeErr := transaction.finalizeAfterCommit(); finalizeErr != nil {
				reservation.state.markAdminDegraded()
				reservation.finishActive()
				reservation.notifyFinished()
				return nil, relayadmin.ErrMutationIndeterminate
			}
			reservation.finishActive()
			reservation.notifyFinished()
			return append([]byte(nil), transaction.cached...), nil
		}
		return reservation.completeExecutedMutation(ctx, transaction, response, err)
	}
	if transaction.setup == nil {
		if reservation.databaseLessDegradedRepair {
			reservation.markFreshRepairIndeterminate()
			if err != nil {
				return nil, err
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			// A database-less repair cannot claim success until Slice 2 has made
			// its completion durable and replayable. Retain it fail-closed.
			return nil, relayadmin.ErrMutationIndeterminate
		}
		reservation.finishActive()
		reservation.notifyFinished()
		if err != nil {
			return nil, err
		}
		if !validCachedAdminResponse(reservation.key, response) {
			return nil, relayadmin.ErrMutationIndeterminate
		}
		return append([]byte(nil), response...), nil
	}
	return reservation.completeFreshSetup(ctx, transaction, response, err)
}

func (reservation *adminMutationReservation) markFreshRepairIndeterminate() {
	state := reservation.state
	state.mu.Lock()
	if state.active[reservation.key.RequestID] == reservation {
		delete(state.active, reservation.key.RequestID)
	}
	state.presence = adminStatePresenceDegraded
	state.mutationKeys[reservation.key.RequestID] = reservation.key
	state.uncertain[reservation.key.RequestID] = reservation.key
	state.mu.Unlock()
	reservation.notifyFinished()
}

func (state *AdminState) hasUncertainOperationLocked(operation relayadmin.Operation) bool {
	for _, key := range state.uncertain {
		if key.Operation == operation {
			return true
		}
	}
	return false
}

func (state *AdminState) hasActiveOperationLocked(operation relayadmin.Operation) bool {
	for _, reservation := range state.active {
		if reservation != nil && reservation.key.Operation == operation {
			return true
		}
	}
	return false
}

func (reservation *adminMutationReservation) completeFreshSetup(
	ctx context.Context,
	transaction *adminMutationTransaction,
	response []byte,
	executionErr error,
) ([]byte, error) {
	stage := transaction.setup
	cleanupBeforeAuthority := func() error {
		if stage.tx != nil {
			_ = stage.tx.Rollback()
		}
		if stage.database != nil {
			_ = stage.database.Close()
		}
		return reservation.state.removeSetupStageDirectory(stage.dir)
	}
	failBeforeAuthority := func(operationErr error) ([]byte, error) {
		cleanupErr := cleanupBeforeAuthority()
		if cleanupErr != nil {
			reservation.state.retainAdminUncertain(reservation.key)
		}
		reservation.finishActive()
		reservation.notifyFinished()
		if cleanupErr != nil {
			return nil, relayadmin.ErrMutationIndeterminate
		}
		return nil, operationErr
	}
	if executionErr != nil {
		return failBeforeAuthority(executionErr)
	}
	if err := ctx.Err(); err != nil {
		return failBeforeAuthority(err)
	}
	if !validCachedAdminResponse(reservation.key, response) {
		return failBeforeAuthority(relayadmin.ErrMutationIndeterminate)
	}
	if _, err := stage.tx.ExecContext(ctx, `INSERT INTO admin_mutation_replay(request_id, digest, operation, state, response, created_at) VALUES (?, ?, ?, 'completed', ?, ?)`,
		reservation.key.RequestID, reservation.key.Digest[:], string(reservation.key.Operation), response, time.Now().UTC().Unix()); err != nil {
		return failBeforeAuthority(fmt.Errorf("persist completed setup replay: %w", err))
	}
	if hook := reservation.state.beforeSetupCommit; hook != nil {
		if err := hook(); err != nil {
			return failBeforeAuthority(err)
		}
	}
	if err := stage.tx.Commit(); err != nil {
		return failBeforeAuthority(fmt.Errorf("commit relay admin setup transaction: %w", err))
	}
	stage.tx = nil
	if _, err := stage.database.db.ExecContext(context.Background(), `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return failBeforeAuthority(fmt.Errorf("checkpoint relay admin setup database: %w", err))
	}
	if err := stage.database.Close(); err != nil {
		stage.database = nil
		return failBeforeAuthority(fmt.Errorf("close relay admin setup database: %w", err))
	}
	stage.database = nil
	if err := validateCompletedAdminSetupStage(stage.dir, reservation.key, response); err != nil {
		return failBeforeAuthority(err)
	}
	if err := syncAdminSetupStage(stage.dir); err != nil {
		return failBeforeAuthority(err)
	}
	if hook := reservation.state.beforeSetupRename; hook != nil {
		if err := hook(); err != nil {
			return failBeforeAuthority(err)
		}
	}
	if err := os.Rename(stage.dir, reservation.state.stateDir); err != nil {
		return failBeforeAuthority(fmt.Errorf("commit relay admin setup state: %w", err))
	}
	// The namespace is authoritative from this point onward. Never remove or
	// rewrite it even if parent fsync, reopen, reconciliation, or fault hooks
	// fail; a later process can recover the completed response from state.db.
	reservation.state.mu.Lock()
	reservation.state.presence = adminStatePresenceDegraded
	reservation.state.degraded = AdminSnapshot{
		Class: AdminStateIncompatible, AdministrativeOwnerUID: stage.ownerUID, OwnerUIDBound: stage.ownerUID != 0,
	}
	reservation.state.replayReady = false
	reservation.state.mutationKeys[reservation.key.RequestID] = reservation.key
	reservation.state.fallback[reservation.key.RequestID] = adminCompletedReplay{
		key: reservation.key, response: append([]byte(nil), response...),
	}
	reservation.state.mu.Unlock()
	authorityErr := reservation.state.syncSetupParent(filepath.Dir(reservation.state.stateDir))
	var database *store
	var openErr error
	if hook := reservation.state.beforeSetupReopen; hook != nil {
		openErr = hook()
	} else {
		database, openErr = openStore(filepath.Join(reservation.state.stateDir, databaseFilename))
	}
	if openErr == nil {
		if schemaErr := database.validSchema(context.Background()); schemaErr != nil {
			database.Close()
			openErr = schemaErr
		} else {
			mutationKeys, keysErr := loadAdminMutationKeys(context.Background(), database)
			snapshot, snapshotErr := adminSnapshotFromQuery(context.Background(), database.db, reservation.state.stateDir)
			if keysErr != nil || snapshotErr != nil || snapshot.Class != AdminStateReady {
				database.Close()
				openErr = ErrAdminStateIncompatible
			} else if authorityErr != nil {
				_ = database.Close()
			} else {
				reservation.state.mu.Lock()
				if !reservation.state.closed {
					reservation.state.database = database
					reservation.state.replayReady = true
					reservation.state.presence = adminStatePresenceReady
					snapshot.Class = AdminStateIncompatible
					reservation.state.degraded = snapshot
					reservation.state.mutationKeys = mutationKeys
					delete(reservation.state.fallback, reservation.key.RequestID)
				} else {
					database.Close()
					openErr = relayadmin.ErrReplayState
				}
				reservation.state.mu.Unlock()
			}
		}
	}
	reservation.finishActive()
	reservation.notifyFinished()
	if hook := reservation.state.afterSetupRename; hook != nil {
		if err := hook(); err != nil {
			return nil, err
		}
	}
	if authorityErr != nil || openErr != nil {
		return nil, relayadmin.ErrMutationIndeterminate
	}
	return append([]byte(nil), response...), nil
}

func adminMutationOperation(operation relayadmin.Operation) bool {
	return operation == relayadmin.OperationSetup || operation == relayadmin.OperationRotate || operation == relayadmin.OperationRepair
}

func validCachedAdminResponse(key relayadmin.ReplayKey, response []byte) bool {
	if len(response) == 0 || len(response) > relayadmin.MaximumFrameSize {
		return false
	}
	parsed, err := relayadmin.ParseResponse(response)
	return err == nil && parsed.Version == relayadmin.Version && parsed.RequestID == key.RequestID && parsed.Operation == key.Operation
}

func (transaction *adminMutationTransaction) validResponse(response []byte) bool {
	if transaction == nil || !validCachedAdminResponse(transaction.key, response) {
		return false
	}
	parsed, err := relayadmin.ParseResponse(response)
	if err != nil {
		return false
	}
	if transaction.endpoint != nil {
		result, ok := parsed.Result.(relayadmin.EndpointRotationResult)
		return parsed.OK && ok && result.PublicURL == transaction.endpoint.newURL && result.Serial == transaction.endpoint.serial
	}
	if transaction.repair != nil {
		return validAdminRepairSuccessResponse(transaction.key, response)
	}
	return true
}

func validAdminRepairSuccessResponse(key relayadmin.ReplayKey, response []byte) bool {
	if key.Operation != relayadmin.OperationRepair || !validCachedAdminResponse(key, response) {
		return false
	}
	parsed, err := relayadmin.ParseResponse(response)
	if err != nil {
		return false
	}
	result, ok := parsed.Result.(relayadmin.RepairResult)
	return parsed.OK && ok && result.Ready && result.Restarting
}

func equalDigest(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	var different byte
	for index := range left {
		different |= left[index] ^ right[index]
	}
	return different == 0
}

func loadAdminMutationKeys(ctx context.Context, database *store) (map[string]relayadmin.ReplayKey, error) {
	keys := make(map[string]relayadmin.ReplayKey)
	rows, err := database.db.QueryContext(ctx, `SELECT request_id, digest, operation FROM admin_mutation_replay`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var requestID, operation string
		var digestBytes []byte
		if err := rows.Scan(&requestID, &digestBytes, &operation); err != nil {
			return nil, err
		}
		if relayadmin.ValidateRequestID(requestID) != nil || len(digestBytes) != 32 {
			return nil, relayadmin.ErrReplayState
		}
		parsedOperation := relayadmin.Operation(operation)
		if !adminMutationOperation(parsedOperation) {
			return nil, relayadmin.ErrReplayState
		}
		var digest [32]byte
		copy(digest[:], digestBytes)
		keys[requestID] = relayadmin.ReplayKey{RequestID: requestID, Digest: digest, Operation: parsedOperation}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return keys, nil
}
