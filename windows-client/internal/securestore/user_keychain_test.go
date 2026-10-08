package securestore

import (
	"context"
	"errors"
	"runtime"
	"testing"
)

func TestUserKeychainUnsupportedFailsClosed(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("signed native app acceptance runs separately")
	}
	store, err := NewUserKeychainStore()
	if store != nil || err == nil {
		t.Fatal("unsupported user storage must fail without a fallback")
	}
}

func TestUserKeychainIsolatesExistingServiceItems(t *testing.T) {
	ctx := context.Background()
	native := newStatefulKeychainNative()
	account := keychainAccountName("identity")
	native.seed("", systemKeychainService, account, []byte("system-fixture"))
	native.seed(testKeychainApplicationIdentifier, keychainService, account, []byte("controller-fixture"))
	store := newUserKeychainStore(native)
	if store.service != "com.zfnf.mobile-egress.client.app.user" || store.accessGroup != "" {
		t.Fatal("user store namespace must be stable and isolated")
	}
	if _, err := store.Get(ctx, "identity"); !errors.Is(err, ErrNotFound) {
		t.Fatal("user store must not read existing system/controller identity", err)
	}
	if err := store.Put(ctx, "identity", []byte("user-fixture")); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(ctx, "identity"); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct{ group, service, value string }{
		{"", systemKeychainService, "system-fixture"},
		{testKeychainApplicationIdentifier, keychainService, "controller-fixture"},
	} {
		value, status := native.Get(item.group, item.service, account)
		if status != keychainStatusSuccess || string(value) != item.value {
			t.Fatal("user operations altered another store")
		}
	}
}

func TestUserKeychainDeniedUpdatePreservesExistingState(t *testing.T) {
	ctx := context.Background()
	native := newStatefulKeychainNative()
	store := newUserKeychainStore(native)
	if err := store.Put(ctx, "pairing", []byte("saved-fixture")); err != nil {
		t.Fatal(err)
	}
	native.updateFailure = keychainStatus(-25308)
	if err := store.Put(ctx, "pairing", []byte("replacement-fixture")); err == nil {
		t.Fatal("locked Keychain replacement must fail")
	}
	native.updateFailure = keychainStatusSuccess
	value, err := newUserKeychainStore(native).Get(ctx, "pairing")
	if err != nil || string(value) != "saved-fixture" {
		t.Fatal("reopening must recover the existing state", err)
	}
}
