//go:build darwin && cgo && macuserintegration && !bindings

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

func TestSignedUserKeychainCRUD(t *testing.T) {
	if os.Getenv("MOBILE_EGRESS_USER_KEYCHAIN_TEST") != "1" {
		t.Skip("explicit signed user Keychain acceptance only")
	}
	if os.Geteuid() == 0 {
		t.Fatal("user acceptance must never run as root")
	}
	store, err := NewUserKeychainStore()
	if err != nil {
		t.Fatal(err)
	}
	key := userKeychainFixtureName(t)
	ctx := context.Background()
	t.Cleanup(func() {
		if err := store.Delete(ctx, key); err != nil {
			t.Error(err)
		}
	})
	if _, err := store.Get(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatal("new fixture must not exist", err)
	}
	if err := store.Put(ctx, key, []byte("user-fixture-a")); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewUserKeychainStore()
	if err != nil {
		t.Fatal(err)
	}
	if value, err := reopened.Get(ctx, key); err != nil || string(value) != "user-fixture-a" {
		t.Fatal("user Keychain reopening lost the fixture", err)
	}
	if err := reopened.Put(ctx, key, []byte("user-fixture-b")); err != nil {
		t.Fatal(err)
	}
	if value, err := store.Get(ctx, key); err != nil || string(value) != "user-fixture-b" {
		t.Fatal("user Keychain replacement failed", err)
	}
	if err := store.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Get(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted user item still exists", err)
	}
}

func TestUserKeychainWrongIdentityRejected(t *testing.T) {
	if os.Getenv("MOBILE_EGRESS_USER_KEYCHAIN_EXPECT_REJECT") != "1" {
		t.Skip("explicit wrong-identifier acceptance only")
	}
	store, err := NewUserKeychainStore()
	if err == nil || store != nil {
		t.Fatal("an unsigned or differently identified executable opened the user store")
	}
}

func TestUserKeychainSameSignedUpgrade(t *testing.T) {
	phase := os.Getenv("MOBILE_EGRESS_USER_KEYCHAIN_PHASE")
	if phase == "" {
		t.Skip("explicit two-build upgrade acceptance only")
	}
	statePath := os.Getenv("MOBILE_EGRESS_USER_KEYCHAIN_STATE")
	if !filepath.IsAbs(statePath) {
		t.Fatal("fixture name file requires an absolute path")
	}
	store, err := NewUserKeychainStore()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if phase == "A" {
		key := userKeychainFixtureName(t)
		if err := store.Put(ctx, key, []byte("same-signed-a")); err != nil {
			t.Fatal(err)
		}
		file, err := os.OpenFile(statePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
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
	if phase != "B" && phase != "cleanup" {
		t.Fatal("invalid user Keychain upgrade phase")
	}
	keyBytes, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	key := string(keyBytes)
	if len(key) != len("user-acceptance-")+32 || key[:len("user-acceptance-")] != "user-acceptance-" {
		t.Fatal("invalid user fixture name")
	}
	if _, err := hex.DecodeString(key[len("user-acceptance-"):]); err != nil {
		t.Fatal("invalid user fixture suffix")
	}
	if phase == "B" {
		if value, err := store.Get(ctx, key); err != nil || string(value) != "same-signed-a" {
			t.Fatal("same-signed replacement cannot read original user item", err)
		}
		if err := store.Put(ctx, key, []byte("same-signed-b")); err != nil {
			t.Fatal(err)
		}
		if value, err := store.Get(ctx, key); err != nil || string(value) != "same-signed-b" {
			t.Fatal("same-signed replacement cannot update original user item", err)
		}
	}
	if err := store.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(statePath); err != nil {
		t.Fatal(err)
	}
}

func userKeychainFixtureName(t *testing.T) string {
	t.Helper()
	var suffix [16]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatal(err)
	}
	return "user-acceptance-" + hex.EncodeToString(suffix[:])
}
