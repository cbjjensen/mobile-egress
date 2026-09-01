//go:build darwin && cgo && macintegration && !bindings

package securestore

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"testing"
)

// Mutation caught: writing outside the fixed service/account identity or with synchronizing/migrating attributes.
func TestKeychainStoreIntegrationCRUDAndDeviceOnlyAttributes(t *testing.T) {
	ctx := context.Background()
	store, err := NewKeychainStore()
	if err != nil {
		t.Fatal(err)
	}
	key := integrationLogicalKey(t)
	t.Cleanup(func() { _ = store.Delete(context.Background(), key) })

	if err := store.Put(ctx, key, []byte("first-secret")); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Get(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(loaded, []byte("first-secret")) {
		t.Fatalf("Get() = %q, want first secret", loaded)
	}

	attributes, err := darwinKeychainItemAttributes(keychainAccountName(key))
	if err != nil {
		t.Fatal(err)
	}
	if attributes.service != keychainService {
		t.Fatalf("service = %q, want %q", attributes.service, keychainService)
	}
	if attributes.account != keychainAccountName(key) {
		t.Fatalf("account = %q, want hashed logical key", attributes.account)
	}
	if !attributes.explicitlyNonSynchronizing {
		t.Fatal("item is not explicitly marked non-synchronizing")
	}
	if !attributes.whenUnlockedThisDeviceOnly {
		t.Fatal("item is not WhenUnlockedThisDeviceOnly")
	}
	if attributes.accessGroup == "" {
		t.Fatal("item has no code-signing access group")
	}
}

// Mutation caught: delete-before-add replacement or changing service/account/access-group across store upgrades.
func TestKeychainStoreIntegrationReplacementPreservesIdentityAndSigningAccess(t *testing.T) {
	ctx := context.Background()
	firstStore, err := NewKeychainStore()
	if err != nil {
		t.Fatal(err)
	}
	key := integrationLogicalKey(t)
	t.Cleanup(func() { _ = firstStore.Delete(context.Background(), key) })

	if err := firstStore.Put(ctx, key, []byte("old-secret")); err != nil {
		t.Fatal(err)
	}
	before, err := darwinKeychainItemAttributes(keychainAccountName(key))
	if err != nil {
		t.Fatal(err)
	}

	upgradedStore, err := NewKeychainStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := upgradedStore.Put(ctx, key, []byte("replacement-secret")); err != nil {
		t.Fatal(err)
	}
	loaded, err := firstStore.Get(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(loaded, []byte("replacement-secret")) {
		t.Fatalf("Get() after replacement = %q, want replacement", loaded)
	}
	after, err := darwinKeychainItemAttributes(keychainAccountName(key))
	if err != nil {
		t.Fatal(err)
	}
	if after.service != before.service || after.account != before.account || after.accessGroup != before.accessGroup {
		t.Fatalf("item identity changed across replacement: before=%+v after=%+v", before, after)
	}
}

// Mutation caught: returning native missing-item errors or leaving data behind after cleanup.
func TestKeychainStoreIntegrationMissingAndCleanup(t *testing.T) {
	ctx := context.Background()
	store, err := NewKeychainStore()
	if err != nil {
		t.Fatal(err)
	}
	key := integrationLogicalKey(t)
	t.Cleanup(func() { _ = store.Delete(context.Background(), key) })

	if _, err := store.Get(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("initial Get() error = %v, want ErrNotFound", err)
	}
	if err := store.Put(ctx, key, []byte("temporary-secret")); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(ctx, key); err != nil {
		t.Fatalf("second Delete() error = %v, want nil", err)
	}
	if _, err := store.Get(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get() after cleanup error = %v, want ErrNotFound", err)
	}
}

func integrationLogicalKey(t *testing.T) string {
	t.Helper()
	var suffix [16]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatal(err)
	}
	return "mobile-egress-integration-" + hex.EncodeToString(suffix[:])
}
