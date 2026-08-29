// Package client coordinates persisted Windows client state and presentation.
package client

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"

	"mobile-egress/windows-client/internal/relayclient"
	"mobile-egress/windows-client/internal/securestore"
)

const (
	credentialsKey      = "local-socks-credentials-v1"
	settingsKey         = "local-settings-v1"
	privateKeyKey       = "client-private-key-v1"
	certificateChainKey = "client-certificate-chain-v1"
	relayCAKey          = "relay-ca-certificate-v1"
)

type Credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type Repository struct {
	store securestore.Store
}

type persistedSettings struct {
	RelayURL string `json:"relayUrl"`
	Role     string `json:"role"`
	Serial   string `json:"serial"`
	Port     uint16 `json:"port"`
}

func NewRepository(store securestore.Store) *Repository {
	return &Repository{store: store}
}

func (repository *Repository) LoadOrCreateCredentials(ctx context.Context) (Credentials, error) {
	raw, err := repository.store.Get(ctx, credentialsKey)
	if err == nil {
		var credentials Credentials
		if json.Unmarshal(raw, &credentials) != nil || credentials.Username == "" || credentials.Password == "" {
			return Credentials{}, errors.New("stored SOCKS credentials are invalid")
		}
		return credentials, nil
	}
	if !errors.Is(err, securestore.ErrNotFound) {
		return Credentials{}, err
	}
	credentials, err := generateCredentials()
	if err != nil {
		return Credentials{}, err
	}
	raw, err = json.Marshal(credentials)
	if err != nil {
		return Credentials{}, err
	}
	if err := repository.store.Put(ctx, credentialsKey, raw); err != nil {
		return Credentials{}, err
	}
	return credentials, nil
}

func generateCredentials() (Credentials, error) {
	values := make([][]byte, 2)
	for index := range values {
		values[index] = make([]byte, 32)
		if _, err := rand.Read(values[index]); err != nil {
			return Credentials{}, fmt.Errorf("generate SOCKS credentials: %w", err)
		}
	}
	return Credentials{
		Username: base64.RawURLEncoding.EncodeToString(values[0]),
		Password: base64.RawURLEncoding.EncodeToString(values[1]),
	}, nil
}

func (repository *Repository) SaveIdentity(ctx context.Context, identity relayclient.Identity) error {
	if identity.RelayURL == "" || identity.Role == "" || identity.Serial == "" || identity.PrivateKeyPEM == "" || identity.CertificatePEM == "" || identity.CACertificatePEM == "" {
		return errors.New("identity is incomplete")
	}
	port := uint16(1080)
	if raw, err := repository.store.Get(ctx, settingsKey); err == nil {
		var current persistedSettings
		if json.Unmarshal(raw, &current) == nil && current.Port != 0 {
			port = current.Port
		}
	}
	for key, value := range map[string]string{
		privateKeyKey: identity.PrivateKeyPEM, certificateChainKey: identity.CertificatePEM, relayCAKey: identity.CACertificatePEM,
	} {
		if err := repository.store.Put(ctx, key, []byte(value)); err != nil {
			return err
		}
	}
	settings, _ := json.Marshal(persistedSettings{RelayURL: identity.RelayURL, Role: identity.Role, Serial: identity.Serial, Port: port})
	return repository.store.Put(ctx, settingsKey, settings)
}

func (repository *Repository) LoadIdentity(ctx context.Context) (relayclient.Identity, uint16, error) {
	raw, err := repository.store.Get(ctx, settingsKey)
	if err != nil {
		return relayclient.Identity{}, 0, err
	}
	var settings persistedSettings
	if json.Unmarshal(raw, &settings) != nil || settings.RelayURL == "" || settings.Role == "" || settings.Serial == "" {
		return relayclient.Identity{}, 0, errors.New("stored settings are invalid")
	}
	values := make(map[string]string, 3)
	for _, key := range []string{privateKeyKey, certificateChainKey, relayCAKey} {
		value, loadErr := repository.store.Get(ctx, key)
		if loadErr != nil || len(value) == 0 {
			return relayclient.Identity{}, 0, errors.New("stored identity is incomplete")
		}
		values[key] = string(value)
	}
	return relayclient.Identity{
		RelayURL: settings.RelayURL, Role: settings.Role, Serial: settings.Serial,
		PrivateKeyPEM: values[privateKeyKey], CertificatePEM: values[certificateChainKey], CACertificatePEM: values[relayCAKey],
	}, settings.Port, nil
}

func (repository *Repository) SavePort(ctx context.Context, port uint16) error {
	if port == 0 {
		return errors.New("SOCKS port must be non-zero")
	}
	raw, err := repository.store.Get(ctx, settingsKey)
	if err != nil {
		return err
	}
	var settings persistedSettings
	if json.Unmarshal(raw, &settings) != nil {
		return errors.New("stored settings are invalid")
	}
	settings.Port = port
	raw, _ = json.Marshal(settings)
	return repository.store.Put(ctx, settingsKey, raw)
}

type ProxyEndpoint struct {
	Credentials Credentials
	Port        uint16
}

func (endpoint ProxyEndpoint) Reveal() string {
	user := url.UserPassword(endpoint.Credentials.Username, endpoint.Credentials.Password)
	return fmt.Sprintf("socks5://%s@127.0.0.1:%d", user.String(), endpoint.Port)
}

func (endpoint ProxyEndpoint) String() string {
	return fmt.Sprintf("socks5://***:***@127.0.0.1:%d", endpoint.Port)
}

type Status struct {
	Paired         bool   `json:"paired"`
	Role           string `json:"role,omitempty"`
	Running        bool   `json:"running"`
	Relay          string `json:"relay"`
	AgentAvailable bool   `json:"agentAvailable"`
	ActiveStreams  int    `json:"activeStreams"`
	BytesUp        int64  `json:"bytesUp"`
	BytesDown      int64  `json:"bytesDown"`
	Port           uint16 `json:"port,omitempty"`
	Proxy          string `json:"proxy,omitempty"`
}
