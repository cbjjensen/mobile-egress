package nodeservice

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"mobile-egress/internal/clientcontrol"
	"mobile-egress/windows-client/internal/relayclient"
	"mobile-egress/windows-client/internal/sealedconfig"
	"mobile-egress/windows-client/internal/securestore"
)

type offlineEnrollment struct{}

func (remote offlineEnrollment) Poll(ctx context.Context, invitation clientcontrol.Invitation, bootstrap clientcontrol.Bootstrap) (clientcontrol.Enrollment, error) {
	return remote.Submit(ctx, invitation, bootstrap)
}

func (offlineEnrollment) Submit(context.Context, clientcontrol.Invitation, clientcontrol.Bootstrap) (clientcontrol.Enrollment, error) {
	return clientcontrol.Enrollment{}, errors.New("secret-request-content")
}

type preparedEnrollment struct {
	t             *testing.T
	repository    *Repository
	store         securestore.Store
	configuration Configuration
	manager       *Standalone
	reject        bool
}

type writeFailingStore struct {
	securestore.Store
	fail bool
}

func (store *writeFailingStore) Put(ctx context.Context, key string, value []byte) error {
	if store.fail {
		return errors.New("storage unavailable")
	}
	return store.Store.Put(ctx, key, value)
}

func TestStandaloneKeepsRevocationFailClosedWhenItsReceiptCannotBeSaved(t *testing.T) {
	ctx := context.Background()
	store := &writeFailingStore{Store: securestore.NewMemoryStore()}
	repository := NewRepository(store)
	bootstrap, _ := repository.Bootstrap(ctx)
	config := signedNodeConfig(t, bootstrap.CSRPEM, "https://relay.example.ts.net:8443", "user", "password")
	_ = repository.BeginPairing(ctx, clientcontrol.Invitation{ID: "enrollment", NodeID: "node", RelayURL: config.RelayURL, CACertificatePEM: config.CACertificatePEM, ExpiresAt: time.Now().Add(time.Minute)})
	remote := &preparedEnrollment{t: t, repository: repository, store: store, configuration: config}
	manager := NewStandalone(repository, &fakeDialer{results: []dialResult{{tunnel: &fakeTunnel{healthy: true}}}}, remote, "windows", "amd64", "test")
	remote.manager = manager
	defer manager.stopService()
	manager.step(ctx)
	deadline := time.Now().Add(time.Second)
	for !manager.Status().Connected {
		if time.Now().After(deadline) {
			t.Fatal("service did not connect")
		}
		time.Sleep(time.Millisecond)
	}
	store.fail = true
	remote.reject = true
	manager.step(ctx)
	manager.step(ctx)
	if manager.Status().Phase != "revoked" || manager.Status().Running {
		t.Fatal("failed revocation write reopened proxy service")
	}
	if _, err := manager.Proxy(ctx, "http"); err == nil {
		t.Fatal("failed revocation write exposed credentials")
	}
}

func (remote *preparedEnrollment) Poll(ctx context.Context, invitation clientcontrol.Invitation, bootstrap clientcontrol.Bootstrap) (clientcontrol.Enrollment, error) {
	return remote.Submit(ctx, invitation, bootstrap)
}
func (remote *preparedEnrollment) Submit(ctx context.Context, invitation clientcontrol.Invitation, bootstrap clientcontrol.Bootstrap) (clientcontrol.Enrollment, error) {
	persisted, err := NewRepository(remote.store).Bootstrap(ctx)
	if err != nil || persisted.CSRPEM != bootstrap.CSRPEM || persisted.ConfigurationPublicKey != bootstrap.ConfigurationPublicKey {
		remote.t.Fatal("public bootstrap sent before private state persisted")
	}
	raw, _ := json.Marshal(remote.configuration)
	envelope, _ := sealedconfig.Seal(bootstrap.ConfigurationPublicKey, raw)
	sealed, _ := json.Marshal(envelope)
	return clientcontrol.Enrollment{ID: invitation.ID, NodeID: invitation.NodeID, State: "delivered", Configuration: &clientcontrol.ConfigurationDelivery{Generation: 1, Envelope: sealed}}, nil
}
func (remote *preparedEnrollment) Report(ctx context.Context, identity relayclient.Identity, id string, generation uint64, version string) error {
	durable, err := NewRepository(remote.store).Runtime(ctx)
	if err != nil || durable.Generation != generation || durable.Identity.Serial != identity.Serial {
		remote.t.Fatal("acknowledged before durable configuration")
	}
	if !remote.manager.Status().Running || !remote.manager.Status().Connected {
		remote.t.Fatal("acknowledged before proxies and tunnel started")
	}
	if remote.reject {
		return &relayclient.ClientControlError{StatusCode: 403, Code: "revoked"}
	}
	return nil
}
func TestStandaloneAcknowledgesAfterDurableStateAndListenersThenRevokes(t *testing.T) {
	ctx := context.Background()
	store := securestore.NewMemoryStore()
	repository := NewRepository(store)
	bootstrap, _ := repository.Bootstrap(ctx)
	config := signedNodeConfig(t, bootstrap.CSRPEM, "https://relay.example.ts.net:8443", "user", "password")
	invitation := clientcontrol.Invitation{ID: "enrollment", NodeID: "node", RelayURL: config.RelayURL, CACertificatePEM: config.CACertificatePEM, ExpiresAt: time.Now().Add(time.Minute)}
	if err := repository.BeginPairing(ctx, invitation); err != nil {
		t.Fatal(err)
	}
	remote := &preparedEnrollment{t: t, repository: repository, store: store, configuration: config}
	manager := NewStandalone(repository, &fakeDialer{results: []dialResult{{tunnel: &fakeTunnel{healthy: true}}}}, remote, "windows", "amd64", "test")
	remote.manager = manager
	defer manager.stopService()
	manager.step(ctx)
	deadline := time.Now().Add(2 * time.Second)
	for !manager.Status().Connected {
		if time.Now().After(deadline) {
			t.Fatal("service did not connect")
		}
		time.Sleep(time.Millisecond)
	}
	manager.step(ctx)
	pairing, _ := repository.Pairing(ctx)
	if pairing.AcknowledgedGeneration != 1 || pairing.Invitation != nil || manager.Status().Phase != "ready" {
		t.Fatal("configuration not durably acknowledged")
	}
	remote.reject = true
	manager.lastReport = time.Time{}
	manager.step(ctx)
	if manager.Status().Running || manager.Status().Phase != "revoked" {
		t.Fatal("revocation did not close listeners")
	}
	manager.step(ctx)
	if manager.Status().Phase != "revoked" {
		t.Fatal("revoked service restarted")
	}
	pairing, _ = NewRepository(store).Pairing(ctx)
	if !pairing.Revoked {
		t.Fatal("revocation not persisted")
	}
	if _, err := manager.Proxy(ctx, "http"); err == nil {
		t.Fatal("revoked Client exposed proxy credentials")
	}
}
func (offlineEnrollment) Report(context.Context, relayclient.Identity, string, uint64, string) error {
	return errors.New("secret-request-content")
}

func TestStandaloneWaitsWithoutConfigurationAndStatusContainsNoSecrets(t *testing.T) {
	manager := NewStandalone(NewRepository(securestore.NewMemoryStore()), &fakeDialer{}, offlineEnrollment{}, "windows", "amd64", "test")
	if err := manager.repositoryBootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	manager.step(context.Background())
	status := manager.Status()
	if status.Phase != "waiting" || status.Running {
		t.Fatalf("unconfigured status: %#v", status)
	}
	raw, _ := json.Marshal(status)
	for _, forbidden := range []string{"private", "password", "capability", "certificate", "secret-request-content"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("unsafe status: %s", raw)
		}
	}
	if _, err := manager.Proxy(context.Background(), "http"); err == nil {
		t.Fatal("proxy credentials available before pairing")
	}
}

func TestStandaloneRejectsUnsupportedCopyKindAndInvalidInvitationWithoutEcho(t *testing.T) {
	manager := NewStandalone(NewRepository(securestore.NewMemoryStore()), &fakeDialer{}, offlineEnrollment{}, "windows", "amd64", "test")
	err := manager.Pair(context.Background(), "secret-invitation-marker")
	if err == nil || strings.Contains(err.Error(), "secret-invitation-marker") {
		t.Fatalf("unsafe pairing error: %v", err)
	}
	if _, err := manager.Proxy(context.Background(), "private-key"); err == nil {
		t.Fatal("arbitrary secret copy supported")
	}
}

func TestStandaloneImportBindsUpdateToClientAndPreservesProxyCredentials(t *testing.T) {
	ctx := context.Background()
	repository := NewRepository(securestore.NewMemoryStore())
	bootstrap, _ := repository.Bootstrap(ctx)
	config := signedNodeConfig(t, bootstrap.CSRPEM, "https://old.example.ts.net:8443", "user", "password")
	if err := repository.BeginPairing(ctx, clientcontrol.Invitation{ID: "enrollment", NodeID: "node", RelayURL: config.RelayURL, CACertificatePEM: config.CACertificatePEM}); err != nil {
		t.Fatal(err)
	}
	seal := func(value Configuration) []byte {
		raw, _ := json.Marshal(value)
		envelope, _ := sealedconfig.Seal(bootstrap.ConfigurationPublicKey, raw)
		encoded, _ := json.Marshal(envelope)
		return encoded
	}
	first, _ := decodeEnvelope(seal(config))
	if err := repository.ApplyPaired(ctx, first); err != nil {
		t.Fatal(err)
	}
	manager := NewStandalone(repository, &fakeDialer{}, offlineEnrollment{}, "windows", "amd64", "test")
	before, err := manager.Proxy(ctx, "http")
	if err != nil {
		t.Fatal(err)
	}
	config.Generation = 4
	config.RelayURL = "https://new.example.ts.net:8443"
	update := clientcontrol.EndpointUpdate{Version: 1, Type: clientcontrol.EndpointUpdateType, NodeID: "different-node", EnrollmentID: "enrollment", Envelope: seal(config)}
	wrong, _ := clientcontrol.EncodeEndpointUpdate(update)
	if err := manager.Import(ctx, wrong); err == nil {
		t.Fatal("accepted update for another Client ID")
	}
	update.NodeID = "node"
	bundle, _ := clientcontrol.EncodeEndpointUpdate(update)
	if err := manager.Import(ctx, bundle); err != nil {
		t.Fatal(err)
	}
	after, _ := manager.Proxy(ctx, "http")
	runtime, _ := repository.Runtime(ctx)
	if before != after || runtime.Generation != 4 || runtime.Identity.RelayURL != config.RelayURL {
		t.Fatal("import changed credentials or failed to apply endpoint")
	}
	if manager.Status().Phase != "connecting" {
		t.Fatal("import was marked complete before acknowledgement")
	}
}
