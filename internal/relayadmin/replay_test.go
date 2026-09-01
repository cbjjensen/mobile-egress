package relayadmin

import (
	"context"
	"crypto/sha256"
	"fmt"
	"testing"
	"time"
)

func TestReplayTracksInFlightCompletedAndDifferentDigests(t *testing.T) {
	t.Parallel()

	store := NewMemoryReplayStore(MemoryReplayConfig{})
	key := replayTestKey("request-a", "same", OperationSetup)

	reservation, err := store.Reserve(context.Background(), key)
	if err != nil || reservation.Decision != ReplayExecute {
		t.Fatalf("Reserve(first) = (%#v, %v), want execute", reservation, err)
	}
	firstReservation := reservation
	duplicateReservation, err := store.Reserve(context.Background(), key)
	if err != nil || duplicateReservation.Decision != ReplayDuplicate {
		t.Fatalf("Reserve(in flight) = (%#v, %v), want duplicate", duplicateReservation, err)
	}

	cachedResponse := []byte(`{"redacted":true}`)
	if _, err := firstReservation.Mutation.Execute(context.Background(), func(_ context.Context, transaction MutationTransaction) ([]byte, error) {
		if transaction.ReplayKey() != key {
			t.Fatalf("mutation transaction key = %#v, want %#v", transaction.ReplayKey(), key)
		}
		return cachedResponse, nil
	}); err != nil {
		t.Fatalf("Mutation.Execute() returned an error: %v", err)
	}
	reservation, err = store.Reserve(context.Background(), key)
	if err != nil || reservation.Decision != ReplayCached || string(reservation.Response) != string(cachedResponse) {
		t.Fatalf("Reserve(completed) = (%#v, %v), want cached response", reservation, err)
	}
	reservation.Response[0] = 'X'
	again, err := store.Reserve(context.Background(), key)
	if err != nil || string(again.Response) != string(cachedResponse) {
		t.Fatal("cached response was not returned as an immutable copy")
	}

	different := replayTestKey("request-a", "different", OperationSetup)
	reservation, err = store.Reserve(context.Background(), different)
	if err != nil || reservation.Decision != ReplayDuplicate {
		t.Fatalf("Reserve(different digest) = (%#v, %v), want duplicate", reservation, err)
	}
}

func TestStatusReplayUsesTTLAndLRUCapacity(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)
	store := NewMemoryReplayStore(MemoryReplayConfig{
		Now:              func() time.Time { return now },
		StatusCapacity:   2,
		StatusTTL:        time.Minute,
		MutationCapacity: 2,
		InFlightCapacity: 2,
	})
	a := replayTestKey("a", "a", OperationStatus)
	b := replayTestKey("b", "b", OperationStatus)
	c := replayTestKey("c", "c", OperationStatus)

	reserveAndComplete(t, store, a, []byte("a"))
	reserveAndComplete(t, store, b, []byte("b"))
	if got, _ := store.Reserve(context.Background(), a); got.Decision != ReplayCached {
		t.Fatalf("Reserve(a touch) = %#v, want cached", got)
	}
	reserveAndComplete(t, store, c, []byte("c"))

	if got, _ := store.Reserve(context.Background(), b); got.Decision != ReplayExecute {
		t.Fatalf("Reserve(evicted LRU b) = %#v, want execute", got)
	}
	if err := store.AbandonStatus(context.Background(), b); err != nil {
		t.Fatalf("AbandonStatus(b) returned an error: %v", err)
	}
	if got, _ := store.Reserve(context.Background(), a); got.Decision != ReplayCached {
		t.Fatalf("Reserve(recent a) = %#v, want cached", got)
	}

	now = now.Add(time.Minute + time.Nanosecond)
	if got, _ := store.Reserve(context.Background(), a); got.Decision != ReplayExecute {
		t.Fatalf("Reserve(expired a) = %#v, want execute", got)
	}
}

func TestMutationReplayNeverEvictsAndReturnsBusyBeforeExecutionAtCapacity(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)
	store := NewMemoryReplayStore(MemoryReplayConfig{
		Now:              func() time.Time { return now },
		StatusCapacity:   1,
		StatusTTL:        time.Nanosecond,
		MutationCapacity: 2,
		InFlightCapacity: 4,
	})
	a := replayTestKey("a", "a", OperationSetup)
	b := replayTestKey("b", "b", OperationRotate)
	c := replayTestKey("c", "c", OperationRepair)
	reserveAndComplete(t, store, a, []byte("a"))
	reserveAndComplete(t, store, b, []byte("b"))

	now = now.Add(24 * time.Hour)
	if got, _ := store.Reserve(context.Background(), c); got.Decision != ReplayBusy {
		t.Fatalf("Reserve(third mutation) = %#v, want busy", got)
	}
	if got, _ := store.Reserve(context.Background(), a); got.Decision != ReplayCached {
		t.Fatalf("Reserve(old mutation) = %#v, want cached without eviction", got)
	}
}

func TestMutationCapacityCountsReservedAndAbandonFreesTheSlot(t *testing.T) {
	t.Parallel()

	store := NewMemoryReplayStore(MemoryReplayConfig{
		StatusCapacity:   1,
		StatusTTL:        time.Minute,
		MutationCapacity: 1,
		InFlightCapacity: 2,
	})
	first := replayTestKey("first", "first", OperationSetup)
	second := replayTestKey("second", "second", OperationRotate)
	firstReservation, _ := store.Reserve(context.Background(), first)
	if firstReservation.Decision != ReplayExecute || firstReservation.Mutation == nil {
		t.Fatalf("Reserve(first) = %#v, want mutation execute", firstReservation)
	}
	if got, _ := store.Reserve(context.Background(), second); got.Decision != ReplayBusy {
		t.Fatalf("Reserve(second) = %#v, want busy before execution", got)
	}
	if err := firstReservation.Mutation.Abandon(context.Background()); err != nil {
		t.Fatalf("Abandon(first) returned an error: %v", err)
	}
	if got, _ := store.Reserve(context.Background(), second); got.Decision != ReplayExecute {
		t.Fatalf("Reserve(second after release) = %#v, want execute", got)
	}
}

func TestReplayDefaultBoundsAreTheExactProtocolLimits(t *testing.T) {
	t.Parallel()

	if StatusReplayCapacity != 4096 || StatusReplayTTL != 10*time.Minute || MutationReplayCapacity != 65536 {
		t.Fatalf("replay defaults = (%d, %s, %d)", StatusReplayCapacity, StatusReplayTTL, MutationReplayCapacity)
	}

	store := NewMemoryReplayStore(MemoryReplayConfig{})
	for index := 0; index < MutationReplayCapacity; index++ {
		key := replayTestKey(fmt.Sprintf("id-%d", index), fmt.Sprintf("digest-%d", index), OperationRepair)
		reserveAndComplete(t, store, key, []byte("response"))
	}
	overflow := replayTestKey("overflow", "overflow", OperationRepair)
	if got, err := store.Reserve(context.Background(), overflow); err != nil || got.Decision != ReplayBusy {
		t.Fatalf("Reserve(exact mutation overflow) = (%#v, %v), want busy", got, err)
	}
}

func reserveAndComplete(t *testing.T, store ReplayStore, key ReplayKey, response []byte) {
	t.Helper()
	reservation, err := store.Reserve(context.Background(), key)
	if err != nil {
		t.Fatalf("Reserve(%q) returned an error: %v", key.RequestID, err)
	}
	if reservation.Decision != ReplayExecute {
		t.Fatalf("Reserve(%q) = %#v, want execute", key.RequestID, reservation)
	}
	if key.Operation == OperationStatus {
		if err := store.CompleteStatus(context.Background(), key, response); err != nil {
			t.Fatalf("CompleteStatus(%q) returned an error: %v", key.RequestID, err)
		}
		return
	}
	if reservation.Mutation == nil {
		t.Fatalf("Reserve(%q) returned no mutation reservation", key.RequestID)
	}
	if _, err := reservation.Mutation.Execute(context.Background(), func(context.Context, MutationTransaction) ([]byte, error) {
		return response, nil
	}); err != nil {
		t.Fatalf("Mutation.Execute(%q) returned an error: %v", key.RequestID, err)
	}
}

func replayTestKey(id, digest string, operation Operation) ReplayKey {
	return ReplayKey{RequestID: id, Digest: sha256.Sum256([]byte(digest)), Operation: operation}
}
