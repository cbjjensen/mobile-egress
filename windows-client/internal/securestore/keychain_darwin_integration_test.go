//go:build darwin && cgo && macintegration && !bindings

package securestore

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const (
	signedIntegrationPhaseEnvironment = "MOBILE_EGRESS_MAC_KEYCHAIN_PHASE"
	signedIntegrationStateEnvironment = "MOBILE_EGRESS_MAC_KEYCHAIN_STATE"
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

	attributes, err := darwinKeychainItemAttributes(store.accessGroup, keychainAccountName(key))
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
	if attributes.accessGroup != store.accessGroup {
		t.Fatalf("access group = %q, want exact signed group %q", attributes.accessGroup, store.accessGroup)
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
	before, err := darwinKeychainItemAttributes(firstStore.accessGroup, keychainAccountName(key))
	if err != nil {
		t.Fatal(err)
	}
	beforePersistentReference, err := darwinKeychainItemPersistentReference(firstStore.accessGroup, keychainAccountName(key))
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
	after, err := darwinKeychainItemAttributes(upgradedStore.accessGroup, keychainAccountName(key))
	if err != nil {
		t.Fatal(err)
	}
	afterPersistentReference, err := darwinKeychainItemPersistentReference(upgradedStore.accessGroup, keychainAccountName(key))
	if err != nil {
		t.Fatal(err)
	}
	if after.service != before.service || after.account != before.account || after.accessGroup != before.accessGroup {
		t.Fatalf("item identity changed across replacement: before=%+v after=%+v", before, after)
	}
	if !bytes.Equal(afterPersistentReference, beforePersistentReference) {
		t.Fatal("persistent item reference changed across replacement")
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

// Mutation caught: an app upgrade losing access to the prior signed identity, recreating the item, or skipping cleanup.
func TestKeychainStoreSignedVersionUpgradePhase(t *testing.T) {
	phase := os.Getenv(signedIntegrationPhaseEnvironment)
	if phase == "" {
		t.Skip("run through the signed macOS Keychain integration harness")
	}
	statePath := os.Getenv(signedIntegrationStateEnvironment)
	if statePath == "" || !filepath.IsAbs(statePath) {
		t.Fatal("signed Keychain integration state path must be absolute")
	}

	switch phase {
	case "A":
		runSignedVersionAPhase(t, statePath)
	case "B":
		runSignedVersionBPhase(t, statePath)
	case "cleanup":
		runSignedCleanupPhase(t, statePath)
	default:
		t.Fatalf("unsupported signed Keychain integration phase %q", phase)
	}
}

type signedUpgradeState struct {
	LogicalKey          string `json:"logicalKey"`
	PersistentReference string `json:"persistentReference"`
}

func runSignedVersionAPhase(t *testing.T, statePath string) {
	t.Helper()
	if _, err := os.Stat(statePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("signed integration state must not exist before version A: %v", err)
	}
	store, err := NewKeychainStore()
	if err != nil {
		t.Fatal(err)
	}
	key := integrationLogicalKey(t)
	keepForVersionB := false
	t.Cleanup(func() {
		if !keepForVersionB {
			_ = store.Delete(context.Background(), key)
		}
	})
	if err := store.Put(context.Background(), key, []byte("non-secret-version-a-fixture")); err != nil {
		t.Fatal(err)
	}
	persistentReference, err := darwinKeychainItemPersistentReference(store.accessGroup, keychainAccountName(key))
	if err != nil {
		t.Fatal(err)
	}
	state := signedUpgradeState{
		LogicalKey:          key,
		PersistentReference: hex.EncodeToString(persistentReference),
	}
	file, err := os.OpenFile(statePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(file).Encode(state); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	keepForVersionB = true
}

func runSignedVersionBPhase(t *testing.T, statePath string) {
	t.Helper()
	state := readSignedUpgradeState(t, statePath)
	store, err := NewKeychainStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Delete(context.Background(), state.LogicalKey)
		_ = os.Remove(statePath)
	})
	loaded, err := store.Get(context.Background(), state.LogicalKey)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(loaded, []byte("non-secret-version-a-fixture")) {
		t.Fatalf("version B initial Get() = %q, want version A fixture", loaded)
	}
	wantPersistentReference, err := hex.DecodeString(state.PersistentReference)
	if err != nil || len(wantPersistentReference) == 0 {
		t.Fatalf("invalid version A persistent reference: %v", err)
	}
	before, err := darwinKeychainItemPersistentReference(store.accessGroup, keychainAccountName(state.LogicalKey))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, wantPersistentReference) {
		t.Fatal("version B resolved a different item than version A")
	}
	if err := store.Put(context.Background(), state.LogicalKey, []byte("non-secret-version-b-fixture")); err != nil {
		t.Fatal(err)
	}
	after, err := darwinKeychainItemPersistentReference(store.accessGroup, keychainAccountName(state.LogicalKey))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, wantPersistentReference) {
		t.Fatal("version B replacement changed the persistent item identity")
	}
	loaded, err = store.Get(context.Background(), state.LogicalKey)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(loaded, []byte("non-secret-version-b-fixture")) {
		t.Fatalf("version B replacement Get() = %q, want version B fixture", loaded)
	}
	if err := store.Delete(context.Background(), state.LogicalKey); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(context.Background(), state.LogicalKey); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get() after signed cleanup error = %v, want ErrNotFound", err)
	}
}

func runSignedCleanupPhase(t *testing.T, statePath string) {
	t.Helper()
	if _, err := os.Stat(statePath); errors.Is(err, os.ErrNotExist) {
		return
	} else if err != nil {
		t.Fatal(err)
	}
	state := readSignedUpgradeState(t, statePath)
	store, err := NewKeychainStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(context.Background(), state.LogicalKey); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(statePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}

func readSignedUpgradeState(t *testing.T, statePath string) signedUpgradeState {
	t.Helper()
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var state signedUpgradeState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	if state.LogicalKey == "" || state.PersistentReference == "" {
		t.Fatal("signed integration state is incomplete")
	}
	return state
}

func integrationLogicalKey(t *testing.T) string {
	t.Helper()
	var suffix [16]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatal(err)
	}
	return "mobile-egress-integration-" + hex.EncodeToString(suffix[:])
}
