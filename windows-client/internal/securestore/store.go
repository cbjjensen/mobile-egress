// Package securestore defines storage for sensitive Windows client state.
package securestore

import (
	"context"
	"errors"
	"sync"
)

var ErrNotFound = errors.New("secure value not found")

// Store persists opaque values without exposing their contents to callers
// other than the current application user.
type Store interface {
	Put(context.Context, string, []byte) error
	Get(context.Context, string) ([]byte, error)
	Delete(context.Context, string) error
}

// MemoryStore is a concurrency-safe test implementation of Store.
type MemoryStore struct {
	mu     sync.RWMutex
	values map[string][]byte
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{values: make(map[string][]byte)}
}

func (store *MemoryStore) Put(ctx context.Context, key string, value []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	store.mu.Lock()
	store.values[key] = append([]byte(nil), value...)
	store.mu.Unlock()
	return nil
}

func (store *MemoryStore) Get(ctx context.Context, key string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	store.mu.RLock()
	value, exists := store.values[key]
	store.mu.RUnlock()
	if !exists {
		return nil, ErrNotFound
	}
	return append([]byte(nil), value...), nil
}

func (store *MemoryStore) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	store.mu.Lock()
	delete(store.values, key)
	store.mu.Unlock()
	return nil
}
