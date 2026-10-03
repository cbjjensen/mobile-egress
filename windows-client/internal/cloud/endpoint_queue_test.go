package cloud

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"mobile-egress/windows-client/internal/nodeservice"
	"mobile-egress/windows-client/internal/sealedconfig"
	"mobile-egress/windows-client/internal/securestore"
)

func TestStageClientEndpointsRetainsOfflineAWSGenerationsAndInitialPairedEnvelope(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(securestore.NewMemoryStore())
	aws := testManagedNode("i-0123456789abcdef0")
	paired := testPairedNode("paired-0123456789abcdef0123456789abcdef")
	paired.SealedConfiguration = "initial-envelope"
	for _, node := range []ManagedNode{aws, paired} {
		if err := repo.SaveNode(ctx, node); err != nil {
			t.Fatal(err)
		}
	}
	for _, url := range []string{"https://second.example:8443", "https://third.example:8443"} {
		if _, err := repo.StageClientEndpoints(ctx, url); err != nil {
			t.Fatal(err)
		}
	}
	nodes, err := repo.Nodes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var queue []EndpointUpdate
	if err = json.Unmarshal([]byte(nodes[0].PendingEndpointUpdates), &queue); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(queue, []EndpointUpdate{{Generation: 2, RelayURL: "https://second.example:8443"}, {Generation: 3, RelayURL: "https://third.example:8443"}}) {
		t.Fatalf("queue=%+v", queue)
	}
	if nodes[1].ConfigurationGeneration != 3 || nodes[1].SealedConfiguration != "initial-envelope" || nodes[1].Health != "configuring" {
		t.Fatal("initial paired delivery changed during rotation")
	}
	repeated, err := repo.StageClientEndpoints(ctx, "https://third.example:8443")
	if err != nil || !reflect.DeepEqual(nodes, repeated) {
		t.Fatal("same endpoint reconciliation changed generations")
	}
	if nodes[0].ClientSerial != aws.ClientSerial || nodes[0].SOCKSPassword != aws.SOCKSPassword {
		t.Fatal("endpoint staging changed credentials")
	}
}

type queuedEndpointRunner struct {
	private        []byte
	applied        uint64
	configurations []nodeservice.Configuration
	loseResponseAt uint64
}

func (runner *queuedEndpointRunner) RunPowerShell(_ context.Context, _ string, script string) (string, error) {
	_, encoded, ok := strings.Cut(script, "FromBase64String('")
	if !ok {
		return "", errors.New("no sealed config")
	}
	encoded, _, _ = strings.Cut(encoded, "')")
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	var envelope sealedconfig.Envelope
	if err = json.Unmarshal(raw, &envelope); err != nil {
		return "", err
	}
	plaintext, err := sealedconfig.Open(runner.private, envelope)
	if err != nil {
		return "", err
	}
	var config nodeservice.Configuration
	if err = json.Unmarshal(plaintext, &config); err != nil {
		return "", err
	}
	runner.configurations = append(runner.configurations, config)
	if config.Generation != runner.applied && config.Generation != runner.applied+1 {
		return "", errors.New("legacy node rejected skipped generation")
	}
	runner.applied = config.Generation
	if runner.loseResponseAt == config.Generation {
		runner.loseResponseAt = 0
		return "", errors.New("response lost")
	}
	return `{"configured":true}`, nil
}

func TestAWSQueuedEndpointReplayRecoversLostRemoteAndLocalResponses(t *testing.T) {
	for _, failure := range []string{"remote-response", "local-save"} {
		t.Run(failure, func(t *testing.T) {
			ctx := context.Background()
			private, public, err := sealedconfig.GenerateKey()
			if err != nil {
				t.Fatal(err)
			}
			node := normalizeManagedNode(testManagedNode("i-0123456789abcdef0"))
			node.ConfigurationPublicKey = public
			node.ConfigurationGeneration = 3
			node.RelayURL = "https://third.example:8443"
			node.Health = "configuring"
			node.AppliedGeneration = 1
			raw, _ := json.Marshal([]EndpointUpdate{{Generation: 2, RelayURL: "https://second.example:8443"}, {Generation: 3, RelayURL: node.RelayURL}})
			node.PendingEndpointUpdates = string(raw)
			store := &memoryNodeStore{saved: node}
			runner := &queuedEndpointRunner{private: private, applied: 1}
			if failure == "remote-response" {
				runner.loseResponseAt = 2
			} else {
				store.failAt = 1
			}
			orchestrator := NewOrchestrator(runner, nil, store)
			if _, err = orchestrator.ReapplyConfiguration(ctx, node); err == nil {
				t.Fatal("interruption did not propagate")
			}
			store.failAt = 0
			completed, err := orchestrator.ReapplyConfiguration(ctx, store.saved)
			if err != nil {
				t.Fatal(err)
			}
			if runner.applied != 3 || completed.PendingEndpointUpdates != "" || completed.AppliedGeneration != 3 || completed.Health != "installed" {
				t.Fatalf("incomplete replay: %+v", completed)
			}
			generations := make([]uint64, len(runner.configurations))
			for i, configuration := range runner.configurations {
				generations[i] = configuration.Generation
				if configuration.Serial != node.ClientSerial || configuration.SOCKSPassword != node.SOCKSPassword {
					t.Fatal("replay replaced Client secrets")
				}
			}
			if !reflect.DeepEqual(generations, []uint64{2, 2, 3}) {
				t.Fatalf("replayed generations %v", generations)
			}
			if runner.configurations[0] != runner.configurations[1] {
				t.Fatal("retry changed current generation content")
			}
		})
	}
}

type endpointFailureStore struct {
	securestore.Store
	fail bool
}

func (store *endpointFailureStore) Put(ctx context.Context, key string, value []byte) error {
	if store.fail {
		return errors.New("storage unavailable")
	}
	return store.Store.Put(ctx, key, value)
}

func TestStageClientEndpointsCommitsEntireFleetOrNothing(t *testing.T) {
	ctx := context.Background()
	storage := &endpointFailureStore{Store: securestore.NewMemoryStore()}
	repo := NewRepository(storage)
	for _, node := range []ManagedNode{testManagedNode("i-0123456789abcdef0"), testPairedNode("paired-0123456789abcdef0123456789abcdef")} {
		if err := repo.SaveNode(ctx, node); err != nil {
			t.Fatal(err)
		}
	}
	before, err := repo.Nodes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	storage.fail = true
	if _, err = repo.StageClientEndpoints(ctx, "https://new.example:8443"); err == nil {
		t.Fatal("failed fleet persistence succeeded")
	}
	after, err := NewRepository(storage).Nodes(ctx)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("partial fleet endpoint mutation escaped failed atomic save")
	}
}

func TestStageClientEndpointsSeedsUnacknowledgedLegacyGeneration(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(securestore.NewMemoryStore())
	node := testManagedNode("i-0123456789abcdef0")
	node.ConfigurationGeneration = 4
	node.Health = "configuring"
	if err := repo.SaveNode(ctx, node); err != nil {
		t.Fatal(err)
	}
	staged, err := repo.StageClientEndpoints(ctx, "https://new.example:8443")
	if err != nil {
		t.Fatal(err)
	}
	var queue []EndpointUpdate
	if err = json.Unmarshal([]byte(staged[0].PendingEndpointUpdates), &queue); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(queue, []EndpointUpdate{{Generation: 4, RelayURL: node.RelayURL}, {Generation: 5, RelayURL: "https://new.example:8443"}}) {
		t.Fatalf("legacy pending configuration was lost: %+v", queue)
	}
}

func TestEndpointQueueRejectsMissingGenerationsAndPrivateFields(t *testing.T) {
	node := normalizeManagedNode(testManagedNode("i-0123456789abcdef0"))
	node.ConfigurationGeneration = 4
	node.AppliedGeneration = 1
	node.Health = "configuring"
	for _, queue := range []string{
		`[{"generation":3,"relayUrl":"https://middle.example:8443"},{"generation":4,"relayUrl":"https://bridge.tail123.ts.net:8443"}]`,
		`[{"generation":2,"relayUrl":"https://bridge.tail123.ts.net:8443","password":"secret"}]`,
		`[{"generation":2,"relayUrl":"http://bad.example"},{"generation":3,"relayUrl":"https://three.example"},{"generation":4,"relayUrl":"https://bridge.tail123.ts.net:8443"}]`,
		strings.Repeat(" ", maximumEndpointQueueBytes+1),
	} {
		node.PendingEndpointUpdates = queue
		if err := validateManagedNode(node); err == nil {
			t.Fatal("invalid endpoint queue accepted")
		}
	}
}
