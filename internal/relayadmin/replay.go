package relayadmin

import (
	"container/list"
	"context"
	"errors"
	"sync"
	"time"
)

const (
	StatusReplayCapacity   = 4096
	StatusReplayTTL        = 10 * time.Minute
	MutationReplayCapacity = 65536
	InFlightReplayCapacity = 4096
)

var ErrReplayState = errors.New("invalid relay admin replay state")

type ReplayKey struct {
	RequestID string
	Digest    [32]byte
	Operation Operation
}

type ReplayDecision uint8

const (
	ReplayExecute ReplayDecision = iota
	ReplayCached
	ReplayDuplicate
	ReplayBusy
)

type ReplayReservation struct {
	Decision ReplayDecision
	Response []byte
	Mutation MutationReservation
}

// MutationTransaction is an opaque transaction token supplied by a replay
// store while it finalizes a mutation. A durable implementation may expose
// additional methods on its concrete token so a handler in the same owning
// package can coordinate SQL state changes with replay completion.
type MutationTransaction interface {
	ReplayKey() ReplayKey
}

// MutationExecution runs only after the store has durably committed the
// reservation. Its returned, already-redacted response is committed by the
// store before Execute succeeds.
type MutationExecution func(context.Context, MutationTransaction) ([]byte, error)

// MutationReservation is a fail-closed transaction lifecycle. Reserve must
// durably persist it before returning ReplayExecute. Execute must retain the
// reservation as indeterminate on callback, cancellation, or completion
// failure. Abandon may remove only a reservation whose execution has not
// started; uncertainty must retain it.
//
// A Task 3B SQLite store can begin a second transaction in Execute, pass a
// concrete MutationTransaction that also exposes its state mutation methods,
// and commit the response in that same transaction. Filesystem mutations are
// not SQL-atomic: their durable pre-reservation remains indeterminate on any
// uncertain outcome, preventing automatic reexecution and allowing repair.
type MutationReservation interface {
	Key() ReplayKey
	Execute(context.Context, MutationExecution) ([]byte, error)
	Abandon(context.Context) error
}

// ReplayStore separates nonmutating status cleanup from durable mutation
// reservations so a server cannot accidentally release a mutation after its
// handler starts.
type ReplayStore interface {
	Reserve(context.Context, ReplayKey) (ReplayReservation, error)
	CompleteStatus(context.Context, ReplayKey, []byte) error
	AbandonStatus(context.Context, ReplayKey) error
}

type MemoryReplayConfig struct {
	Now              func() time.Time
	StatusCapacity   int
	StatusTTL        time.Duration
	MutationCapacity int
	InFlightCapacity int
}

type memoryReplayState uint8

const (
	memoryStatusInFlight memoryReplayState = iota + 1
	memoryStatusCompleted
	memoryMutationReserved
	memoryMutationExecuting
	memoryMutationCompleted
	memoryMutationIndeterminate
)

type memoryReplayEntry struct {
	key       ReplayKey
	state     memoryReplayState
	token     uint64
	response  []byte
	expiresAt time.Time
	lru       *list.Element
}

// MemoryReplayStore is a concurrency-safe bounded replay implementation.
type MemoryReplayStore struct {
	mu sync.Mutex

	now              func() time.Time
	statusCapacity   int
	statusTTL        time.Duration
	mutationCapacity int
	inFlightCapacity int

	entries       map[string]*memoryReplayEntry
	statusLRU     list.List
	inFlight      int
	mutationSlots int
	nextToken     uint64
}

func NewMemoryReplayStore(config MemoryReplayConfig) *MemoryReplayStore {
	now := config.Now
	if now == nil {
		now = time.Now
	}
	statusCapacity := boundedCapacity(config.StatusCapacity, StatusReplayCapacity)
	mutationCapacity := boundedCapacity(config.MutationCapacity, MutationReplayCapacity)
	inFlightCapacity := boundedCapacity(config.InFlightCapacity, InFlightReplayCapacity)
	statusTTL := config.StatusTTL
	if statusTTL <= 0 || statusTTL > StatusReplayTTL {
		statusTTL = StatusReplayTTL
	}
	return &MemoryReplayStore{
		now:              now,
		statusCapacity:   statusCapacity,
		statusTTL:        statusTTL,
		mutationCapacity: mutationCapacity,
		inFlightCapacity: inFlightCapacity,
		entries:          make(map[string]*memoryReplayEntry),
	}
}

func boundedCapacity(configured, maximum int) int {
	if configured <= 0 || configured > maximum {
		return maximum
	}
	return configured
}

func (store *MemoryReplayStore) Reserve(ctx context.Context, key ReplayKey) (ReplayReservation, error) {
	if err := ctx.Err(); err != nil {
		return ReplayReservation{}, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.pruneExpiredLocked(store.now())
	if err := ctx.Err(); err != nil {
		return ReplayReservation{}, err
	}

	if entry, ok := store.entries[key.RequestID]; ok {
		if entry.key.Digest != key.Digest || entry.key.Operation != key.Operation {
			return ReplayReservation{Decision: ReplayDuplicate}, nil
		}
		switch entry.state {
		case memoryStatusCompleted, memoryMutationCompleted:
			if entry.lru != nil {
				store.statusLRU.MoveToBack(entry.lru)
			}
			return ReplayReservation{Decision: ReplayCached, Response: append([]byte(nil), entry.response...)}, nil
		case memoryMutationIndeterminate:
			return ReplayReservation{Decision: ReplayBusy}, nil
		default:
			return ReplayReservation{Decision: ReplayDuplicate}, nil
		}
	}
	if store.inFlight >= store.inFlightCapacity {
		return ReplayReservation{Decision: ReplayBusy}, nil
	}
	if key.Operation.mutation() && store.mutationSlots >= store.mutationCapacity {
		return ReplayReservation{Decision: ReplayBusy}, nil
	}

	store.nextToken++
	entry := &memoryReplayEntry{key: key, token: store.nextToken}
	store.entries[key.RequestID] = entry
	store.inFlight++
	if key.Operation.mutation() {
		entry.state = memoryMutationReserved
		store.mutationSlots++
		return ReplayReservation{
			Decision: ReplayExecute,
			Mutation: &memoryMutationReservation{store: store, key: key, token: entry.token},
		}, nil
	}
	entry.state = memoryStatusInFlight
	return ReplayReservation{Decision: ReplayExecute}, nil
}

func (store *MemoryReplayStore) CompleteStatus(ctx context.Context, key ReplayKey, response []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	entry, ok := store.entries[key.RequestID]
	if !ok || key.Operation != OperationStatus || entry.state != memoryStatusInFlight || entry.key != key || len(response) == 0 {
		return ErrReplayState
	}
	entry.state = memoryStatusCompleted
	entry.response = append([]byte(nil), response...)
	entry.expiresAt = store.now().Add(store.statusTTL)
	entry.lru = store.statusLRU.PushBack(key.RequestID)
	store.inFlight--
	for store.statusLRU.Len() > store.statusCapacity {
		store.removeStatusLocked(store.statusLRU.Front())
	}
	return nil
}

func (store *MemoryReplayStore) AbandonStatus(ctx context.Context, key ReplayKey) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	entry, ok := store.entries[key.RequestID]
	if !ok || key.Operation != OperationStatus || entry.state != memoryStatusInFlight || entry.key != key {
		return ErrReplayState
	}
	delete(store.entries, key.RequestID)
	store.inFlight--
	return nil
}

type memoryMutationReservation struct {
	store *MemoryReplayStore
	key   ReplayKey
	token uint64
}

func (reservation *memoryMutationReservation) Key() ReplayKey { return reservation.key }

func (reservation *memoryMutationReservation) Execute(ctx context.Context, execution MutationExecution) ([]byte, error) {
	if execution == nil {
		return nil, ErrReplayState
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	store := reservation.store
	store.mu.Lock()
	entry, ok := store.entries[reservation.key.RequestID]
	if !ok || entry.key != reservation.key || entry.token != reservation.token || entry.state != memoryMutationReserved {
		store.mu.Unlock()
		return nil, ErrReplayState
	}
	if err := ctx.Err(); err != nil {
		store.mu.Unlock()
		return nil, err
	}
	entry.state = memoryMutationExecuting
	store.mu.Unlock()

	response, executionErr := execution(ctx, memoryMutationTransaction{key: reservation.key})
	store.mu.Lock()
	defer store.mu.Unlock()
	entry, ok = store.entries[reservation.key.RequestID]
	if !ok || entry.key != reservation.key || entry.token != reservation.token || entry.state != memoryMutationExecuting {
		return nil, ErrReplayState
	}
	if executionErr != nil || ctx.Err() != nil || len(response) == 0 {
		entry.state = memoryMutationIndeterminate
		store.inFlight--
		if executionErr != nil {
			return nil, executionErr
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, ErrReplayState
	}
	entry.state = memoryMutationCompleted
	entry.response = append([]byte(nil), response...)
	store.inFlight--
	return append([]byte(nil), response...), nil
}

func (reservation *memoryMutationReservation) Abandon(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	store := reservation.store
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	entry, ok := store.entries[reservation.key.RequestID]
	if !ok || entry.key != reservation.key || entry.token != reservation.token || entry.state != memoryMutationReserved {
		return ErrReplayState
	}
	delete(store.entries, reservation.key.RequestID)
	store.inFlight--
	store.mutationSlots--
	return nil
}

type memoryMutationTransaction struct{ key ReplayKey }

func (transaction memoryMutationTransaction) ReplayKey() ReplayKey { return transaction.key }

func (store *MemoryReplayStore) pruneExpiredLocked(now time.Time) {
	for element := store.statusLRU.Front(); element != nil; {
		next := element.Next()
		requestID, _ := element.Value.(string)
		entry := store.entries[requestID]
		if entry == nil || !now.Before(entry.expiresAt) {
			store.removeStatusLocked(element)
		}
		element = next
	}
}

func (store *MemoryReplayStore) removeStatusLocked(element *list.Element) {
	if element == nil {
		return
	}
	requestID, _ := element.Value.(string)
	entry := store.entries[requestID]
	if entry != nil && entry.lru == element && entry.state == memoryStatusCompleted && entry.key.Operation == OperationStatus {
		delete(store.entries, requestID)
	}
	store.statusLRU.Remove(element)
}
