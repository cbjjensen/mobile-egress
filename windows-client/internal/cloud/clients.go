package cloud

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	ManagementAWS    = "aws-ssm"
	ManagementPaired = "paired"
)

// PendingClient is stored only in the controller's encrypted native store.
// Invitation is never included in ordinary snapshots or activity messages.
type PendingClient struct {
	NodeID       string    `json:"nodeId"`
	DisplayName  string    `json:"displayName"`
	EnrollmentID string    `json:"enrollmentId,omitempty"`
	Invitation   string    `json:"invitation,omitempty"`
	ExpiresAt    time.Time `json:"expiresAt,omitempty"`
}

type PendingClientView struct {
	NodeID      string    `json:"nodeId"`
	DisplayName string    `json:"displayName"`
	ExpiresAt   time.Time `json:"expiresAt"`
}

func normalizeManagedNode(node ManagedNode) ManagedNode {
	if node.Management == "" && validInstanceID(node.InstanceID) {
		node.Management = ManagementAWS
	}
	if node.Management == ManagementAWS {
		if node.NodeID == "" {
			node.NodeID = node.InstanceID
		}
		if node.DisplayName == "" {
			node.DisplayName = node.InstanceID
		}
		if node.Platform == "" {
			node.Platform = "windows"
		}
		if node.Architecture == "" {
			node.Architecture = "amd64"
		}
	}
	return node
}

func validPairedNodeID(id string) bool {
	if len(id) != 39 || !strings.HasPrefix(id, "paired-") || strings.ToLower(id) != id {
		return false
	}
	_, err := hex.DecodeString(id[7:])
	return err == nil
}

func validClientName(name string) bool {
	return strings.TrimSpace(name) == name && name != "" && len(name) <= 80 && !strings.ContainsFunc(name, unicode.IsControl)
}

func nodeProxyAddress(node ManagedNode, port uint16) string {
	host := "127.0.0.2"
	if node.Platform == "macos" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, strconv.Itoa(int(port)))
}

func nodeProxyReady(node ManagedNode) bool {
	if node.Management == ManagementPaired {
		return node.AppliedGeneration > 0
	}
	return supportsManagedNodeProxy(node.ServiceVersion)
}

func clientSlotCount(state controllerState) int {
	ids := make(map[string]bool)
	for _, node := range state.Nodes {
		ids[node.NodeID] = true
	}
	for _, id := range state.NodeReservations {
		ids[id] = true
	}
	for _, pending := range state.PendingClients {
		ids[pending.NodeID] = true
	}
	return len(ids)
}

func stateHasPendingClient(state controllerState, id string) bool {
	for _, pending := range state.PendingClients {
		if pending.NodeID == id {
			return true
		}
	}
	return false
}

func (repository *Repository) ReserveClient(ctx context.Context, id, name string) error {
	if !validPairedNodeID(id) || !validClientName(name) {
		return errors.New("Client ID and display name are invalid")
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	state, err := repository.loadOrCreate(ctx)
	if err != nil {
		return err
	}
	if stateHasPendingClient(state, id) {
		return nil
	}
	for _, node := range state.Nodes {
		if node.NodeID == id {
			return errors.New("Client is already registered")
		}
	}
	if clientSlotCount(state) >= MaximumManagedNodes {
		return fmt.Errorf("at most %d Clients can be managed", MaximumManagedNodes)
	}
	state.PendingClients = append(state.PendingClients, PendingClient{NodeID: id, DisplayName: name})
	return repository.save(ctx, state)
}

func (repository *Repository) SaveClientInvitation(ctx context.Context, pending PendingClient) error {
	if !validPairedNodeID(pending.NodeID) || !validClientName(pending.DisplayName) || pending.EnrollmentID == "" || pending.Invitation == "" || pending.ExpiresAt.IsZero() {
		return errors.New("Client invitation is incomplete")
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	state, err := repository.loadOrCreate(ctx)
	if err != nil {
		return err
	}
	for i, current := range state.PendingClients {
		if current.NodeID == pending.NodeID {
			if current.EnrollmentID != "" && current.EnrollmentID != pending.EnrollmentID {
				return errors.New("Client invitation identity changed")
			}
			state.PendingClients[i] = pending
			return repository.save(ctx, state)
		}
	}
	return errors.New("Client slot is not reserved")
}

func (repository *Repository) PendingClients(ctx context.Context) ([]PendingClient, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	state, err := repository.loadOrCreate(ctx)
	if err != nil {
		return nil, err
	}
	result := append([]PendingClient(nil), state.PendingClients...)
	sort.Slice(result, func(i, j int) bool { return result[i].NodeID < result[j].NodeID })
	return result, nil
}

func (repository *Repository) CompleteClientInvitation(ctx context.Context, id string) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	state, err := repository.loadOrCreate(ctx)
	if err != nil {
		return err
	}
	for i, pending := range state.PendingClients {
		if pending.NodeID == id {
			state.PendingClients = append(state.PendingClients[:i], state.PendingClients[i+1:]...)
			return repository.save(ctx, state)
		}
	}
	return nil
}

// RemoveClient is called only after the relay confirms cancellation/revocation.
func (repository *Repository) RemoveClient(ctx context.Context, id string) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	state, err := repository.loadOrCreate(ctx)
	if err != nil {
		return err
	}
	for i, node := range state.Nodes {
		if node.NodeID == id {
			state.Nodes = append(state.Nodes[:i], state.Nodes[i+1:]...)
			break
		}
	}
	for i, pending := range state.PendingClients {
		if pending.NodeID == id {
			state.PendingClients = append(state.PendingClients[:i], state.PendingClients[i+1:]...)
			break
		}
	}
	return repository.save(ctx, state)
}
