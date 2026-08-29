//go:build windows

package securestore

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDPAPIStoreRoundTripsWithoutWritingPlaintext(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	store, err := NewDPAPIStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	secret := []byte("local-socks-password-that-must-not-be-plaintext")
	if err := store.Put(context.Background(), "socks-password", secret); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.Get(context.Background(), "socks-password")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(loaded, secret) {
		t.Fatalf("Get() = %q, want original secret", loaded)
	}

	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("encrypted store files = %d, want 1", len(entries))
	}
	ciphertext, err := os.ReadFile(filepath.Join(directory, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ciphertext, secret) {
		t.Fatal("DPAPI store persisted the secret in plaintext")
	}
}
