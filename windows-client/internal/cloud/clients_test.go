package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"mobile-egress/windows-client/internal/securestore"
)

func TestClientRegistryMigratesEC2WithoutChangingIdentity(t *testing.T) {
	ctx := context.Background()
	store := securestore.NewMemoryStore()
	old := testManagedNode("i-0123456789abcdef0")
	raw, _ := json.Marshal(controllerState{Version: 2, Nodes: []ManagedNode{old}, NodeReservations: []string{"i-0123456789abcdef1"}})
	if err := store.Put(ctx, controllerStateKey, raw); err != nil {
		t.Fatal(err)
	}
	repo := NewRepository(store)
	nodes, err := repo.Nodes(ctx)
	if err != nil || len(nodes) != 1 {
		t.Fatalf("migration: %v %v", nodes, err)
	}
	node := nodes[0]
	if node.NodeID != old.InstanceID || node.Management != ManagementAWS || node.Platform != "windows" || node.Architecture != "amd64" || node.SOCKSPassword != old.SOCKSPassword || node.ClientSerial != old.ClientSerial || node.ConfigurationGeneration != old.ConfigurationGeneration {
		t.Fatal("migration changed identity or omitted provider metadata")
	}
	reserved, err := repo.NodeReservations(ctx)
	if err != nil || len(reserved) != 1 || reserved[0] != "i-0123456789abcdef1" {
		t.Fatalf("lost reservation: %v %v", reserved, err)
	}
}

func TestPairedMacRegistryNeedsNoAWSAndCopiesCorrectEndpoint(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(securestore.NewMemoryStore())
	id := "paired-0123456789abcdef0123456789abcdef"
	if err := repo.ReserveClient(ctx, id, "Work Mac"); err != nil {
		t.Fatal(err)
	}
	pending := PendingClient{NodeID: id, DisplayName: "Work Mac", EnrollmentID: "enrollment", Invitation: "private-invitation", ExpiresAt: time.Now().Add(time.Minute)}
	if err := repo.SaveClientInvitation(ctx, pending); err != nil {
		t.Fatal(err)
	}
	node := testPairedNode(id)
	if err := repo.SaveNode(ctx, node); err != nil {
		t.Fatal(err)
	}
	views, err := repo.NodeViews(ctx)
	if err != nil || len(views) != 1 || views[0].NodeID != id || views[0].Proxy != "127.0.0.1:1081:***:***" || views[0].ProxyReady {
		t.Fatalf("unacknowledged views: %+v %v", views, err)
	}
	if _, err := repo.ProxyLine(ctx, id); err == nil {
		t.Fatal("copied credentials before initial acknowledgement")
	}
	node.AppliedGeneration = 1
	node.Health = "installed"
	if err := repo.SaveNode(ctx, node); err != nil {
		t.Fatal(err)
	}
	line, err := repo.ProxyLine(ctx, id)
	if err != nil || line != "127.0.0.1:1081:user:password" {
		t.Fatalf("Mac HTTP copy: %q %v", line, err)
	}
	url, err := repo.SOCKSProxyURL(ctx, id)
	if err != nil || url != "socks5://user:password@127.0.0.1:1080" {
		t.Fatalf("Mac SOCKS copy: %q %v", url, err)
	}
	views, _ = repo.NodeViews(ctx)
	raw, _ := json.Marshal(views)
	for _, secret := range []string{"private-invitation", "password", "certificate", "configurationPublicKey"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("view leaks %q", secret)
		}
	}
	if err := repo.CompleteClientInvitation(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := repo.RemoveClient(ctx, id); err != nil {
		t.Fatal(err)
	}
	nodes, _ := repo.Nodes(ctx)
	if len(nodes) != 0 {
		t.Fatal("removed client remains")
	}
}

func TestMixedReservationsShareTenClientLimit(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(securestore.NewMemoryStore())
	for i := 0; i < 9; i++ {
		if err := repo.SaveNode(ctx, testManagedNode(fmt.Sprintf("i-%017x", i+1))); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		results <- repo.ReserveClient(ctx, "paired-0123456789abcdef0123456789abcdef", "Mac")
	}()
	go func() { defer wg.Done(); results <- repo.ReserveNode(ctx, "i-aaaaaaaaaaaaaaaaa") }()
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("admitted %d reservations for one free slot", successes)
	}
}

func TestClientRegistryRejectsPlatformAndManagementMismatch(t *testing.T) {
	repo := NewRepository(securestore.NewMemoryStore())
	node := testPairedNode("paired-0123456789abcdef0123456789abcdef")
	for _, platform := range []string{"linux", "", "../macos"} {
		node.Platform = platform
		if err := repo.SaveNode(context.Background(), node); err == nil {
			t.Fatalf("accepted platform %q", platform)
		}
	}
	node = testPairedNode(node.NodeID)
	node.Management = ManagementAWS
	if err := repo.SaveNode(context.Background(), node); err == nil {
		t.Fatal("accepted paired id as AWS node")
	}
}

func testPairedNode(id string) ManagedNode {
	node := testManagedNode("")
	node.NodeID = id
	node.DisplayName = "Work Mac"
	node.Management = ManagementPaired
	node.Platform = "macos"
	node.Architecture = "arm64"
	node.EnrollmentID = "enrollment"
	node.Health = "configuring"
	return node
}
