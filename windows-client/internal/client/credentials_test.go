package client

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"mobile-egress/windows-client/internal/relayclient"
	"mobile-egress/windows-client/internal/securestore"
)

func TestRepositoryGeneratesAndPersistsThirtyTwoByteSOCKSCredentials(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := securestore.NewMemoryStore()
	repository := NewRepository(store)
	first, err := repository.LoadOrCreateCredentials(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"username": first.Username, "password": first.Password} {
		decoded, err := base64.RawURLEncoding.DecodeString(value)
		if err != nil {
			t.Fatalf("%s is not raw base64url: %v", name, err)
		}
		if len(decoded) != 32 {
			t.Fatalf("decoded %s length = %d, want 32", name, len(decoded))
		}
	}
	if first.Username == first.Password {
		t.Fatal("generated username and password are identical")
	}

	second, err := NewRepository(store).LoadOrCreateCredentials(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Fatalf("persisted credentials changed: %#v != %#v", second, first)
	}
}

func TestRepositoryPersistsCompleteIdentityGenerationThroughSecureStore(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := securestore.NewMemoryStore()
	repository := NewRepository(store)
	identity := relayclient.Identity{
		RelayURL: "https://relay.example:8443", Role: "client", Serial: "ABC123",
		PrivateKeyPEM: "private-key", CertificatePEM: "certificate-chain", CACertificatePEM: "relay-ca",
	}
	if err := repository.SaveIdentity(ctx, identity); err != nil {
		t.Fatal(err)
	}
	if err := repository.SavePort(ctx, 1080); err != nil {
		t.Fatal(err)
	}
	active, err := store.Get(ctx, activeGenerationKey)
	if err != nil {
		t.Fatal(err)
	}
	generation, err := store.Get(ctx, generationKey(string(active)))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"private-key", "certificate-chain", "relay-ca"} {
		if !strings.Contains(string(generation), required) {
			t.Fatalf("active secure generation is missing %q", required)
		}
	}
	loaded, port, err := NewRepository(store).LoadIdentity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if loaded != identity || port != 1080 {
		t.Fatalf("loaded identity/port = %#v/%d, want %#v/1080", loaded, port, identity)
	}
}

func TestRepositoryMigratesLegacySingleRoleIdentityIntoItsDedicatedSlot(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := securestore.NewMemoryStore()
	legacyOwner := relayclient.Identity{
		RelayURL: "https://relay.example:8443", Role: "owner", Serial: "OWNER123",
		PrivateKeyPEM: "owner-key", CertificatePEM: "owner-chain", CACertificatePEM: "relay-ca",
	}
	legacy := persistedGeneration{
		Credentials:   Credentials{Username: "username", Password: "password"},
		Settings:      persistedSettings{RelayURL: legacyOwner.RelayURL, Role: legacyOwner.Role, Serial: legacyOwner.Serial, Port: 1080},
		PrivateKeyPEM: legacyOwner.PrivateKeyPEM, CertificatePEM: legacyOwner.CertificatePEM, CACertificatePEM: legacyOwner.CACertificatePEM,
	}
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, generationKey("AAAAAAAAAAAAAAAAAAAAAA"), raw); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, activeGenerationKey, []byte("AAAAAAAAAAAAAAAAAAAAAA")); err != nil {
		t.Fatal(err)
	}

	repository := NewRepository(store)
	identities, ok := any(repository).(interface {
		LoadOwnerIdentity(context.Context) (relayclient.Identity, uint16, error)
		LoadClientIdentity(context.Context) (relayclient.Identity, uint16, error)
	})
	if !ok {
		t.Fatal("Repository does not expose independent owner and client identity loads")
	}
	got, _, err := identities.LoadOwnerIdentity(ctx)
	if err != nil || got != legacyOwner {
		t.Fatalf("migrated owner = %#v, %v; want %#v", got, err, legacyOwner)
	}
	migratedActive, err := store.Get(ctx, activeGenerationKey)
	if err != nil {
		t.Fatal(err)
	}
	migratedRaw, err := store.Get(ctx, generationKey(string(migratedActive)))
	if err != nil {
		t.Fatal(err)
	}
	var migrated persistedGeneration
	if err := json.Unmarshal(migratedRaw, &migrated); err != nil {
		t.Fatal(err)
	}
	if migrated.Owner == nil || migrated.Owner.Serial != legacyOwner.Serial || migrated.Client != nil || migrated.PrivateKeyPEM != "" || migrated.Settings.Role != "" {
		t.Fatalf("legacy generation was not rewritten as owner-only dual state: %#v", migrated)
	}
	if _, _, err := identities.LoadClientIdentity(ctx); !errors.Is(err, securestore.ErrNotFound) {
		t.Fatalf("migrated owner unexpectedly created a client: %v", err)
	}
}

func TestRepositoryPairingGenerationSwitchIsAtomicOnStoreFailure(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := &faultStore{Store: securestore.NewMemoryStore()}
	repository := NewRepository(store)
	credentials, err := repository.LoadOrCreateCredentials(ctx)
	if err != nil {
		t.Fatal(err)
	}
	first := relayclient.Identity{
		RelayURL: "https://one.example:8443", Role: "client", Serial: "AAA",
		PrivateKeyPEM: "first-key", CertificatePEM: "first-chain", CACertificatePEM: "first-ca",
	}
	if err := repository.SaveIdentity(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := relayclient.Identity{
		RelayURL: "https://two.example:8443", Role: "client", Serial: "BBB",
		PrivateKeyPEM: "second-key", CertificatePEM: "second-chain", CACertificatePEM: "second-ca",
	}
	store.fail(activeGenerationKey)
	if err := repository.SaveIdentity(ctx, second); err == nil {
		t.Fatal("SaveIdentity succeeded after the active-generation switch failed")
	}

	reloaded := NewRepository(store)
	got, _, err := reloaded.LoadIdentity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != first {
		t.Fatalf("failed generation switch replaced last valid identity: %#v", got)
	}
	gotCredentials, err := reloaded.LoadOrCreateCredentials(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if gotCredentials != credentials {
		t.Fatal("failed generation switch replaced local SOCKS credentials")
	}
}

func TestRepositoryPreservesPairingWhenNewGenerationWriteFails(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := &faultStore{Store: securestore.NewMemoryStore()}
	repository := NewRepository(store)
	first := relayclient.Identity{
		RelayURL: "https://one.example:8443", Role: "client", Serial: "AAA",
		PrivateKeyPEM: "first-key", CertificatePEM: "first-chain", CACertificatePEM: "first-ca",
	}
	if err := repository.SaveIdentity(ctx, first); err != nil {
		t.Fatal(err)
	}
	store.failWithPrefix(generationPrefix)
	second := relayclient.Identity{
		RelayURL: "https://two.example:8443", Role: "client", Serial: "BBB",
		PrivateKeyPEM: "second-key", CertificatePEM: "second-chain", CACertificatePEM: "second-ca",
	}
	if err := repository.SaveIdentity(ctx, second); err == nil {
		t.Fatal("SaveIdentity succeeded after the new generation write failed")
	}
	got, _, err := NewRepository(store).LoadIdentity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != first {
		t.Fatalf("failed generation write replaced last valid identity: %#v", got)
	}
}

type faultStore struct {
	securestore.Store
	mu         sync.Mutex
	failKey    string
	failPrefix string
}

func (store *faultStore) failWithPrefix(prefix string) {
	store.mu.Lock()
	store.failPrefix = prefix
	store.mu.Unlock()
}

func (store *faultStore) fail(key string) {
	store.mu.Lock()
	store.failKey = key
	store.mu.Unlock()
}

func (store *faultStore) Put(ctx context.Context, key string, value []byte) error {
	store.mu.Lock()
	fail := key == store.failKey || (store.failPrefix != "" && strings.HasPrefix(key, store.failPrefix))
	store.mu.Unlock()
	if fail {
		return errors.New("injected secure-store write failure")
	}
	return store.Store.Put(ctx, key, value)
}

func TestProxyEndpointRedactsSecretsAndStatusHasNoDestinationModel(t *testing.T) {
	t.Parallel()

	endpoint := ProxyEndpoint{Credentials: Credentials{Username: "local-user", Password: "local-password"}, Port: 1080}
	if got := endpoint.Reveal(); got != "socks5://local-user:local-password@127.0.0.1:1080" {
		t.Fatalf("Reveal() = %q", got)
	}
	if got := fmt.Sprint(endpoint); got != "socks5://***:***@127.0.0.1:1080" {
		t.Fatalf("String() = %q, want redacted proxy line", got)
	}

	status := Status{Running: true, Relay: "connected", AgentAvailable: true, ActiveStreams: 1, BytesUp: 12, BytesDown: 34}
	raw, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, forbidden := range []string{"destination", "hostname", "local-user", "local-password", "example-sensitive.test"} {
		if strings.Contains(strings.ToLower(text), strings.ToLower(forbidden)) {
			t.Fatalf("status JSON exposed forbidden detail %q: %s", forbidden, text)
		}
	}
}
