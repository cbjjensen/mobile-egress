package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"mobile-egress/internal/clientcontrol"
	"mobile-egress/windows-client/internal/client"
	"mobile-egress/windows-client/internal/cloud"
	"mobile-egress/windows-client/internal/localbridge"
	"mobile-egress/windows-client/internal/nodeservice"
	"mobile-egress/windows-client/internal/relayclient"
	"mobile-egress/windows-client/internal/sealedconfig"
	"mobile-egress/windows-client/internal/securestore"
)

func TestPairingPersistsConfigurationBeforeDeliveryAndWaitsForReceipt(t *testing.T) {
	ctx := context.Background()
	repo := cloud.NewRepository(securestore.NewMemoryStore())
	id := "paired-0123456789abcdef0123456789abcdef"
	if err := repo.ReserveClient(ctx, id, "Mac"); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveClientInvitation(ctx, cloud.PendingClient{NodeID: id, DisplayName: "Mac", EnrollmentID: "enrollment", Invitation: "encoded", ExpiresAt: time.Now().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	_, public, err := sealedconfig.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	e := clientcontrol.Enrollment{ID: "enrollment", NodeID: id, DisplayName: "Mac", State: "approved", Bootstrap: &clientcontrol.Bootstrap{ConfigurationPublicKey: public, Platform: "macos", Architecture: "arm64", ServiceVersion: "1.2.3"}, ClientSerial: "ABC", CertificatePEM: "certificate", CACertificatePEM: "ca"}
	status := []clientcontrol.Status{}
	deliveries := 0
	control := &pairedControlAPI{
		List: func(context.Context, relayclient.Identity) ([]clientcontrol.Enrollment, error) {
			return []clientcontrol.Enrollment{e}, nil
		},
		Approve: func(context.Context, relayclient.Identity, string) (clientcontrol.Enrollment, error) { return e, nil },
		Deliver: func(_ context.Context, _ relayclient.Identity, _ string, _ uint64, envelope sealedconfig.Envelope) error {
			nodes, err := repo.Nodes(ctx)
			if err != nil || len(nodes) != 1 || nodes[0].Health != "configuring" {
				t.Fatal("delivered before durable metadata")
			}
			deliveries++
			return nil
		},
		Statuses: func(context.Context, relayclient.Identity) ([]clientcontrol.Status, error) { return status, nil },
	}
	app := &DesktopApp{cloudRepository: repo, pairedAPI: control}
	owner := relayclient.Identity{RelayURL: "https://bridge.example:8443"}
	if err := app.syncPairedClients(ctx, owner); err != nil {
		t.Fatal(err)
	}
	if deliveries != 1 {
		t.Fatal("configuration was not delivered")
	}
	nodes, _ := repo.Nodes(ctx)
	original := nodes[0]
	if original.Health != "configuring" || original.AppliedGeneration != 0 {
		t.Fatal("unacknowledged install marked complete")
	}
	// A lost delivery response must reuse exactly the retained credentials/key/generation.
	if err := app.syncPairedClients(ctx, owner); err != nil {
		t.Fatal(err)
	}
	nodes, _ = repo.Nodes(ctx)
	if nodes[0] != original {
		t.Fatal("retry changed configuration")
	}
	status = []clientcontrol.Status{{NodeID: id, EnrollmentID: e.ID, ClientSerial: e.ClientSerial, AppliedGeneration: 1, ServiceVersion: "1.2.3", Connected: true}}
	if err := app.syncPairedClients(ctx, owner); err != nil {
		t.Fatal(err)
	}
	nodes, _ = repo.Nodes(ctx)
	pending, _ := repo.PendingClients(ctx)
	if nodes[0].Health != "installed" || nodes[0].AppliedGeneration != 1 || len(pending) != 0 {
		t.Fatal("receipt did not complete installation")
	}
}

func TestPairingDeliveryFailureRetainsRecoverableMetadata(t *testing.T) {
	ctx := context.Background()
	repo := cloud.NewRepository(securestore.NewMemoryStore())
	node := pairedNodeForDesktopTest(t)
	if err := repo.SaveNode(ctx, node); err != nil {
		t.Fatal(err)
	}
	app := &DesktopApp{cloudRepository: repo, pairedAPI: &pairedControlAPI{Deliver: func(context.Context, relayclient.Identity, string, uint64, sealedconfig.Envelope) error {
		return errors.New("secret remote failure")
	}}}
	if err := app.deliverPairedConfiguration(ctx, relayclient.Identity{}, node); err == nil {
		t.Fatal("expected failure")
	}
	stored, _ := repo.Nodes(ctx)
	if len(stored) != 1 || stored[0] != node {
		t.Fatal("failed delivery lost metadata")
	}
}

func TestEndpointExportKeepsIdentityAndCredentials(t *testing.T) {
	ctx := context.Background()
	repo := cloud.NewRepository(securestore.NewMemoryStore())
	node := pairedNodeForDesktopTest(t)
	private, public, err := sealedconfig.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	node.ConfigurationPublicKey = public
	initial, err := sealPairedConfiguration(node)
	if err != nil {
		t.Fatal(err)
	}
	initialJSON, _ := json.Marshal(initial)
	node.SealedConfiguration = string(initialJSON)
	node.ConfigurationGeneration = 4
	node.AppliedGeneration = 1
	node.RelayURL = "https://new.example:8443"
	if err := repo.SaveNode(ctx, node); err != nil {
		t.Fatal(err)
	}
	app := &DesktopApp{cloudRepository: repo}
	encoded, err := app.ExportClientEndpointUpdate(node.NodeID)
	if err != nil {
		t.Fatal(err)
	}
	update, err := clientcontrol.DecodeEndpointUpdate(encoded)
	if err != nil {
		t.Fatal(err)
	}
	var envelope sealedconfig.Envelope
	if err := json.Unmarshal(update.Envelope, &envelope); err != nil {
		t.Fatal(err)
	}
	if update.NodeID != node.NodeID || update.EnrollmentID != node.EnrollmentID || envelope.Version != 1 {
		t.Fatal("wrong endpoint update target")
	}
	plaintext, err := sealedconfig.Open(private, envelope)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(plaintext)
	var configuration nodeservice.Configuration
	if err := json.Unmarshal(plaintext, &configuration); err != nil {
		t.Fatal(err)
	}
	if configuration != pairedConfiguration(node) {
		t.Fatal("export did not seal the latest endpoint with unchanged credentials")
	}
	stored, _ := repo.Nodes(ctx)
	if stored[0] != node {
		t.Fatal("export changed credentials or endpoint generation")
	}
}

type endpointBridgeFake struct {
	desktopBridgeSpy
	owner relayclient.Identity
}

func (bridge *endpointBridgeFake) Rotate(context.Context, relayclient.Identity) (localbridge.BridgeStatus, relayclient.Identity, error) {
	bridge.rotateCalls++
	return localbridge.BridgeStatus{}, bridge.owner, nil
}

func TestRotationPersistsDesiredEndpointWithoutAWSBeforeMigrationQR(t *testing.T) {
	ctx := context.Background()
	store := securestore.NewMemoryStore()
	owner := relayclient.Identity{RelayURL: "https://old.example:8443", Role: "owner", Serial: "ABC", PrivateKeyPEM: "key", CertificatePEM: "certificate", CACertificatePEM: "ca"}
	owners := client.NewRepository(store)
	if err := owners.SaveOwnerIdentity(ctx, owner); err != nil {
		t.Fatal(err)
	}
	repo := cloud.NewRepository(store)
	node := pairedNodeForDesktopTest(t)
	node.Health = "installed"
	node.AppliedGeneration = 1
	if err := repo.SaveNode(ctx, node); err != nil {
		t.Fatal(err)
	}
	owner.RelayURL = "https://new.example:8443"
	bridge := &endpointBridgeFake{owner: owner}
	app := &DesktopApp{ownerRepository: owners, cloudRepository: repo, bridge: bridge}
	_, err := app.RotateLocalBridge()
	// The deliberately invalid test Owner cannot issue a QR. Desired Client state
	// must already be saved so this independent failure cannot strand the fleet.
	if err == nil || !strings.Contains(err.Error(), "QR") {
		t.Fatalf("expected later QR failure, got %v", err)
	}
	if bridge.rotateCalls != 1 {
		t.Fatal("AWS blocked rotation")
	}
	nodes, err := repo.Nodes(ctx)
	if err != nil || len(nodes) != 1 || nodes[0].RelayURL != owner.RelayURL || nodes[0].ConfigurationGeneration != 2 || nodes[0].Health != "configuring" {
		t.Fatalf("desired endpoint lost: %#v, %v", nodes, err)
	}
}

func TestPairingCanAcknowledgeLatestEndpointAfterMissingInitialReceipt(t *testing.T) {
	ctx := context.Background()
	repo := cloud.NewRepository(securestore.NewMemoryStore())
	node := pairedNodeForDesktopTest(t)
	initial, err := sealPairedConfiguration(node)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(initial)
	node.SealedConfiguration = string(raw)
	if err := repo.ReserveClient(ctx, node.NodeID, node.DisplayName); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveClientInvitation(ctx, cloud.PendingClient{NodeID: node.NodeID, DisplayName: node.DisplayName, EnrollmentID: node.EnrollmentID, Invitation: "encoded", ExpiresAt: time.Now().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveNode(ctx, node); err != nil {
		t.Fatal(err)
	}
	for _, origin := range []string{"https://second.example:8443", "https://third.example:8443"} {
		if _, err := repo.StageClientEndpoints(ctx, origin); err != nil {
			t.Fatal(err)
		}
	}
	app := &DesktopApp{cloudRepository: repo, pairedAPI: &pairedControlAPI{
		List: func(context.Context, relayclient.Identity) ([]clientcontrol.Enrollment, error) {
			return []clientcontrol.Enrollment{{ID: node.EnrollmentID, NodeID: node.NodeID, State: "delivered"}}, nil
		},
		Deliver: func(_ context.Context, _ relayclient.Identity, _ string, generation uint64, envelope sealedconfig.Envelope) error {
			got, _ := json.Marshal(envelope)
			if generation != 1 || string(got) != node.SealedConfiguration {
				t.Fatal("rotation replaced original enrollment delivery")
			}
			return nil
		},
		Statuses: func(context.Context, relayclient.Identity) ([]clientcontrol.Status, error) {
			return []clientcontrol.Status{{NodeID: node.NodeID, EnrollmentID: node.EnrollmentID, ClientSerial: node.ClientSerial, AppliedGeneration: 3}}, nil
		},
	}}
	if err := app.syncPairedClients(ctx, relayclient.Identity{}); err != nil {
		t.Fatal(err)
	}
	nodes, _ := repo.Nodes(ctx)
	pending, _ := repo.PendingClients(ctx)
	if len(pending) != 0 || nodes[0].Health != "installed" || nodes[0].AppliedGeneration != 3 || nodes[0].SealedConfiguration != "" || nodes[0].SOCKSPassword != node.SOCKSPassword {
		t.Fatal("latest receipt did not finish pairing with original credentials")
	}
}

func TestPairedStatusProxyAndRevocationRemainAvailableDuringAWSOperation(t *testing.T) {
	ctx := context.Background()
	store := securestore.NewMemoryStore()
	owners := client.NewRepository(store)
	owner := relayclient.Identity{RelayURL: "https://bridge.example:8443", Role: "owner", Serial: "OWNER", PrivateKeyPEM: "owner-secret-key", CertificatePEM: "owner-certificate", CACertificatePEM: "ca"}
	if err := owners.SaveOwnerIdentity(ctx, owner); err != nil {
		t.Fatal(err)
	}
	repo := cloud.NewRepository(store)
	node := pairedNodeForDesktopTest(t)
	node.Health = "installed"
	node.AppliedGeneration = 1
	if err := repo.SaveNode(ctx, node); err != nil {
		t.Fatal(err)
	}
	app := &DesktopApp{ownerRepository: owners, cloudRepository: repo, pairedAPI: &pairedControlAPI{
		Statuses: func(context.Context, relayclient.Identity) ([]clientcontrol.Status, error) {
			return []clientcontrol.Status{{NodeID: node.NodeID, EnrollmentID: node.EnrollmentID, ClientSerial: node.ClientSerial, AppliedGeneration: 1, Connected: true}}, nil
		},
		Revoke: func(context.Context, relayclient.Identity, string) error { return nil },
	}}
	app.provisioning.Lock() // Simulate an unrelated blocked AWS/SSM operation.
	defer app.provisioning.Unlock()
	done := make(chan error, 1)
	go func() {
		fleet, err := app.RefreshClients()
		if err != nil {
			done <- err
			return
		}
		if len(fleet.Nodes) != 1 || !fleet.Nodes[0].Connected || !fleet.Nodes[0].ConnectionKnown {
			done <- errors.New("standalone live status unavailable")
			return
		}
		raw, _ := json.Marshal(fleet)
		for _, secret := range []string{node.SOCKSPassword, node.CertificatePEM, owner.PrivateKeyPEM, node.ConfigurationPublicKey} {
			if strings.Contains(string(raw), secret) {
				done <- errors.New("fleet status exposed private material")
				return
			}
		}
		proxy, err := app.ClientProxyLine(node.NodeID)
		if err != nil || proxy != "127.0.0.1:1081:user:password" {
			done <- errors.New("standalone proxy unavailable")
			return
		}
		done <- app.RevokeClient(node.NodeID)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("AWS operation blocked paired Client control")
	}
}

func pairedNodeForDesktopTest(t *testing.T) cloud.ManagedNode {
	t.Helper()
	_, public, err := sealedconfig.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	return cloud.ManagedNode{NodeID: "paired-0123456789abcdef0123456789abcdef", DisplayName: "Mac", Platform: "macos", Architecture: "arm64", Management: cloud.ManagementPaired, EnrollmentID: "enrollment", ClientSerial: "ABC", ConfigurationPublicKey: public, ConfigurationGeneration: 1, ServiceVersion: "1.2.3", Health: "configuring", SOCKSUsername: "user", SOCKSPassword: "password", SOCKSPort: 1080, RelayURL: "https://bridge.example:8443", CertificatePEM: "certificate", CACertificatePEM: "ca"}
}
