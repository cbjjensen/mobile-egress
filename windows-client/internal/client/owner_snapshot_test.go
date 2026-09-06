package client

import (
	"mobile-egress/windows-client/internal/relayclient"
	"testing"
)

func TestOwnerSnapshotCopiesLoadedIdentityWithoutRepository(t *testing.T) {
	core := &Core{}
	reader, ok := any(core).(interface {
		OwnerSnapshot() (relayclient.Identity, bool)
	})
	if !ok {
		t.Fatal("Core does not expose OwnerSnapshot")
	}
	if identity, ready := reader.OwnerSnapshot(); ready || identity != (relayclient.Identity{}) {
		t.Fatal("empty Core reported an Owner")
	}
	owner := testIdentity("owner", "ABC")
	core.owner = &owner
	snapshot, ready := reader.OwnerSnapshot()
	if !ready || snapshot != owner {
		t.Fatal("snapshot did not return loaded Owner")
	}
	snapshot.RelayURL = "https://changed.example"
	next, _ := reader.OwnerSnapshot()
	if next.RelayURL != owner.RelayURL {
		t.Fatal("snapshot mutation changed the loaded Owner")
	}
	core.owner.RelayURL = "https://updated.example"
	if snapshot.RelayURL != "https://changed.example" {
		t.Fatal("Owner mutation changed an existing snapshot")
	}
}
