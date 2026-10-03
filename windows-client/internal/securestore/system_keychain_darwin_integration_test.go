//go:build darwin && cgo && macsystemintegration && !bindings

package securestore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// Build this test twice, sign both versions with the same Developer ID and
// daemon identifier, then run phase A and phase B as root using the same
// absolute state path. The state file contains only a random item name.
func TestSystemKeychainSameSignedUpgrade(t *testing.T) {
	phase := os.Getenv("MOBILE_EGRESS_SYSTEM_KEYCHAIN_PHASE")
	if phase == "" {
		t.Skip("signed root upgrade acceptance was not requested")
	}
	path := os.Getenv("MOBILE_EGRESS_SYSTEM_KEYCHAIN_STATE")
	if !filepath.IsAbs(path) {
		t.Fatal("acceptance state path must be absolute")
	}
	store, err := NewSystemKeychainStore()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if phase == "A" {
		var suffix [16]byte
		if _, err := rand.Read(suffix[:]); err != nil {
			t.Fatal(err)
		}
		key := "signed-upgrade-" + hex.EncodeToString(suffix[:])
		if err := store.Put(ctx, key, []byte("version-a-fixture")); err != nil {
			t.Fatal(err)
		}
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			_ = store.Delete(ctx, key)
			t.Fatal(err)
		}
		_, writeErr := file.WriteString(key)
		if err := errors.Join(writeErr, file.Close()); err != nil {
			_ = store.Delete(ctx, key)
			t.Fatal(err)
		}
		return
	}
	keyBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	key := string(keyBytes)
	if len(key) != len("signed-upgrade-")+32 {
		t.Fatal("acceptance item name is invalid")
	}
	if phase == "B" {
		value, err := store.Get(ctx, key)
		if err != nil || string(value) != "version-a-fixture" {
			t.Fatal("same-signed upgrade lost access to the original item", err)
		}
		if err := store.Put(ctx, key, []byte("version-b-fixture")); err != nil {
			t.Fatal(err)
		}
		value, err = store.Get(ctx, key)
		if err != nil || string(value) != "version-b-fixture" {
			t.Fatal("same-signed upgrade could not replace the existing item", err)
		}
	} else if phase != "cleanup" {
		t.Fatal("invalid signed upgrade phase")
	}
	if err := store.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}

// Explicit native acceptance only: run as root from a Developer ID signed test
// binary with identifier com.zfnf.mobile-egress.client. Ordinary go test does
// not modify System.keychain or request elevation.
func TestSignedRootSystemKeychainCRUD(t *testing.T) {
	store, err := NewSystemKeychainStore()
	if err != nil {
		t.Fatal(err)
	}
	var suffix [16]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatal(err)
	}
	key := "native-acceptance-" + hex.EncodeToString(suffix[:])
	ctx := context.Background()
	t.Cleanup(func() {
		if err := store.Delete(ctx, key); err != nil {
			t.Error(err)
		}
	})
	if err := store.Put(ctx, key, []byte("fixture-a")); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewSystemKeychainStore()
	if err != nil {
		t.Fatal(err)
	}
	data, err := reopened.Get(ctx, key)
	if err != nil || string(data) != "fixture-a" {
		t.Fatal("System Keychain did not reopen the original item", err)
	}
	if err := reopened.Put(ctx, key, []byte("fixture-b")); err != nil {
		t.Fatal(err)
	}
	data, err = store.Get(ctx, key)
	if err != nil || string(data) != "fixture-b" {
		t.Fatal("System Keychain did not update the original item", err)
	}
	if err := store.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatal("System Keychain missing item status mismatch", err)
	}
}
