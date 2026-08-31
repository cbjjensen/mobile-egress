package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"mobile-egress/windows-client/internal/securestore"
)

func TestEncryptedRepositoryPersistsAccessKeysAndManagedNodes(t *testing.T) {
	t.Parallel()

	repository := NewRepository(securestore.NewMemoryStore())
	credentials := StoredAccessKeys{AccessKeyID: "AKIAEXAMPLE", SecretAccessKey: "secret", SessionToken: "token"}
	if err := repository.SaveAccessKeys(context.Background(), credentials); err != nil {
		t.Fatal(err)
	}
	loaded, err := repository.AccessKeys(context.Background())
	if err != nil || loaded != credentials {
		t.Fatalf("AccessKeys() = %#v/%v", loaded, err)
	}
	node := ManagedNode{
		InstanceID: "i-0123456789abcdef0", ClientSerial: "A1", ConfigurationPublicKey: "public", ConfigurationGeneration: 1,
		ServiceVersion: "1.2.3", Health: "healthy", SOCKSUsername: "user", SOCKSPassword: "password", SOCKSPort: 1080,
		RelayURL: "https://bridge.tail123.ts.net:8443", CertificatePEM: "certificate", CACertificatePEM: "ca",
	}
	if err := repository.SaveNode(context.Background(), node); err != nil {
		t.Fatal(err)
	}
	views, err := repository.NodeViews(context.Background())
	if err != nil || len(views) != 1 || views[0].InstanceID != node.InstanceID || views[0].Proxy != "127.0.0.1:1081:***:***" || !views[0].HTTPProxyReady {
		t.Fatalf("NodeViews() = %#v/%v", views, err)
	}
	proxy, err := repository.ProxyLine(context.Background(), node.InstanceID)
	if err != nil || proxy != "127.0.0.1:1081:user:password" {
		t.Fatalf("ProxyLine() = %q/%v", proxy, err)
	}
	socksURL, err := repository.SOCKSProxyURL(context.Background(), node.InstanceID)
	if err != nil || socksURL != "socks5://user:password@127.0.0.1:1080" {
		t.Fatalf("SOCKSProxyURL() = %q/%v", socksURL, err)
	}
}

func TestHTTPProxyLineRequiresAClientVersionWithHTTPConnect(t *testing.T) {
	t.Parallel()

	repository := NewRepository(securestore.NewMemoryStore())
	node := testManagedNode("i-0123456789abcdef0")
	node.ServiceVersion = "1.0.21"
	if err := repository.SaveNode(context.Background(), node); err != nil {
		t.Fatal(err)
	}
	views, err := repository.NodeViews(context.Background())
	if err != nil || len(views) != 1 || views[0].HTTPProxyReady {
		t.Fatalf("legacy NodeViews() = %#v/%v, want HTTP proxy unavailable", views, err)
	}
	if _, err := repository.ProxyLine(context.Background(), node.InstanceID); err == nil {
		t.Fatal("ProxyLine() exposed an HTTP endpoint for a pre-1.0.22 Client")
	}
	if socksURL, err := repository.SOCKSProxyURL(context.Background(), node.InstanceID); err != nil || socksURL != "socks5://user:password@127.0.0.1:1080" {
		t.Fatalf("legacy SOCKSProxyURL() = %q/%v", socksURL, err)
	}

	node.ServiceVersion = "1.0.22"
	if err := repository.SaveNode(context.Background(), node); err != nil {
		t.Fatal(err)
	}
	views, err = repository.NodeViews(context.Background())
	if err != nil || len(views) != 1 || !views[0].HTTPProxyReady {
		t.Fatalf("1.0.22 NodeViews() = %#v/%v, want HTTP proxy ready", views, err)
	}
	if line, err := repository.ProxyLine(context.Background(), node.InstanceID); err != nil || line != "127.0.0.1:1081:user:password" {
		t.Fatalf("1.0.22 ProxyLine() = %q/%v", line, err)
	}
}

func TestRepositoryMigratesVersionOneManagedNodeGeneration(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := securestore.NewMemoryStore()
	node := testManagedNode("i-0123456789abcdef0")
	node.ConfigurationGeneration = 0
	legacy, err := json.Marshal(controllerState{Version: 1, Nodes: []ManagedNode{node}})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, controllerStateKey, legacy); err != nil {
		t.Fatal(err)
	}

	nodes, err := NewRepository(store).Nodes(ctx)
	if err != nil {
		t.Fatalf("Nodes() did not migrate version-one state: %v", err)
	}
	if len(nodes) != 1 || nodes[0].ConfigurationGeneration != 1 {
		t.Fatalf("migrated nodes = %#v", nodes)
	}
	raw, err := store.Get(ctx, controllerStateKey)
	if err != nil {
		t.Fatal(err)
	}
	var state controllerState
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	if state.Version != 2 {
		t.Fatalf("persisted migrated version = %d, want 2", state.Version)
	}
}

func TestNodeReservationsAtomicallyEnforceTheManagedNodeLimit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repository := NewRepository(securestore.NewMemoryStore())
	for index := 0; index < MaximumManagedNodes-1; index++ {
		if err := repository.SaveNode(ctx, testManagedNode(fmt.Sprintf("i-%017x", index+1))); err != nil {
			t.Fatal(err)
		}
	}

	instanceIDs := []string{"i-aaaaaaaaaaaaaaaaa", "i-bbbbbbbbbbbbbbbbb"}
	errorsByCall := make([]error, len(instanceIDs))
	var wait sync.WaitGroup
	for index, instanceID := range instanceIDs {
		wait.Add(1)
		go func(index int, instanceID string) {
			defer wait.Done()
			errorsByCall[index] = repository.ReserveNode(ctx, instanceID)
		}(index, instanceID)
	}
	wait.Wait()
	successes := 0
	for _, err := range errorsByCall {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent reservations succeeded %d times, errors = %#v", successes, errorsByCall)
	}
	reservations, err := repository.NodeReservations(ctx)
	if err != nil || len(reservations) != 1 {
		t.Fatalf("NodeReservations() = %#v/%v", reservations, err)
	}
	if err := repository.ReserveNode(ctx, reservations[0]); err != nil {
		t.Fatalf("ReserveNode() did not allow recovery of the same reservation: %v", err)
	}
	if err := repository.ReleaseNodeReservation(ctx, reservations[0]); err != nil {
		t.Fatal(err)
	}
}

func TestSaveNodeAtomicallyConsumesItsReservation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repository := NewRepository(securestore.NewMemoryStore())
	node := testManagedNode("i-0123456789abcdef0")
	if err := repository.ReserveNode(ctx, node.InstanceID); err != nil {
		t.Fatal(err)
	}
	if err := repository.SaveNode(ctx, node); err != nil {
		t.Fatal(err)
	}
	reservations, err := repository.NodeReservations(ctx)
	if err != nil || len(reservations) != 0 {
		t.Fatalf("reservation remained after commit: %#v/%v", reservations, err)
	}
}

func TestValidateInstallCandidateRejectsAnEleventhNodeBeforeOrchestration(t *testing.T) {
	t.Parallel()

	nodes := make([]ManagedNode, MaximumManagedNodes)
	for index := range nodes {
		nodes[index].InstanceID = "managed"
	}
	if err := ValidateInstallCandidate(nodes, "i-0123456789abcdef0"); err == nil {
		t.Fatal("ValidateInstallCandidate() accepted an eleventh managed node")
	}
	if err := ValidateInstallCandidate(nodes[:MaximumManagedNodes-1], "i-0123456789abcdef0"); err != nil {
		t.Fatalf("ValidateInstallCandidate() rejected the tenth managed node: %v", err)
	}
	nodes[0].InstanceID = "i-0123456789abcdef0"
	if err := ValidateInstallCandidate(nodes[:1], "i-0123456789abcdef0"); err == nil {
		t.Fatal("ValidateInstallCandidate() accepted a duplicate managed node")
	}
}

func testManagedNode(instanceID string) ManagedNode {
	return ManagedNode{
		InstanceID: instanceID, ClientSerial: "A1", ConfigurationPublicKey: "public", ConfigurationGeneration: 1,
		ServiceVersion: "1.2.3", Health: "healthy", SOCKSUsername: "user", SOCKSPassword: "password", SOCKSPort: 1080,
		RelayURL: "https://bridge.tail123.ts.net:8443", CertificatePEM: "certificate", CACertificatePEM: "ca",
	}
}
