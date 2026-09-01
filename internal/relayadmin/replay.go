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
}

// ReplayStore is the narrow reservation/completion contract implemented by
// the in-memory store here and by Task 3B's durable SQLite journal.
type ReplayStore interface {
	Reserve(context.Context, ReplayKey) (ReplayReservation, error)
	Complete(context.Context, ReplayKey, []byte) error
	Release(context.Context, ReplayKey)
}

type MemoryReplayConfig struct {
	Now              func() time.Time
	StatusCapacity   int
	StatusTTL        time.Duration
	MutationCapacity int
	InFlightCapacity int
}

type memoryReplayEntry struct {
	key       ReplayKey
	inFlight  bool
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
		if entry.inFlight {
			return ReplayReservation{Decision: ReplayDuplicate}, nil
		}
		if entry.lru != nil {
			store.statusLRU.MoveToBack(entry.lru)
		}
		return ReplayReservation{Decision: ReplayCached, Response: append([]byte(nil), entry.response...)}, nil
	}
	if store.inFlight >= store.inFlightCapacity {
		return ReplayReservation{Decision: ReplayBusy}, nil
	}
	if key.Operation.mutation() && store.mutationSlots >= store.mutationCapacity {
		return ReplayReservation{Decision: ReplayBusy}, nil
	}
	entry := &memoryReplayEntry{key: key, inFlight: true}
	store.entries[key.RequestID] = entry
	store.inFlight++
	if key.Operation.mutation() {
		store.mutationSlots++
	}
	return ReplayReservation{Decision: ReplayExecute}, nil
}

func (store *MemoryReplayStore) Complete(ctx context.Context, key ReplayKey, response []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	entry, ok := store.entries[key.RequestID]
	if !ok || !entry.inFlight || entry.key.Digest != key.Digest || entry.key.Operation != key.Operation {
		return ErrReplayState
	}
	entry.inFlight = false
	entry.response = append([]byte(nil), response...)
	store.inFlight--
	if key.Operation == OperationStatus {
		entry.expiresAt = store.now().Add(store.statusTTL)
		entry.lru = store.statusLRU.PushBack(key.RequestID)
		for store.statusLRU.Len() > store.statusCapacity {
			store.removeStatusLocked(store.statusLRU.Front())
		}
	}
	return nil
}

func (store *MemoryReplayStore) Release(_ context.Context, key ReplayKey) {
	store.mu.Lock()
	defer store.mu.Unlock()
	entry, ok := store.entries[key.RequestID]
	if !ok || !entry.inFlight || entry.key.Digest != key.Digest || entry.key.Operation != key.Operation {
		return
	}
	delete(store.entries, key.RequestID)
	store.inFlight--
	if key.Operation.mutation() {
		store.mutationSlots--
	}
}

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
	if entry != nil && entry.lru == element && !entry.inFlight && entry.key.Operation == OperationStatus {
		delete(store.entries, requestID)
	}
	store.statusLRU.Remove(element)
}
