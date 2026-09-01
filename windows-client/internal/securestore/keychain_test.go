package securestore

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

// Mutation caught: using the logical key directly, uppercase hex, or a hash other than SHA-256.
func TestKeychainAccountNameIsLowercaseSHA256(t *testing.T) {
	t.Parallel()

	const want = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if got := keychainAccountName("abc"); got != want {
		t.Fatalf("keychainAccountName(abc) = %q, want %q", got, want)
	}
}

// Mutation caught: allowing unusable keys or empty secrets to reach and mutate native storage.
func TestKeychainStoreValidatesKeysAndValues(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newKeychainStore(newStatefulKeychainNative())
	tests := []struct {
		name string
		run  func() error
	}{
		{name: "put empty key", run: func() error { return store.Put(ctx, "", []byte("secret")) }},
		{name: "put empty value", run: func() error { return store.Put(ctx, "identity", nil) }},
		{name: "get empty key", run: func() error { _, err := store.Get(ctx, ""); return err }},
		{name: "delete empty key", run: func() error { return store.Delete(ctx, "") }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := test.run(); err == nil {
				t.Fatal("operation succeeded, want validation error")
			}
		})
	}
}

// Mutation caught: returning a platform status directly instead of the Store contract's ErrNotFound.
func TestKeychainStoreMapsMissingGetToErrNotFound(t *testing.T) {
	t.Parallel()

	store := newKeychainStore(newStatefulKeychainNative())
	if _, err := store.Get(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get() error = %v, want ErrNotFound", err)
	}
}

// Mutation caught: treating an idempotent delete of a missing item as a caller-visible failure.
func TestKeychainStoreDeleteMissingIsSuccessful(t *testing.T) {
	t.Parallel()

	store := newKeychainStore(newStatefulKeychainNative())
	if err := store.Delete(context.Background(), "missing"); err != nil {
		t.Fatalf("Delete() error = %v, want nil", err)
	}
}

// Mutation caught: collapsing native failures other than item-not-found into ErrNotFound.
func TestKeychainStorePreservesUnexpectedNativeStatus(t *testing.T) {
	t.Parallel()

	native := newStatefulKeychainNative()
	native.getFailure = keychainStatus(-34018)
	store := newKeychainStore(native)
	_, err := store.Get(context.Background(), "identity")
	if err == nil {
		t.Fatal("Get() succeeded, want native status error")
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatalf("Get() error = %v, must not map unexpected status to ErrNotFound", err)
	}
	var statusErr keychainStatusError
	if !errors.As(err, &statusErr) || statusErr.status != keychainStatus(-34018) {
		t.Fatalf("Get() error = %v, want status -34018", err)
	}
	if !strings.Contains(err.Error(), "get secure value") {
		t.Fatalf("Get() error = %q, want operation context", err)
	}
}

// Mutation caught: deleting an existing value before attempting a replacement that can fail.
func TestKeychainStoreFailedReplacementPreservesOldValue(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	native := newStatefulKeychainNative()
	store := newKeychainStore(native)
	if err := store.Put(ctx, "identity", []byte("old-value")); err != nil {
		t.Fatal(err)
	}
	native.updateFailure = keychainStatus(-25308)
	if err := store.Put(ctx, "identity", []byte("new-value")); err == nil {
		t.Fatal("replacement succeeded, want injected native update failure")
	}
	loaded, err := store.Get(ctx, "identity")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(loaded, []byte("old-value")) {
		t.Fatalf("Get() after failed replacement = %q, want old value", loaded)
	}
}

// Mutation caught: returning duplicate-item during an add race instead of retrying atomic update.
func TestKeychainStoreDuplicateSafeAddConvergesOnRequestedValue(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	native := newStatefulKeychainNative()
	native.concurrentAddValue = []byte("racing-value")
	store := newKeychainStore(native)
	if err := store.Put(ctx, "identity", []byte("requested-value")); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Get(ctx, "identity")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(loaded, []byte("requested-value")) {
		t.Fatalf("Get() after add race = %q, want requested value", loaded)
	}
}

type statefulKeychainNative struct {
	items              map[string][]byte
	getFailure         keychainStatus
	updateFailure      keychainStatus
	concurrentAddValue []byte
}

func newStatefulKeychainNative() *statefulKeychainNative {
	return &statefulKeychainNative{items: make(map[string][]byte)}
}

func (native *statefulKeychainNative) Add(service, account string, value []byte) keychainStatus {
	identity := service + "\x00" + account
	if native.concurrentAddValue != nil {
		native.items[identity] = append([]byte(nil), native.concurrentAddValue...)
		native.concurrentAddValue = nil
		return keychainStatusDuplicateItem
	}
	if _, exists := native.items[identity]; exists {
		return keychainStatusDuplicateItem
	}
	native.items[identity] = append([]byte(nil), value...)
	return keychainStatusSuccess
}

func (native *statefulKeychainNative) Update(service, account string, value []byte) keychainStatus {
	if native.updateFailure != keychainStatusSuccess {
		status := native.updateFailure
		native.updateFailure = keychainStatusSuccess
		return status
	}
	identity := service + "\x00" + account
	if _, exists := native.items[identity]; !exists {
		return keychainStatusItemNotFound
	}
	native.items[identity] = append([]byte(nil), value...)
	return keychainStatusSuccess
}

func (native *statefulKeychainNative) Get(service, account string) ([]byte, keychainStatus) {
	if native.getFailure != keychainStatusSuccess {
		return nil, native.getFailure
	}
	value, exists := native.items[service+"\x00"+account]
	if !exists {
		return nil, keychainStatusItemNotFound
	}
	return append([]byte(nil), value...), keychainStatusSuccess
}

func (native *statefulKeychainNative) Delete(service, account string) keychainStatus {
	identity := service + "\x00" + account
	if _, exists := native.items[identity]; !exists {
		return keychainStatusItemNotFound
	}
	delete(native.items, identity)
	return keychainStatusSuccess
}
