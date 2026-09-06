package desktop

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"mobile-egress/windows-client/internal/client"
	"mobile-egress/windows-client/internal/relayclient"
	"mobile-egress/windows-client/internal/securestore"
	"mobile-egress/windows-client/internal/tailscale"
)

type snapshotCountingStore struct {
	securestore.Store
	reads atomic.Int32
}

func (s *snapshotCountingStore) Get(ctx context.Context, key string) ([]byte, error) {
	s.reads.Add(1)
	return s.Store.Get(ctx, key)
}

func TestWindowAndMenuSnapshotsPerformNoStorageOrIdentityReads(t *testing.T) {
	store := &snapshotCountingStore{Store: securestore.NewMemoryStore()}
	identity := relayclient.Identity{Role: "owner", Serial: "AA", RelayURL: "https://relay.example", PrivateKeyPEM: "secret-owner-key", CertificatePEM: "secret-owner-cert", CACertificatePEM: "secret-trust"}
	if err := client.NewRepository(store).SaveOwnerIdentity(context.Background(), identity); err != nil {
		t.Fatal(err)
	}
	app, err := newDesktopApp(context.Background(), desktopControllerConfig{Platform: platformWindows, Store: store, Gateway: contractGateway{}})
	if err != nil {
		t.Fatal(err)
	}
	// Owner polling must use Core even if the identity repository is unavailable.
	app.ownerRepository = nil
	store.reads.Store(0)
	app.startup(context.Background())
	defer app.shutdownApp()
	awaitMonitor(t, func() bool {
		s := app.GetControllerSnapshot()
		for _, c := range s.Components {
			if c.Checking {
				return false
			}
		}
		return true
	})
	if store.reads.Load() != 1 {
		t.Fatalf("collection made %d storage reads, want one metadata read", store.reads.Load())
	}
	var readers sync.WaitGroup
	for range 2 {
		readers.Go(func() {
			for range 100 {
				app.GetBridgeStatus()
				app.ManagedNodes()
				app.PendingEC2NodeReservations()
				app.GetControllerSnapshot()
			}
		})
	}
	readers.Wait()
	if store.reads.Load() != 1 {
		t.Fatal("display reads performed storage IO")
	}
	raw, err := json.Marshal(app.GetControllerSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "secret-") {
		t.Fatal("snapshot exposed identity material")
	}
}

func TestNodeInstallRechecksLiveBridgeDespiteCachedReadiness(t *testing.T) {
	m := newStatusMonitor(platformWindows, monitorChecks{})
	m.publish(componentHelper, componentResult{helper: relayServiceNotRequired}, nil)
	m.publish(componentTailscale, componentResult{tailscale: tailscale.Status{Online: true, FunnelReady: true, PublicURL: "https://relay.example"}}, nil)
	m.publish(componentRelay, componentResult{ownerReady: true, ownerURL: "https://relay.example", relayReady: true}, nil)
	app := &DesktopApp{monitor: m}
	if !app.GetBridgeStatus().Ready {
		t.Fatal("fixture is not display ready")
	}
	_, err := app.InstallEC2Node("i-0123456789abcdef0")
	if err == nil || !strings.Contains(err.Error(), "not currently ready") {
		t.Fatalf("cached status bypassed live preflight: %v", err)
	}
}
