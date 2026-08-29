package securestore

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestMemoryStoreCopiesValuesAndReportsMissingKeys(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := NewMemoryStore()
	original := []byte("private-value")
	if err := store.Put(ctx, "identity", original); err != nil {
		t.Fatal(err)
	}
	original[0] = 'X'

	loaded, err := store.Get(ctx, "identity")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(loaded, []byte("private-value")) {
		t.Fatalf("Get() = %q, want an independent copy", loaded)
	}
	loaded[0] = 'Y'
	reloaded, err := store.Get(ctx, "identity")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(reloaded, []byte("private-value")) {
		t.Fatalf("stored value was mutated through Get(): %q", reloaded)
	}

	if err := store.Delete(ctx, "identity"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, "identity"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get() after Delete() error = %v, want ErrNotFound", err)
	}
}
