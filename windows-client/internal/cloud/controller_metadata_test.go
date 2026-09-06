package cloud

import (
	"context"
	"encoding/json"
	"mobile-egress/windows-client/internal/securestore"
	"strings"
	"testing"
)

type metadataCountingStore struct {
	securestore.Store
	reads int
}

func (store *metadataCountingStore) Get(ctx context.Context, key string) ([]byte, error) {
	store.reads++
	return store.Store.Get(ctx, key)
}

func TestControllerMetadataReadsOnceAndReturnsSanitizedCopies(t *testing.T) {
	ctx := context.Background()
	store := &metadataCountingStore{Store: securestore.NewMemoryStore()}
	repository := NewRepository(store)
	reader, ok := any(repository).(interface {
		ControllerMetadata(context.Context) ([]ManagedNodeView, []string, error)
	})
	if !ok {
		t.Fatal("Repository does not expose ControllerMetadata")
	}
	for _, id := range []string{"i-0123456789abcdef2", "i-0123456789abcdef1"} {
		if err := repository.SaveNode(ctx, testManagedNode(id)); err != nil {
			t.Fatal(err)
		}
	}
	if err := repository.ReserveNode(ctx, "i-0123456789abcdef3"); err != nil {
		t.Fatal(err)
	}
	store.reads = 0
	nodes, reservations, err := reader.ControllerMetadata(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if store.reads != 1 {
		t.Fatalf("metadata required %d secure store reads", store.reads)
	}
	if len(nodes) != 2 || nodes[0].InstanceID != "i-0123456789abcdef1" || len(reservations) != 1 || reservations[0] != "i-0123456789abcdef3" {
		t.Fatalf("unexpected metadata: %v / %v", nodes, reservations)
	}
	encoded, _ := json.Marshal(nodes)
	if strings.Contains(string(encoded), "Password") || strings.Contains(string(encoded), "certificate") || nodes[0].Proxy != "127.0.0.2:1081:***:***" {
		t.Fatalf("metadata exposed credentials: %s", encoded)
	}
	nodes[0].InstanceID = "changed"
	reservations[0] = "changed"
	again, reserved, err := reader.ControllerMetadata(ctx)
	if err != nil || again[0].InstanceID != "i-0123456789abcdef1" || reserved[0] != "i-0123456789abcdef3" {
		t.Fatal("returned metadata aliases stored state")
	}
}
