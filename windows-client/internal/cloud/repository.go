package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"

	"mobile-egress/windows-client/internal/securestore"
)

const controllerStateKey = "local-bridge-controller-state-v1"

type StoredAccessKeys struct {
	AccessKeyID     string `json:"accessKeyId"`
	SecretAccessKey string `json:"secretAccessKey"`
	SessionToken    string `json:"sessionToken,omitempty"`
}

type ManagedNodeView struct {
	InstanceID     string `json:"instanceId"`
	ClientSerial   string `json:"clientSerial"`
	ServiceVersion string `json:"serviceVersion"`
	Health         string `json:"health"`
	Proxy          string `json:"proxy"`
}

type controllerState struct {
	Version    int               `json:"version"`
	AccessKeys *StoredAccessKeys `json:"accessKeys,omitempty"`
	Nodes      []ManagedNode     `json:"nodes,omitempty"`
}

type Repository struct {
	mu    sync.Mutex
	store securestore.Store
}

func NewRepository(store securestore.Store) *Repository { return &Repository{store: store} }

func (repository *Repository) SaveAccessKeys(ctx context.Context, credentials StoredAccessKeys) error {
	if strings.TrimSpace(credentials.AccessKeyID) == "" || strings.TrimSpace(credentials.SecretAccessKey) == "" {
		return errors.New("AWS access key ID and secret are required")
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	state, err := repository.loadOrCreate(ctx)
	if err != nil {
		return err
	}
	state.AccessKeys = &credentials
	return repository.save(ctx, state)
}

func (repository *Repository) AccessKeys(ctx context.Context) (StoredAccessKeys, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	state, err := repository.load(ctx)
	if err != nil {
		return StoredAccessKeys{}, err
	}
	if state.AccessKeys == nil {
		return StoredAccessKeys{}, securestore.ErrNotFound
	}
	return *state.AccessKeys, nil
}

func (repository *Repository) SaveNode(ctx context.Context, node ManagedNode) error {
	if err := validateManagedNode(node); err != nil {
		return err
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	state, err := repository.loadOrCreate(ctx)
	if err != nil {
		return err
	}
	replaced := false
	for index := range state.Nodes {
		if state.Nodes[index].InstanceID == node.InstanceID {
			state.Nodes[index] = node
			replaced = true
			break
		}
	}
	if !replaced {
		if len(state.Nodes) >= MaximumManagedNodes {
			return fmt.Errorf("at most %d EC2 nodes can be managed", MaximumManagedNodes)
		}
		state.Nodes = append(state.Nodes, node)
	}
	return repository.save(ctx, state)
}

func (repository *Repository) Nodes(ctx context.Context) ([]ManagedNode, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	state, err := repository.loadOrCreate(ctx)
	if err != nil {
		return nil, err
	}
	nodes := append([]ManagedNode(nil), state.Nodes...)
	sort.Slice(nodes, func(left, right int) bool { return nodes[left].InstanceID < nodes[right].InstanceID })
	return nodes, nil
}

func (repository *Repository) NodeViews(ctx context.Context) ([]ManagedNodeView, error) {
	nodes, err := repository.Nodes(ctx)
	if err != nil {
		return nil, err
	}
	views := make([]ManagedNodeView, 0, len(nodes))
	for _, node := range nodes {
		views = append(views, ManagedNodeView{
			InstanceID: node.InstanceID, ClientSerial: node.ClientSerial, ServiceVersion: node.ServiceVersion,
			Health: node.Health, Proxy: "socks5://***:***@127.0.0.1:1080",
		})
	}
	return views, nil
}

func (repository *Repository) ProxyLine(ctx context.Context, instanceID string) (string, error) {
	nodes, err := repository.Nodes(ctx)
	if err != nil {
		return "", err
	}
	for _, node := range nodes {
		if node.InstanceID == instanceID {
			return fmt.Sprintf("socks5://%s:%s@127.0.0.1:%d", node.SOCKSUsername, node.SOCKSPassword, node.SOCKSPort), nil
		}
	}
	return "", errors.New("managed EC2 node was not found")
}

func (repository *Repository) loadOrCreate(ctx context.Context) (controllerState, error) {
	state, err := repository.load(ctx)
	if errors.Is(err, securestore.ErrNotFound) {
		return controllerState{Version: 1}, nil
	}
	return state, err
}

func (repository *Repository) load(ctx context.Context) (controllerState, error) {
	if repository == nil || repository.store == nil {
		return controllerState{}, errors.New("encrypted controller store is required")
	}
	raw, err := repository.store.Get(ctx, controllerStateKey)
	if err != nil {
		return controllerState{}, err
	}
	if len(raw) == 0 || len(raw) > 2<<20 {
		return controllerState{}, errors.New("encrypted controller state is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var state controllerState
	if decoder.Decode(&state) != nil || state.Version != 1 || decoder.Decode(&struct{}{}) != io.EOF {
		return controllerState{}, errors.New("encrypted controller state is invalid")
	}
	for _, node := range state.Nodes {
		if err := validateManagedNode(node); err != nil {
			return controllerState{}, errors.New("encrypted controller state is invalid")
		}
	}
	return state, nil
}

func (repository *Repository) save(ctx context.Context, state controllerState) error {
	encoded, err := json.Marshal(state)
	if err != nil {
		return err
	}
	defer clear(encoded)
	return repository.store.Put(ctx, controllerStateKey, encoded)
}

func validateManagedNode(node ManagedNode) error {
	if !validInstanceID(node.InstanceID) || node.ClientSerial == "" || node.ConfigurationPublicKey == "" || node.ConfigurationGeneration == 0 || node.ServiceVersion == "" ||
		node.SOCKSUsername == "" || node.SOCKSPassword == "" || node.SOCKSPort != 1080 || node.RelayURL == "" ||
		node.CertificatePEM == "" || node.CACertificatePEM == "" {
		return errors.New("managed EC2 node metadata is incomplete")
	}
	return nil
}

func ValidateInstallCandidate(nodes []ManagedNode, instanceID string) error {
	if !validInstanceID(instanceID) {
		return errors.New("invalid EC2 instance ID")
	}
	for _, node := range nodes {
		if node.InstanceID == instanceID {
			return errors.New("EC2 instance is already managed")
		}
	}
	if len(nodes) >= MaximumManagedNodes {
		return fmt.Errorf("at most %d EC2 Client nodes can be managed", MaximumManagedNodes)
	}
	return nil
}
