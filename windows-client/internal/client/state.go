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
	"strings"
	"sync"

	"mobile-egress/windows-client/internal/relayclient"
	"mobile-egress/windows-client/internal/securestore"
)

const (
	activeGenerationKey = "active-identity-generation-v1"
	generationPrefix    = "identity-generation-v1-"
)

type Credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type Repository struct {
	mu    sync.Mutex
	store securestore.Store
}

type persistedSettings struct {
	RelayURL string `json:"relayUrl"`
	Role     string `json:"role"`
	Serial   string `json:"serial"`
	Port     uint16 `json:"port"`
}

type persistedGeneration struct {
	Credentials      Credentials       `json:"credentials"`
	Settings         persistedSettings `json:"settings"`
	PrivateKeyPEM    string            `json:"privateKeyPem,omitempty"`
	CertificatePEM   string            `json:"certificatePem,omitempty"`
	CACertificatePEM string            `json:"caCertificatePem,omitempty"`
}

func NewRepository(store securestore.Store) *Repository {
	return &Repository{store: store}
}

func (repository *Repository) LoadOrCreateCredentials(ctx context.Context) (Credentials, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	generation, err := repository.loadGeneration(ctx)
	if errors.Is(err, securestore.ErrNotFound) {
		generation.Credentials, err = generateCredentials()
		generation.Settings.Port = 1080
		if err == nil {
			err = repository.commitGeneration(ctx, generation)
		}
	}
	if err != nil {
		return Credentials{}, err
	}
	if generation.Credentials.Username == "" || generation.Credentials.Password == "" {
		return Credentials{}, errors.New("stored SOCKS credentials are invalid")
	}
	return generation.Credentials, nil
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
	repository.mu.Lock()
	defer repository.mu.Unlock()
	generation, err := repository.loadOrCreateGeneration(ctx)
	if err != nil {
		return err
	}
	port := generation.Settings.Port
	if port == 0 {
		port = 1080
	}
	generation.Settings = persistedSettings{RelayURL: identity.RelayURL, Role: identity.Role, Serial: identity.Serial, Port: port}
	generation.PrivateKeyPEM = identity.PrivateKeyPEM
	generation.CertificatePEM = identity.CertificatePEM
	generation.CACertificatePEM = identity.CACertificatePEM
	return repository.commitGeneration(ctx, generation)
}

func (repository *Repository) LoadIdentity(ctx context.Context) (relayclient.Identity, uint16, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	generation, err := repository.loadGeneration(ctx)
	if err != nil {
		return relayclient.Identity{}, 0, err
	}
	settings := generation.Settings
	if settings.RelayURL == "" && settings.Role == "" && settings.Serial == "" {
		return relayclient.Identity{}, 0, securestore.ErrNotFound
	}
	if settings.RelayURL == "" || settings.Role == "" || settings.Serial == "" || generation.PrivateKeyPEM == "" || generation.CertificatePEM == "" || generation.CACertificatePEM == "" {
		return relayclient.Identity{}, 0, errors.New("stored identity is incomplete")
	}
	return relayclient.Identity{
		RelayURL: settings.RelayURL, Role: settings.Role, Serial: settings.Serial,
		PrivateKeyPEM: generation.PrivateKeyPEM, CertificatePEM: generation.CertificatePEM, CACertificatePEM: generation.CACertificatePEM,
	}, settings.Port, nil
}

func (repository *Repository) SavePort(ctx context.Context, port uint16) error {
	if port == 0 {
		return errors.New("SOCKS port must be non-zero")
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	generation, err := repository.loadGeneration(ctx)
	if err != nil {
		return err
	}
	if generation.Settings.RelayURL == "" {
		return errors.New("stored settings are invalid")
	}
	generation.Settings.Port = port
	return repository.commitGeneration(ctx, generation)
}

func (repository *Repository) loadOrCreateGeneration(ctx context.Context) (persistedGeneration, error) {
	generation, err := repository.loadGeneration(ctx)
	if err == nil {
		return generation, nil
	}
	if !errors.Is(err, securestore.ErrNotFound) {
		return persistedGeneration{}, err
	}
	credentials, err := generateCredentials()
	if err != nil {
		return persistedGeneration{}, err
	}
	return persistedGeneration{Credentials: credentials, Settings: persistedSettings{Port: 1080}}, nil
}

func (repository *Repository) loadGeneration(ctx context.Context) (persistedGeneration, error) {
	active, err := repository.store.Get(ctx, activeGenerationKey)
	if err != nil {
		return persistedGeneration{}, err
	}
	id := string(active)
	if !validGenerationID(id) {
		return persistedGeneration{}, errors.New("active secure generation pointer is invalid")
	}
	raw, err := repository.store.Get(ctx, generationKey(id))
	if err != nil {
		return persistedGeneration{}, errors.New("active secure generation is unavailable")
	}
	var generation persistedGeneration
	if json.Unmarshal(raw, &generation) != nil || generation.Credentials.Username == "" || generation.Credentials.Password == "" {
		return persistedGeneration{}, errors.New("active secure generation is invalid")
	}
	return generation, nil
}

func (repository *Repository) commitGeneration(ctx context.Context, generation persistedGeneration) error {
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		return fmt.Errorf("generate identity generation: %w", err)
	}
	id := base64.RawURLEncoding.EncodeToString(idBytes)
	raw, err := json.Marshal(generation)
	if err != nil {
		return err
	}
	if err := repository.store.Put(ctx, generationKey(id), raw); err != nil {
		return err
	}
	return repository.store.Put(ctx, activeGenerationKey, []byte(id))
}

func generationKey(id string) string { return generationPrefix + id }

func validGenerationID(id string) bool {
	if len(id) != 22 || strings.TrimSpace(id) != id {
		return false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(id)
	return err == nil && len(decoded) == 16
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
