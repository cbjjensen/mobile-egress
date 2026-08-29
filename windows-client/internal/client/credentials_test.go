package client

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
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

func TestRepositoryPersistsIdentityArtifactsSeparatelyThroughSecureStore(t *testing.T) {
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
	for key, want := range map[string]string{
		privateKeyKey: "private-key", certificateChainKey: "certificate-chain", relayCAKey: "relay-ca",
	} {
		got, err := store.Get(ctx, key)
		if err != nil {
			t.Fatalf("Get(%q): %v", key, err)
		}
		if string(got) != want {
			t.Fatalf("Get(%q) = %q, want %q", key, got, want)
		}
	}
	settings, err := store.Get(ctx, settingsKey)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"private-key", "certificate-chain", "relay-ca"} {
		if strings.Contains(string(settings), forbidden) {
			t.Fatalf("settings contain identity artifact %q: %s", forbidden, settings)
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
