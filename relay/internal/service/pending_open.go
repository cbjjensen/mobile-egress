package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"time"

	"mobile-egress/relay/internal/policy"
	"mobile-egress/relay/internal/protocol"
)

type pendingOpen struct {
	id     string
	client *session
	ctx    context.Context
	cancel context.CancelFunc
}

func (service *Service) handleClientOpen(client *session, envelope protocol.Envelope) {
	payload, err := envelope.DecodePayload()
	if err != nil {
		service.rejectOpen(client, envelope.StreamID, "invalid_target")
		return
	}
	target, err := parseClientOpen(payload)
	if err != nil {
		service.rejectOpen(client, envelope.StreamID, "invalid_target")
		return
	}

	service.mu.Lock()
	if !service.sessionActiveLocked(client) {
		service.mu.Unlock()
		return
	}
	code := ""
	if service.streams[envelope.StreamID] != nil || service.pendingOpens[envelope.StreamID] != nil || service.closedStreamIDInUseLocked(envelope.StreamID, time.Now()) {
		code = "stream_in_use"
	} else if service.resolverWorkers >= service.maxResolverWorkers {
		code = "agent_unavailable"
	}

	if code != "" {
		service.mu.Unlock()
		service.rejectOpen(client, envelope.StreamID, code)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), service.openingTimeout)
	pending := &pendingOpen{id: envelope.StreamID, client: client, ctx: ctx, cancel: cancel}
	if service.pendingOpens == nil {
		service.pendingOpens = make(map[string]*pendingOpen)
	}
	service.pendingOpens[pending.id] = pending
	service.resolverWorkers++
	service.workers.Add(1)
	service.mu.Unlock()
	go service.resolvePendingOpen(pending, target)
}

func (service *Service) resolvePendingOpen(pending *pendingOpen, target clientOpenRequest) {
	defer service.workers.Done()
	// A canceled reservation is removed immediately, but its
	// worker still owns a permit until resolution has actually returned.
	defer func() { service.mu.Lock(); service.resolverWorkers--; service.mu.Unlock() }()
	defer pending.cancel()
	addresses, err := service.lookupNetIP(pending.ctx, "ip", target.Host)
	if err != nil || len(addresses) == 0 || pending.ctx.Err() != nil {
		service.rejectPendingOpen(pending, "dns_failure")
		return
	}
	for _, address := range addresses {
		if err := policy.ValidatePublicTCPAddress(address.Unmap(), target.Port); err != nil {
			service.rejectPendingOpen(pending, "policy_denied")
			return
		}
	}
	payload, err := json.Marshal(agentOpenRequest{IP: addresses[0].Unmap().String(), Port: target.Port})
	if err != nil {
		service.rejectPendingOpen(pending, "invalid_target")
		return
	}
	forward := protocol.Envelope{Version: protocol.Version1, Type: protocol.TypeOpen, StreamID: pending.id, Payload: base64.RawURLEncoding.EncodeToString(payload)}
	for {
		service.mu.Lock()
		if service.pendingOpens[pending.id] != pending || !service.sessionActiveLocked(pending.client) {
			service.releasePendingOpenLocked(pending, true)
			service.mu.Unlock()
			return
		}
		if pending.ctx.Err() != nil {
			service.mu.Unlock()
			service.rejectPendingOpen(pending, "dns_failure")
			return
		}
		if service.agent == nil && service.agentPending != nil {
			ready := service.agentPending
			service.mu.Unlock()
			select {
			case <-ready:
				continue
			case <-pending.ctx.Done():
				service.rejectPendingOpen(pending, "agent_unavailable")
				return
			}
		}
		agent := service.agent
		if agent == nil || !service.sessionActiveLocked(agent) {
			service.mu.Unlock()
			service.rejectPendingOpen(pending, "agent_unavailable")
			return
		}
		now := time.Now()
		service.releasePendingOpenLocked(pending, false)
		tracked := &stream{id: pending.id, client: pending.client, agent: agent, state: streamOpening, openingDeadline: now.Add(service.openingTimeout), lastActivity: now}
		service.streams[tracked.id] = tracked
		service.activeStreams++
		admission := agent.outbound.enqueue(forward)
		if admission != outboundAdmitted {
			service.removeStreamLocked(tracked)
		} else {
			service.metrics.addStream()
		}
		service.mu.Unlock()
		if admission == outboundControlSaturated {
			agent.close("session_closed")
		}
		if admission != outboundAdmitted {
			service.rejectOpen(pending.client, pending.id, "agent_unavailable")
		}
		return
	}
}

func (service *Service) releasePendingOpenLocked(pending *pendingOpen, remember bool) {
	if service.pendingOpens[pending.id] != pending {
		return
	}
	delete(service.pendingOpens, pending.id)
	pending.cancel()
	if remember {
		service.rememberClosedStreamLocked(&stream{id: pending.id, client: pending.client, state: streamOpening}, time.Now())
	}
}

func (service *Service) rejectPendingOpen(pending *pendingOpen, code string) {
	service.mu.Lock()
	if service.pendingOpens[pending.id] != pending {
		service.mu.Unlock()
		return
	}
	service.releasePendingOpenLocked(pending, true)
	if !service.sessionActiveLocked(pending.client) {
		service.mu.Unlock()
		return
	}
	service.metrics.addError(code)
	admission := pending.client.outbound.enqueue(protocol.Envelope{Version: protocol.Version1, Type: protocol.TypeRejected, StreamID: pending.id, Payload: encodeRelayError(code)})
	service.mu.Unlock()
	if admission == outboundControlSaturated {
		pending.client.close("session_closed")
	}
}
