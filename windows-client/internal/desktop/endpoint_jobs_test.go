package desktop

import (
	"context"
	"errors"
	"testing"

	"mobile-egress/windows-client/internal/cloud"
	"mobile-egress/windows-client/internal/securestore"
)

type mixedEndpointRunner struct{ calls []string }

func (runner *mixedEndpointRunner) RunPowerShell(_ context.Context, instanceID, _ string) (string, error) {
	runner.calls = append(runner.calls, instanceID)
	if instanceID == "i-00000000000000001" {
		return "", errors.New("SSM offline")
	}
	return `{"configured":true}`, nil
}

func TestEndpointJobContinuesPastOfflineClient(t *testing.T) {
	ctx := context.Background()
	repo := cloud.NewRepository(securestore.NewMemoryStore())
	for _, id := range []string{"i-00000000000000001", "i-00000000000000002"} {
		node := pairedNodeForDesktopTest(t)
		node.NodeID, node.InstanceID, node.DisplayName = id, id, id
		node.Platform, node.Architecture, node.Management = "windows", "amd64", cloud.ManagementAWS
		node.EnrollmentID = ""
		node.Health = "installed"
		if err := repo.SaveNode(ctx, node); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := repo.StageClientEndpoints(ctx, "https://new.example:8443"); err != nil {
		t.Fatal(err)
	}
	app := &DesktopApp{cloudRepository: repo}
	runner := &mixedEndpointRunner{}
	app.applyAWSEndpointUpdates(runner)
	if len(runner.calls) != 2 {
		t.Fatalf("offline Client starved healthy Client: calls %v", runner.calls)
	}
	nodes, err := repo.Nodes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if nodes[0].Health != "configuring" || nodes[1].Health != "installed" || nodes[1].AppliedGeneration != 2 {
		t.Fatal("incorrect per-Client recovery outcome")
	}
}
