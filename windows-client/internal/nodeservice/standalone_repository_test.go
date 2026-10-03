package nodeservice

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"mobile-egress/internal/clientcontrol"
	"mobile-egress/windows-client/internal/sealedconfig"
	"mobile-egress/windows-client/internal/securestore"
)

func TestPairedUpdatesAllowMissedGenerationsWithoutReplacingSecrets(t *testing.T) {
	ctx := context.Background()
	store := securestore.NewMemoryStore()
	repository := NewRepository(store)
	bootstrap, err := repository.Bootstrap(ctx)
	if err != nil {
		t.Fatal(err)
	}
	config := signedNodeConfig(t, bootstrap.CSRPEM, "https://relay.example.ts.net:8443", "user", "password")
	invitation := clientcontrol.Invitation{Version: 1, Type: "client-enrollment", ID: "enrollment", NodeID: "paired-node", RelayURL: config.RelayURL, CACertificatePEM: config.CACertificatePEM, Capability: "secret", ExpiresAt: time.Now().Add(time.Minute)}
	if err := repository.BeginPairing(ctx, invitation); err != nil {
		t.Fatal(err)
	}
	seal := func(c Configuration) sealedconfig.Envelope {
		raw, _ := json.Marshal(c)
		e, err := sealedconfig.Seal(bootstrap.ConfigurationPublicKey, raw)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	first := seal(config)
	if err := repository.ApplyPaired(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := repository.ApplyPaired(ctx, first); err != nil {
		t.Fatalf("durable delivery retry: %v", err)
	}
	repository = NewRepository(store)
	next := config
	next.Generation = 5
	next.RelayURL = "https://new.example.ts.net:8443"
	if err := repository.ApplyPaired(ctx, seal(next)); err != nil {
		t.Fatalf("missed generations: %v", err)
	}
	changed := next
	changed.Generation = 6
	changed.SOCKSPassword = "replacement"
	if err := repository.ApplyPaired(ctx, seal(changed)); err == nil {
		t.Fatal("replaced credentials")
	}
	if err := repository.ApplyPaired(ctx, first); err == nil {
		t.Fatal("accepted stale configuration")
	}
	runtime, err := repository.Runtime(ctx)
	if err != nil || runtime.Generation != 5 || runtime.Password != "password" {
		t.Fatalf("runtime generation or credentials changed: %v", err)
	}
	pairing, err := repository.Pairing(ctx)
	if err != nil || pairing.NodeID != invitation.NodeID {
		t.Fatalf("pairing did not survive restart: %v", err)
	}
}

func TestLegacyApplyStillRejectsSkippedGenerations(t *testing.T) {
	repository := NewRepository(securestore.NewMemoryStore())
	bootstrap, _ := repository.Bootstrap(context.Background())
	config := signedNodeConfig(t, bootstrap.CSRPEM, "https://relay.example.ts.net:8443", "user", "password")
	applyNodeConfig(t, repository, bootstrap.ConfigurationPublicKey, config)
	config.Generation = 3
	raw, _ := json.Marshal(config)
	envelope, _ := sealedconfig.Seal(bootstrap.ConfigurationPublicKey, raw)
	if err := repository.Apply(context.Background(), envelope); err == nil {
		t.Fatal("legacy AWS update skipped generations")
	}
	if err := repository.ApplyPaired(context.Background(), envelope); err == nil {
		t.Fatal("legacy AWS node accepted paired update")
	}
}

func TestInterruptedPairingPreservesOriginalBootstrapAcrossServiceUpgrade(t *testing.T) {
	ctx := context.Background()
	store := securestore.NewMemoryStore()
	repository := NewRepository(store)
	if err := repository.BeginPairing(ctx, clientcontrol.Invitation{ID: "enrollment", NodeID: "node"}); err != nil {
		t.Fatal(err)
	}
	first, err := repository.PairingBootstrap(ctx, "windows", "amd64", "1.0")
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.BeginPairing(ctx, clientcontrol.Invitation{ID: "enrollment", NodeID: "node"}); err != nil {
		t.Fatal(err)
	}
	resumed, err := NewRepository(store).PairingBootstrap(ctx, "windows", "amd64", "1.1")
	if err != nil {
		t.Fatal(err)
	}
	if resumed != first || resumed.ServiceVersion != "1.0" {
		t.Fatal("upgrade changed bound public bootstrap")
	}
}

func TestPairingRejectsConfigurationOutsidePinnedCA(t *testing.T) {
	repository := NewRepository(securestore.NewMemoryStore())
	bootstrap, _ := repository.Bootstrap(context.Background())
	config := signedNodeConfig(t, bootstrap.CSRPEM, "https://relay.example.ts.net:8443", "user", "password")
	other := signedNodeConfig(t, bootstrap.CSRPEM, config.RelayURL, "user", "password")
	_ = repository.BeginPairing(context.Background(), clientcontrol.Invitation{ID: "id", NodeID: "node", RelayURL: config.RelayURL, CACertificatePEM: other.CACertificatePEM})
	raw, _ := json.Marshal(config)
	envelope, _ := sealedconfig.Seal(bootstrap.ConfigurationPublicKey, raw)
	if err := repository.ApplyPaired(context.Background(), envelope); err == nil {
		t.Fatal("accepted CA outside invitation")
	}
}

func TestPendingPairingCanMoveOriginWithoutChangingBoundKeysOrAuthority(t *testing.T) {
	ctx := context.Background()
	repository := NewRepository(securestore.NewMemoryStore())
	bootstrap, _ := repository.Bootstrap(ctx)
	config := signedNodeConfig(t, bootstrap.CSRPEM, "https://old.example.ts.net:8443", "user", "password")
	invitation := clientcontrol.Invitation{ID: "enrollment", NodeID: "node", RelayURL: config.RelayURL, CACertificatePEM: config.CACertificatePEM, Capability: "same-capability", ExpiresAt: time.Now().Add(time.Minute)}
	if err := repository.BeginPairing(ctx, invitation); err != nil {
		t.Fatal(err)
	}
	first, err := repository.PairingBootstrap(ctx, "windows", "amd64", "1.0")
	if err != nil {
		t.Fatal(err)
	}
	moved := invitation
	moved.RelayURL = "https://new.example.ts.net:8443"
	if err := repository.BeginPairing(ctx, moved); err != nil {
		t.Fatalf("same pinned invitation could not move: %v", err)
	}
	resumed, _ := repository.PairingBootstrap(ctx, "windows", "amd64", "1.1")
	if first != resumed {
		t.Fatal("origin move changed bound bootstrap")
	}
	changed := moved
	changed.Capability = "changed"
	if err := repository.BeginPairing(ctx, changed); err == nil {
		t.Fatal("accepted capability change")
	}
	changed = moved
	changed.RelayURL = "http://insecure.example.test"
	if err := repository.BeginPairing(ctx, changed); err == nil {
		t.Fatal("accepted insecure invitation origin")
	}
	raw, _ := json.Marshal(config)
	envelope, _ := sealedconfig.Seal(bootstrap.ConfigurationPublicKey, raw)
	if err := repository.ApplyPaired(ctx, envelope); err != nil {
		t.Fatalf("original pinned configuration could not be recovered for subsequent endpoint import: %v", err)
	}
	current, _ := repository.Runtime(ctx)
	if current.Identity.RelayURL != config.RelayURL {
		t.Fatal("invitation re-paste silently rewrote sealed endpoint")
	}
}
