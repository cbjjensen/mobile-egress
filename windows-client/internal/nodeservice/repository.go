// Package nodeservice owns headless Windows Client identity and configuration
// state. Private material never appears in bootstrap or command responses.
package nodeservice

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"mobile-egress/pairing"
	"mobile-egress/windows-client/internal/relayclient"
	"mobile-egress/windows-client/internal/sealedconfig"
	"mobile-egress/windows-client/internal/securestore"
)

const (
	stateKey             = "headless-client-node-state-v1"
	nodeStateVersion     = 1
	configurationVersion = 1
	maximumStateBytes    = 2 << 20
)

type BootstrapResponse struct {
	CSRPEM                 string `json:"csrPem"`
	ConfigurationPublicKey string `json:"configurationPublicKey"`
}

type Configuration struct {
	Version          int    `json:"version"`
	Generation       uint64 `json:"generation"`
	RelayURL         string `json:"relayUrl"`
	Role             string `json:"role"`
	Serial           string `json:"serial"`
	CertificatePEM   string `json:"certificatePem"`
	CACertificatePEM string `json:"caCertificatePem"`
	SOCKSUsername    string `json:"socksUsername"`
	SOCKSPassword    string `json:"socksPassword"`
	SOCKSPort        uint16 `json:"socksPort"`
}

type Runtime struct {
	Identity relayclient.Identity
	Username string
	Password string
	Port     uint16
}

type persistedState struct {
	Version                   int            `json:"version"`
	IdentityPrivateKeyPEM     string         `json:"identityPrivateKeyPem"`
	CSRPEM                    string         `json:"csrPem"`
	ConfigurationPrivateKey   string         `json:"configurationPrivateKey"`
	ConfigurationPublicKey    string         `json:"configurationPublicKey"`
	Configuration             *Configuration `json:"configuration,omitempty"`
	LastConfigurationEnvelope string         `json:"lastConfigurationEnvelope,omitempty"`
}

type Repository struct {
	mu    sync.Mutex
	store securestore.Store
}

func NewRepository(store securestore.Store) *Repository {
	return &Repository{store: store}
}

func (repository *Repository) Bootstrap(ctx context.Context) (BootstrapResponse, error) {
	if repository == nil || repository.store == nil {
		return BootstrapResponse{}, errors.New("node secure store is required")
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	state, err := repository.load(ctx)
	if errors.Is(err, securestore.ErrNotFound) {
		state, err = newState()
		if err == nil {
			err = repository.save(ctx, state)
		}
	}
	if err != nil {
		return BootstrapResponse{}, err
	}
	return BootstrapResponse{CSRPEM: state.CSRPEM, ConfigurationPublicKey: state.ConfigurationPublicKey}, nil
}

func (repository *Repository) Apply(ctx context.Context, envelope sealedconfig.Envelope) error {
	if repository == nil || repository.store == nil {
		return errors.New("node secure store is required")
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	state, err := repository.load(ctx)
	if err != nil {
		return err
	}
	fingerprint, err := envelope.Fingerprint()
	if err != nil {
		return errors.New("sealed node configuration is malformed")
	}
	if fingerprint == state.LastConfigurationEnvelope {
		return errors.New("sealed node configuration was already applied")
	}
	configurationPrivateKey, err := base64.RawURLEncoding.Strict().DecodeString(state.ConfigurationPrivateKey)
	if err != nil || len(configurationPrivateKey) != 32 {
		return errors.New("stored node configuration key is invalid")
	}
	plaintext, err := sealedconfig.Open(configurationPrivateKey, envelope)
	clear(configurationPrivateKey)
	if err != nil {
		return errors.New("sealed node configuration could not be authenticated")
	}
	defer clear(plaintext)
	var configuration Configuration
	decoder := json.NewDecoder(bytes.NewReader(plaintext))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&configuration); err != nil {
		return errors.New("sealed node configuration is invalid")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("sealed node configuration is invalid")
	}
	if err := validateConfiguration(state.IdentityPrivateKeyPEM, configuration); err != nil {
		return errors.New("sealed node configuration is invalid")
	}
	if state.Configuration != nil && !validEndpointOnlyUpdate(*state.Configuration, configuration) {
		return errors.New("sealed node configuration attempted to replace node secrets")
	}
	if state.Configuration == nil {
		if configuration.Generation != 1 {
			return errors.New("sealed node configuration has an invalid initial generation")
		}
	} else if configuration.Generation != state.Configuration.Generation+1 {
		return errors.New("sealed node configuration is stale or out of sequence")
	}
	state.Configuration = &configuration
	state.LastConfigurationEnvelope = fingerprint
	if err := repository.save(ctx, state); err != nil {
		return fmt.Errorf("persist sealed node configuration: %w", err)
	}
	return nil
}

func (repository *Repository) Runtime(ctx context.Context) (Runtime, error) {
	if repository == nil || repository.store == nil {
		return Runtime{}, errors.New("node secure store is required")
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	state, err := repository.load(ctx)
	if err != nil {
		return Runtime{}, err
	}
	if state.Configuration == nil {
		return Runtime{}, errors.New("node configuration has not been applied")
	}
	configuration := *state.Configuration
	if err := validateConfiguration(state.IdentityPrivateKeyPEM, configuration); err != nil {
		return Runtime{}, errors.New("stored node configuration is invalid")
	}
	return Runtime{
		Identity: relayclient.Identity{
			RelayURL: configuration.RelayURL, Role: configuration.Role, Serial: configuration.Serial,
			PrivateKeyPEM: state.IdentityPrivateKeyPEM, CertificatePEM: configuration.CertificatePEM,
			CACertificatePEM: configuration.CACertificatePEM,
		},
		Username: configuration.SOCKSUsername, Password: configuration.SOCKSPassword, Port: configuration.SOCKSPort,
	}, nil
}

func newState() (persistedState, error) {
	identityKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return persistedState{}, fmt.Errorf("generate node identity key: %w", err)
	}
	privateKeyDER, err := x509.MarshalPKCS8PrivateKey(identityKey)
	if err != nil {
		return persistedState{}, fmt.Errorf("encode node identity key: %w", err)
	}
	requestDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: "Mobile Egress Client Node"},
	}, identityKey)
	if err != nil {
		return persistedState{}, fmt.Errorf("create node identity request: %w", err)
	}
	configurationPrivateKey, configurationPublicKey, err := sealedconfig.GenerateKey()
	if err != nil {
		return persistedState{}, err
	}
	defer clear(configurationPrivateKey)
	return persistedState{
		Version:                 nodeStateVersion,
		IdentityPrivateKeyPEM:   string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKeyDER})),
		CSRPEM:                  string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: requestDER})),
		ConfigurationPrivateKey: base64.RawURLEncoding.EncodeToString(configurationPrivateKey),
		ConfigurationPublicKey:  configurationPublicKey,
	}, nil
}

func (repository *Repository) load(ctx context.Context) (persistedState, error) {
	raw, err := repository.store.Get(ctx, stateKey)
	if err != nil {
		return persistedState{}, err
	}
	if len(raw) == 0 || len(raw) > maximumStateBytes {
		return persistedState{}, errors.New("stored node state is invalid")
	}
	var state persistedState
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil || state.Version != nodeStateVersion {
		return persistedState{}, errors.New("stored node state is invalid")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return persistedState{}, errors.New("stored node state is invalid")
	}
	if err := validateBootstrapState(state); err != nil {
		return persistedState{}, errors.New("stored node state is invalid")
	}
	return state, nil
}

func (repository *Repository) save(ctx context.Context, state persistedState) error {
	encoded, err := json.Marshal(state)
	if err != nil {
		return err
	}
	defer clear(encoded)
	return repository.store.Put(ctx, stateKey, encoded)
}

func validateBootstrapState(state persistedState) error {
	if state.Version != nodeStateVersion || state.IdentityPrivateKeyPEM == "" || state.CSRPEM == "" || state.ConfigurationPrivateKey == "" || state.ConfigurationPublicKey == "" {
		return errors.New("incomplete node state")
	}
	privateKey, err := parseIdentityPrivateKey(state.IdentityPrivateKeyPEM)
	if err != nil {
		return err
	}
	block, rest := pem.Decode([]byte(state.CSRPEM))
	if block == nil || block.Type != "CERTIFICATE REQUEST" || len(bytes.TrimSpace(rest)) != 0 {
		return errors.New("invalid node CSR")
	}
	request, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil || request.CheckSignature() != nil || !publicKeysEqual(request.PublicKey, privateKey.Public()) {
		return errors.New("invalid node CSR")
	}
	privateConfigurationKey, err := base64.RawURLEncoding.Strict().DecodeString(state.ConfigurationPrivateKey)
	if err != nil || len(privateConfigurationKey) != 32 {
		return errors.New("invalid node configuration key")
	}
	privateKeyObject, err := ecdh.X25519().NewPrivateKey(privateConfigurationKey)
	clear(privateConfigurationKey)
	if err != nil || base64.RawURLEncoding.EncodeToString(privateKeyObject.PublicKey().Bytes()) != state.ConfigurationPublicKey {
		return errors.New("invalid node configuration key")
	}
	return nil
}

func validateConfiguration(privateKeyPEM string, configuration Configuration) error {
	if configuration.Version != configurationVersion || configuration.Generation == 0 || configuration.Role != "client" || configuration.SOCKSPort != 1080 {
		return errors.New("invalid configuration metadata")
	}
	if _, err := pairing.RelayOrigin(configuration.RelayURL); err != nil {
		return err
	}
	if !validCredential(configuration.SOCKSUsername) || !validCredential(configuration.SOCKSPassword) || !validSerial(configuration.Serial) {
		return errors.New("invalid configuration credentials")
	}
	privateKey, err := parseIdentityPrivateKey(privateKeyPEM)
	if err != nil {
		return err
	}
	ca, err := pairing.CACertificate(configuration.CACertificatePEM)
	if err != nil {
		return err
	}
	block, _ := pem.Decode([]byte(configuration.CertificatePEM))
	if block == nil || block.Type != "CERTIFICATE" {
		return errors.New("invalid client certificate")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil || strings.ToUpper(certificate.SerialNumber.Text(16)) != configuration.Serial || !publicKeysEqual(certificate.PublicKey, privateKey.Public()) {
		return errors.New("invalid client certificate")
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	if _, err := certificate.Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		return errors.New("invalid client certificate")
	}
	return nil
}

func validEndpointOnlyUpdate(existing, replacement Configuration) bool {
	existing.RelayURL = ""
	replacement.RelayURL = ""
	existing.Generation = 0
	replacement.Generation = 0
	return existing == replacement
}

func parseIdentityPrivateKey(value string) (crypto.Signer, error) {
	block, rest := pem.Decode([]byte(value))
	if block == nil || block.Type != "PRIVATE KEY" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, errors.New("invalid node identity key")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("invalid node identity key")
	}
	key, ok := parsed.(crypto.Signer)
	if !ok {
		return nil, errors.New("invalid node identity key")
	}
	return key, nil
}

func publicKeysEqual(left, right crypto.PublicKey) bool {
	leftDER, err := x509.MarshalPKIXPublicKey(left)
	if err != nil {
		return false
	}
	rightDER, err := x509.MarshalPKIXPublicKey(right)
	return err == nil && bytes.Equal(leftDER, rightDER)
}

func validCredential(value string) bool {
	return value != "" && len(value) <= 255 && value == strings.TrimSpace(value)
}

func validSerial(value string) bool {
	if value == "" || value != strings.ToUpper(value) || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if !((character >= '0' && character <= '9') || (character >= 'A' && character <= 'F')) {
			return false
		}
	}
	return true
}
