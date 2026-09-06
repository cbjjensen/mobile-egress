package desktop

import (
	"context"
	"mobile-egress/windows-client/internal/relayclient"
)

type controllerHealthClient interface {
	Health(context.Context) (relayclient.RelayHealth, error)
	Close() error
}

// Only the relay component worker uses this state. Cleanup runs after that
// worker exits, including when shutdown's bounded wait has already elapsed.
type controllerHealth struct {
	identity relayclient.Identity
	client   controllerHealthClient
	create   func(relayclient.Identity) (controllerHealthClient, error)
}

func (h *controllerHealth) close() {
	if h.client != nil {
		_ = h.client.Close()
		h.client = nil
	}
	h.identity = relayclient.Identity{}
}
func (h *controllerHealth) read(ctx context.Context, identity relayclient.Identity, ready bool) (componentResult, error) {
	if !ready {
		h.close()
		return componentResult{}, nil
	}
	if h.client != nil && h.identity != identity {
		h.close()
	}
	result := componentResult{ownerReady: true, ownerURL: identity.RelayURL}
	if h.client == nil {
		client, err := h.create(identity)
		if err != nil {
			return result, err
		}
		h.client = client
		h.identity = identity
	}
	health, err := h.client.Health(ctx)
	result.relayReady = health.Readiness
	return result, err
}
