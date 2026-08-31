package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"

	"mobile-egress/windows-client/internal/securestore"
)

const (
	controllerStateKey     = "local-bridge-controller-state-v1"
	controllerStateVersion = 2
)

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
	HTTPProxyReady bool   `json:"httpProxyReady"`
}

type controllerState struct {
	Version          int               `json:"version"`
	AccessKeys       *StoredAccessKeys `json:"accessKeys,omitempty"`
	Nodes            []ManagedNode     `json:"nodes,omitempty"`
	NodeReservations []string          `json:"nodeReservations,omitempty"`
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
	reservationIndex := -1
	for index, instanceID := range state.NodeReservations {
		if instanceID == node.InstanceID {
			reservationIndex = index
			break
		}
	}
	if !replaced {
		if reservationIndex < 0 && len(state.Nodes)+len(state.NodeReservations) >= MaximumManagedNodes {
			return fmt.Errorf("at most %d EC2 nodes can be managed", MaximumManagedNodes)
		}
		state.Nodes = append(state.Nodes, node)
	}
	if reservationIndex >= 0 {
		state.NodeReservations = append(state.NodeReservations[:reservationIndex], state.NodeReservations[reservationIndex+1:]...)
	}
	return repository.save(ctx, state)
}

// ReserveNode durably claims one of the managed-node slots before remote
// provisioning begins. Repeating the same instance ID resumes an interrupted
// attempt; a different instance cannot take that slot.
func (repository *Repository) ReserveNode(ctx context.Context, instanceID string) error {
	if !validInstanceID(instanceID) {
		return errors.New("invalid EC2 instance ID")
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	state, err := repository.loadOrCreate(ctx)
	if err != nil {
		return err
	}
	for _, node := range state.Nodes {
		if node.InstanceID == instanceID {
			return errors.New("EC2 instance is already managed")
		}
	}
	for _, reservedInstanceID := range state.NodeReservations {
		if reservedInstanceID == instanceID {
			return nil
		}
	}
	if len(state.Nodes)+len(state.NodeReservations) >= MaximumManagedNodes {
		return fmt.Errorf("at most %d EC2 Client nodes can be managed", MaximumManagedNodes)
	}
	state.NodeReservations = append(state.NodeReservations, instanceID)
	return repository.save(ctx, state)
}

func (repository *Repository) ReleaseNodeReservation(ctx context.Context, instanceID string) error {
	if !validInstanceID(instanceID) {
		return errors.New("invalid EC2 instance ID")
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	state, err := repository.loadOrCreate(ctx)
	if err != nil {
		return err
	}
	for index, reservedInstanceID := range state.NodeReservations {
		if reservedInstanceID == instanceID {
			state.NodeReservations = append(state.NodeReservations[:index], state.NodeReservations[index+1:]...)
			return repository.save(ctx, state)
		}
	}
	return nil
}

func (repository *Repository) NodeReservations(ctx context.Context) ([]string, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	state, err := repository.loadOrCreate(ctx)
	if err != nil {
		return nil, err
	}
	reservations := append([]string(nil), state.NodeReservations...)
	sort.Strings(reservations)
	return reservations, nil
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
			Health: node.Health, Proxy: "127.0.0.1:1081:***:***", HTTPProxyReady: supportsHTTPConnect(node.ServiceVersion),
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
			if !supportsHTTPConnect(node.ServiceVersion) {
				return "", errors.New("managed EC2 Client must be updated before HTTP proxying")
			}
			return fmt.Sprintf("127.0.0.1:1081:%s:%s", node.SOCKSUsername, node.SOCKSPassword), nil
		}
	}
	return "", errors.New("managed EC2 node was not found")
}

func (repository *Repository) SOCKSProxyURL(ctx context.Context, instanceID string) (string, error) {
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

func supportsHTTPConnect(version string) bool {
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return false
	}
	values := make([]uint64, len(parts))
	for index, part := range parts {
		value, err := strconv.ParseUint(part, 10, 32)
		if err != nil {
			return false
		}
		values[index] = value
	}
	if values[0] != 1 {
		return values[0] > 1
	}
	if values[1] != 0 {
		return values[1] > 0
	}
	return values[2] >= 22
}

func (repository *Repository) loadOrCreate(ctx context.Context) (controllerState, error) {
	state, err := repository.load(ctx)
	if errors.Is(err, securestore.ErrNotFound) {
		return controllerState{Version: controllerStateVersion}, nil
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
	if decoder.Decode(&state) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return controllerState{}, errors.New("encrypted controller state is invalid")
	}
	migrated := migrateControllerState(&state)
	if state.Version != controllerStateVersion {
		return controllerState{}, errors.New("encrypted controller state is invalid")
	}
	seen := make(map[string]struct{}, len(state.Nodes)+len(state.NodeReservations))
	for _, node := range state.Nodes {
		if err := validateManagedNode(node); err != nil {
			return controllerState{}, errors.New("encrypted controller state is invalid")
		}
		if _, exists := seen[node.InstanceID]; exists {
			return controllerState{}, errors.New("encrypted controller state is invalid")
		}
		seen[node.InstanceID] = struct{}{}
	}
	for _, instanceID := range state.NodeReservations {
		if !validInstanceID(instanceID) {
			return controllerState{}, errors.New("encrypted controller state is invalid")
		}
		if _, exists := seen[instanceID]; exists {
			return controllerState{}, errors.New("encrypted controller state is invalid")
		}
		seen[instanceID] = struct{}{}
	}
	if len(seen) > MaximumManagedNodes {
		return controllerState{}, errors.New("encrypted controller state is invalid")
	}
	if migrated {
		if err := repository.save(ctx, state); err != nil {
			return controllerState{}, fmt.Errorf("migrate encrypted controller state: %w", err)
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

func migrateControllerState(state *controllerState) bool {
	if state == nil || state.Version != 1 {
		return false
	}
	state.Version = controllerStateVersion
	for index := range state.Nodes {
		if state.Nodes[index].ConfigurationGeneration == 0 {
			state.Nodes[index].ConfigurationGeneration = 1
		}
	}
	return true
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
