package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"sort"

	"mobile-egress/pairing"
)

const maximumEndpointUpdates = 256
const maximumEndpointQueueBytes = 64 << 10

// EndpointUpdate preserves the exact public content of each pending legacy
// configuration generation. The surrounding repository remains encrypted.
type EndpointUpdate struct {
	Generation uint64 `json:"generation"`
	RelayURL   string `json:"relayUrl"`
}

func endpointQueue(node ManagedNode) ([]EndpointUpdate, error) {
	if node.PendingEndpointUpdates == "" {
		return nil, nil
	}
	if node.Management != ManagementAWS || len(node.PendingEndpointUpdates) > maximumEndpointQueueBytes {
		return nil, errors.New("invalid pending Client endpoint updates")
	}
	decoder := json.NewDecoder(bytes.NewBufferString(node.PendingEndpointUpdates))
	decoder.DisallowUnknownFields()
	var values []EndpointUpdate
	if decoder.Decode(&values) != nil || decoder.Decode(new(any)) != io.EOF || len(values) == 0 || len(values) > maximumEndpointUpdates {
		return nil, errors.New("invalid pending Client endpoint updates")
	}
	for i, value := range values {
		origin, err := pairing.RelayOrigin(value.RelayURL)
		if err != nil || len(value.RelayURL) > 2048 || origin.String() != value.RelayURL || value.Generation == 0 || value.Generation > math.MaxInt64 || value.Generation > node.ConfigurationGeneration || value.Generation <= node.AppliedGeneration {
			return nil, errors.New("invalid pending Client endpoint update")
		}
		if i > 0 && value.Generation != values[i-1].Generation+1 {
			return nil, errors.New("nonsequential pending Client endpoint updates")
		}
		if i == 0 && node.AppliedGeneration > 0 && value.Generation != node.AppliedGeneration+1 {
			return nil, errors.New("pending Client endpoint generation is missing")
		}
	}
	last := values[len(values)-1]
	if last.Generation != node.ConfigurationGeneration || last.RelayURL != node.RelayURL {
		return nil, errors.New("pending Client endpoint does not match desired configuration")
	}
	return values, nil
}

func encodeEndpointQueue(values []EndpointUpdate) (string, error) {
	if len(values) == 0 {
		return "", nil
	}
	if len(values) > maximumEndpointUpdates {
		return "", errors.New("too many pending Client endpoint updates")
	}
	raw, err := json.Marshal(values)
	if err != nil || len(raw) > maximumEndpointQueueBytes {
		return "", errors.New("pending Client endpoint updates are too large")
	}
	return string(raw), nil
}

func stageClientEndpoint(node ManagedNode, relayURL string) (ManagedNode, error) {
	node = normalizeManagedNode(node)
	if err := validateManagedNode(node); err != nil {
		return ManagedNode{}, err
	}
	if relayURL == node.RelayURL {
		return node, nil
	}
	if node.ConfigurationGeneration >= math.MaxInt64 {
		return ManagedNode{}, errors.New("Client configuration generation exhausted")
	}
	if node.Management == ManagementAWS {
		pending, err := endpointQueue(node)
		if err != nil {
			return ManagedNode{}, err
		}
		if len(pending) == 0 {
			if node.Health == "configuring" {
				pending = append(pending, EndpointUpdate{Generation: node.ConfigurationGeneration, RelayURL: node.RelayURL})
			} else if node.AppliedGeneration == 0 {
				// Legacy installed records predate explicit configuration receipts.
				node.AppliedGeneration = node.ConfigurationGeneration
			}
		}
		pending = append(pending, EndpointUpdate{Generation: node.ConfigurationGeneration + 1, RelayURL: relayURL})
		node.PendingEndpointUpdates, err = encodeEndpointQueue(pending)
		if err != nil {
			return ManagedNode{}, err
		}
	}
	node.ConfigurationGeneration++
	node.RelayURL = relayURL
	node.Health = "configuring"
	if node.Management == ManagementPaired && node.AppliedGeneration > 0 {
		node.SealedConfiguration = ""
	}
	if err := validateManagedNode(node); err != nil {
		return ManagedNode{}, err
	}
	return node, nil
}

// StageClientEndpoints commits the entire fleet's desired endpoint before any
// external delivery. Reconciliation with the same origin is idempotent.
func (repository *Repository) StageClientEndpoints(ctx context.Context, relayURL string) ([]ManagedNode, error) {
	origin, err := pairing.RelayOrigin(relayURL)
	if err != nil || len(relayURL) > 2048 {
		return nil, errors.New("new relay endpoint is invalid")
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	state, err := repository.loadOrCreate(ctx)
	if err != nil {
		return nil, err
	}
	changed := false
	for i, node := range state.Nodes {
		updated, err := stageClientEndpoint(node, origin.String())
		if err != nil {
			return nil, err
		}
		changed = changed || updated != node
		state.Nodes[i] = updated
	}
	if changed {
		if err = repository.save(ctx, state); err != nil {
			return nil, errors.New("persist desired Client endpoints")
		}
	}
	nodes := append([]ManagedNode(nil), state.Nodes...)
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].NodeID < nodes[j].NodeID })
	return nodes, nil
}
