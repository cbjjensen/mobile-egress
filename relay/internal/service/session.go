package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gorilla/websocket"
	"mobile-egress/relay/internal/enrollment"
	"mobile-egress/relay/internal/policy"
	"mobile-egress/relay/internal/protocol"
)

const (
	maxWebSocketMessageBytes      = 2 << 20
	webSocketWriteTimeout         = time.Second
	agentSessionLivenessTimeout   = 65 * time.Second
	maxClosedStreamTombstones     = 1024
	closedStreamTombstoneLifetime = 30 * time.Second
)

type lookupNetIPFunc func(context.Context, string, string) ([]netip.Addr, error)

func defaultLookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, network, host)
}

var relayErrorCodes = map[string]struct{}{
	"agent_stream_limit":  {},
	"agent_unavailable":   {},
	"client_closed":       {},
	"client_stream_limit": {},
	"dns_failure":         {},
	"idle_timeout":        {},
	"invalid_target":      {},
	"opening_timeout":     {},
	"policy_denied":       {},
	"protocol_error":      {},
	"revoked":             {},
	"session_closed":      {},
	"stream_in_use":       {},
	"stream_not_found":    {},
	"target_closed":       {},
	"target_failure":      {},
}

type session struct {
	service *Service
	serial  string
	role    enrollment.Role
	conn    sessionConnection

	outbound         *outboundMailbox
	writeMu          sync.Mutex
	closeOnce        sync.Once
	registered       bool
	lastInbound      time.Time
	livenessExpiring bool
}

type sessionConnection interface {
	SetReadLimit(int64)
	SetPingHandler(func(string) error)
	ReadMessage() (int, []byte, error)
	SetWriteDeadline(time.Time) error
	WriteMessage(int, []byte) error
	WriteControl(int, []byte, time.Time) error
	Close() error
}

var (
	errOutboundDataSaturated = errors.New("session data lane saturated")
	errSessionUnavailable    = errors.New("session unavailable")
)

type streamState uint8

const (
	streamOpening streamState = iota
	streamOpen
)

type stream struct {
	id              string
	client          *session
	agent           *session
	state           streamState
	openingDeadline time.Time
	lastActivity    time.Time
}

type closedStreamTombstone struct {
	client             *session
	agent              *session
	state              streamState
	clientDataAllowed  bool
	openingOutcomeSeen bool
	expiresAt          time.Time
}

type clientOpenRequest struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

type agentOpenRequest struct {
	IP   string `json:"ip"`
	Port int    `json:"port"`
}

type streamNotification struct {
	target   *session
	envelope protocol.Envelope
}

var sessionUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin: func(request *http.Request) bool {
		return request.Header.Get("Origin") == ""
	},
}

func (service *Service) handleSession(writer http.ResponseWriter, request *http.Request) {
	serial, role, status := service.authenticateRequest(request)
	if status != 0 {
		writeAPIError(writer, status, authErrorCode(status))
		return
	}
	if role != enrollment.RoleClient && role != enrollment.RoleAgent {
		writeAPIError(writer, http.StatusForbidden, "session_role_required")
		return
	}

	service.mu.Lock()
	if service.closed {
		service.mu.Unlock()
		writeAPIError(writer, http.StatusServiceUnavailable, "not_ready")
		return
	}
	currentRole, revoked, err := service.store.identityStatus(request.Context(), serial)
	if err != nil || revoked || currentRole != role {
		service.mu.Unlock()
		writeAPIError(writer, http.StatusUnauthorized, "unauthorized")
		return
	}
	if _, exists := service.sessions[serial]; exists || (role == enrollment.RoleAgent && service.agent != nil) {
		service.mu.Unlock()
		writeAPIError(writer, http.StatusConflict, "session_conflict")
		return
	}
	if _, exists := service.pendingSessions[serial]; exists || (role == enrollment.RoleAgent && service.agentPending != nil) {
		service.mu.Unlock()
		writeAPIError(writer, http.StatusConflict, "session_conflict")
		return
	}
	if service.pendingSessions == nil {
		service.pendingSessions = make(map[string]struct{})
	}
	service.pendingSessions[serial] = struct{}{}
	if role == enrollment.RoleAgent {
		service.agentPending = make(chan struct{})
	}
	service.mu.Unlock()

	connection, err := sessionUpgrader.Upgrade(writer, request, nil)
	if err != nil {
		service.mu.Lock()
		service.releasePendingSessionLocked(serial, role)
		service.mu.Unlock()
		return
	}

	service.mu.Lock()
	service.releasePendingSessionLocked(serial, role)
	currentRole, revoked, err = service.store.identityStatus(request.Context(), serial)
	if service.closed || err != nil || revoked || currentRole != role {
		service.mu.Unlock()
		_ = connection.Close()
		return
	}
	if _, exists := service.sessions[serial]; exists || (role == enrollment.RoleAgent && service.agent != nil) {
		service.mu.Unlock()
		_ = connection.Close()
		return
	}
	activeSession := newSession(service, serial, role, connection)
	activeSession.registered = true
	service.sessions[serial] = activeSession
	if role == enrollment.RoleAgent {
		service.agent = activeSession
		service.agentConnected = true
	} else {
		service.connectedClients++
	}
	service.mu.Unlock()

	service.janitorOnce.Do(func() { go service.runStreamJanitor() })
	activeSession.readLoop()
	activeSession.close("session_closed")
}

func (service *Service) releasePendingSessionLocked(serial string, role enrollment.Role) {
	delete(service.pendingSessions, serial)
	if role == enrollment.RoleAgent && service.agentPending != nil {
		close(service.agentPending)
		service.agentPending = nil
	}
}

func newSession(service *Service, serial string, role enrollment.Role, connection sessionConnection) *session {
	activeSession := &session{
		service: service, serial: serial, role: role, conn: connection,
		outbound: newSessionOutboundMailbox(role), lastInbound: time.Now(),
	}
	connection.SetPingHandler(func(payload string) error {
		activeSession.noteInbound(time.Now())
		return connection.WriteControl(websocket.PongMessage, []byte(payload), time.Now().Add(webSocketWriteTimeout))
	})
	go activeSession.writeLoop()
	return activeSession
}

func (activeSession *session) readLoop() {
	activeSession.conn.SetReadLimit(maxWebSocketMessageBytes)
	for {
		messageType, raw, err := activeSession.conn.ReadMessage()
		if err != nil {
			return
		}
		activeSession.noteInbound(time.Now())
		if messageType != websocket.BinaryMessage {
			activeSession.service.protocolViolation(activeSession)
			return
		}
		role, revoked, err := activeSession.service.store.identityStatus(context.Background(), activeSession.serial)
		if err != nil || revoked || role != activeSession.role {
			activeSession.service.closeIdentitySession(activeSession.serial, "revoked")
			return
		}
		envelope, err := protocol.ParseEnvelope(raw)
		if err != nil {
			activeSession.service.protocolViolation(activeSession)
			return
		}
		if envelope.Type == protocol.TypePing {
			if activeSession.send(protocol.Envelope{Version: protocol.Version1, Type: protocol.TypePong, StreamID: "", Payload: ""}) != nil {
				return
			}
			continue
		}
		if envelope.Type == protocol.TypePong {
			continue
		}
		if err := activeSession.service.routeEnvelope(activeSession, envelope); err != nil {
			activeSession.service.protocolViolation(activeSession)
			return
		}
	}
}

func (activeSession *session) noteInbound(now time.Time) {
	activeSession.service.mu.Lock()
	if activeSession.registered && !activeSession.livenessExpiring {
		activeSession.lastInbound = now
	}
	activeSession.service.mu.Unlock()
}

func (service *Service) routeEnvelope(sender *session, envelope protocol.Envelope) error {
	if sender.role == enrollment.RoleClient {
		if envelope.Type == protocol.TypeOpen {
			service.handleClientOpen(sender, envelope)
			return nil
		}
		return service.handleClientStreamFrame(sender, envelope)
	}
	return service.handleAgentStreamFrame(sender, envelope)
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
	resolveContext, cancelResolve := context.WithTimeout(context.Background(), service.openingTimeout)
	defer cancelResolve()
	addresses, err := service.lookupNetIP(resolveContext, "ip", target.Host)
	if err != nil || len(addresses) == 0 {
		service.rejectOpen(client, envelope.StreamID, "dns_failure")
		return
	}
	approved := make([]netip.Addr, 0, len(addresses))
	for _, address := range addresses {
		address = address.Unmap()
		if err := policy.ValidatePublicTCPAddress(address, target.Port); err != nil {
			service.rejectOpen(client, envelope.StreamID, "policy_denied")
			return
		}
		approved = append(approved, address)
	}
	forwardPayload, err := json.Marshal(agentOpenRequest{IP: approved[0].String(), Port: target.Port})
	if err != nil {
		service.rejectOpen(client, envelope.StreamID, "invalid_target")
		return
	}
	forward := protocol.Envelope{
		Version: protocol.Version1, Type: protocol.TypeOpen, StreamID: envelope.StreamID,
		Payload: base64.RawURLEncoding.EncodeToString(forwardPayload),
	}

	for {
		now := time.Now()
		service.mu.Lock()
		currentRole, revoked, identityErr := service.store.identityStatus(context.Background(), client.serial)
		if identityErr != nil || revoked || currentRole != enrollment.RoleClient || !client.registered || service.sessions[client.serial] != client {
			service.mu.Unlock()
			return
		}
		if _, exists := service.streams[envelope.StreamID]; exists || service.closedStreamIDInUseLocked(envelope.StreamID, now) {
			service.mu.Unlock()
			service.rejectOpen(client, envelope.StreamID, "stream_in_use")
			return
		}
		if service.agent == nil && service.agentPending != nil {
			pendingAgent := service.agentPending
			service.mu.Unlock()
			select {
			case <-pendingAgent:
				continue
			case <-resolveContext.Done():
				service.rejectOpen(client, envelope.StreamID, "agent_unavailable")
				return
			}
		}
		if service.agent == nil {
			service.mu.Unlock()
			service.rejectOpen(client, envelope.StreamID, "agent_unavailable")
			return
		}
		clientStreams := 0
		for _, existing := range service.streams {
			if existing.client == client {
				clientStreams++
			}
		}
		if clientStreams >= service.maxClientStreams {
			service.mu.Unlock()
			service.rejectOpen(client, envelope.StreamID, "client_stream_limit")
			return
		}
		if len(service.streams) >= service.maxAgentStreams {
			service.mu.Unlock()
			service.rejectOpen(client, envelope.StreamID, "agent_stream_limit")
			return
		}
		agent := service.agent
		tracked := &stream{
			id: envelope.StreamID, client: client, agent: agent, state: streamOpening,
			openingDeadline: now.Add(service.openingTimeout), lastActivity: now,
		}
		service.streams[envelope.StreamID] = tracked
		service.activeStreams++
		errorCode := ""
		if err := service.store.incrementTotalStreams(context.Background()); err != nil {
			service.removeStreamLocked(tracked)
			errorCode = "agent_unavailable"
		}
		admission := outboundClosed
		if errorCode == "" {
			admission = agent.outbound.enqueue(forward)
			if admission != outboundAdmitted {
				service.removeStreamLocked(tracked)
				errorCode = "agent_unavailable"
			}
		}
		service.mu.Unlock()
		if admission == outboundControlSaturated {
			agent.close("session_closed")
		}
		if errorCode != "" {
			service.rejectOpen(client, envelope.StreamID, errorCode)
		}
		return
	}
}

func (service *Service) handleClientStreamFrame(client *session, envelope protocol.Envelope) error {
	if envelope.Type != protocol.TypeData && envelope.Type != protocol.TypeClose {
		return errors.New("role-incompatible client frame")
	}
	service.mu.Lock()
	tracked, exists := service.streams[envelope.StreamID]
	if !exists {
		if service.absorbLateStreamFrameLocked(client, enrollment.RoleClient, envelope, time.Now()) {
			service.mu.Unlock()
			return nil
		}
		service.mu.Unlock()
		return errors.New("client does not own stream")
	}
	if tracked.client != client {
		service.mu.Unlock()
		return errors.New("client does not own stream")
	}
	if envelope.Type == protocol.TypeData && tracked.state != streamOpen {
		service.mu.Unlock()
		return errors.New("data before stream opened")
	}
	agent := tracked.agent
	tracked.lastActivity = time.Now()
	admission := outboundClosed
	var notifications []streamNotification
	if envelope.Type == protocol.TypeData && agent != nil && agent.outbound != nil {
		if hook := service.beforeDataAdmission; hook != nil {
			hook()
		}
		admission = agent.outbound.enqueue(envelope)
		if admission == outboundDataSaturated {
			service.removeStreamLocked(tracked)
			notifications = service.streamCloseNotificationsLocked(tracked, "agent_unavailable", tracked.agent)
		}
	}
	if envelope.Type == protocol.TypeClose {
		if !validEnvelopeErrorCode(envelope) {
			service.mu.Unlock()
			return errors.New("invalid close error code")
		}
		service.removeStreamLocked(tracked)
	}
	service.mu.Unlock()
	if agent == nil {
		return nil
	}
	if envelope.Type == protocol.TypeData {
		if admission == outboundDataSaturated {
			_ = service.store.incrementError(context.Background(), "agent_unavailable")
			service.dispatchNotifications(notifications)
			return nil
		}
		if admission != outboundAdmitted {
			return nil
		}
		payload, _ := envelope.DecodePayload()
		_ = service.store.addBytes(context.Background(), int64(len(payload)))
		return nil
	}
	_ = agent.send(envelope)
	return nil
}

func (service *Service) handleAgentStreamFrame(agent *session, envelope protocol.Envelope) error {
	if envelope.Type != protocol.TypeOpened && envelope.Type != protocol.TypeRejected && envelope.Type != protocol.TypeData && envelope.Type != protocol.TypeClose {
		return errors.New("role-incompatible agent frame")
	}
	service.mu.Lock()
	tracked, exists := service.streams[envelope.StreamID]
	if !exists {
		if service.absorbLateStreamFrameLocked(agent, enrollment.RoleAgent, envelope, time.Now()) {
			service.mu.Unlock()
			return nil
		}
		service.mu.Unlock()
		return errors.New("agent stream not found")
	}
	if tracked.agent != agent {
		service.mu.Unlock()
		return errors.New("agent stream not found")
	}
	if envelope.Type == protocol.TypeOpened {
		if tracked.state != streamOpening || envelope.Payload != "" {
			service.mu.Unlock()
			return errors.New("invalid opened frame")
		}
		if !time.Now().Before(tracked.openingDeadline) {
			service.removeStreamLocked(tracked)
			notifications := service.streamCloseNotificationsLocked(tracked, "opening_timeout", agent)
			service.mu.Unlock()
			_ = service.store.incrementError(context.Background(), "opening_timeout")
			service.dispatchNotifications(notifications)
			return nil
		}
		tracked.state = streamOpen
	}
	if envelope.Type == protocol.TypeData && tracked.state != streamOpen {
		service.mu.Unlock()
		return errors.New("data before stream opened")
	}
	if (envelope.Type == protocol.TypeRejected || envelope.Type == protocol.TypeClose) && !validEnvelopeErrorCode(envelope) {
		service.mu.Unlock()
		return errors.New("invalid stream error code")
	}
	client := tracked.client
	tracked.lastActivity = time.Now()
	admission := outboundClosed
	var notifications []streamNotification
	if envelope.Type == protocol.TypeData && client != nil && client.outbound != nil {
		if hook := service.beforeDataAdmission; hook != nil {
			hook()
		}
		admission = client.outbound.enqueue(envelope)
		if admission == outboundDataSaturated {
			service.removeStreamLocked(tracked)
			notifications = service.streamCloseNotificationsLocked(tracked, "agent_unavailable", tracked.agent)
		}
	}
	if envelope.Type == protocol.TypeRejected || envelope.Type == protocol.TypeClose {
		service.removeStreamLocked(tracked)
	}
	service.mu.Unlock()
	if envelope.Type == protocol.TypeData {
		if admission == outboundDataSaturated {
			_ = service.store.incrementError(context.Background(), "agent_unavailable")
			service.dispatchNotifications(notifications)
			return nil
		}
		if admission != outboundAdmitted {
			return nil
		}
		payload, _ := envelope.DecodePayload()
		_ = service.store.addBytes(context.Background(), int64(len(payload)))
		return nil
	}
	_ = client.send(envelope)
	return nil
}

func parseClientOpen(payload []byte) (clientOpenRequest, error) {
	if !utf8.Valid(payload) {
		return clientOpenRequest{}, errors.New("open payload is not UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var target clientOpenRequest
	if err := decoder.Decode(&target); err != nil {
		return clientOpenRequest{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return clientOpenRequest{}, errors.New("trailing open JSON")
	}
	if target.Host == "" || target.Host != strings.TrimSpace(target.Host) || len(target.Host) > 253 || strings.ContainsRune(target.Host, '\x00') {
		return clientOpenRequest{}, errors.New("invalid target host")
	}
	if target.Port < 1 || target.Port > 65535 {
		return clientOpenRequest{}, errors.New("invalid target port")
	}
	return target, nil
}

func (service *Service) rejectOpen(client *session, streamID, code string) {
	_ = service.store.incrementError(context.Background(), code)
	_ = client.send(protocol.Envelope{
		Version: protocol.Version1, Type: protocol.TypeRejected, StreamID: streamID, Payload: encodeRelayError(code),
	})
}

func (service *Service) protocolViolation(activeSession *session) {
	_ = service.store.incrementError(context.Background(), "protocol_error")
	activeSession.close("protocol_error")
}

func (service *Service) detachSession(activeSession *session, code string) {
	service.mu.Lock()
	notifications := service.detachSessionLocked(activeSession, code)
	service.mu.Unlock()
	service.dispatchNotifications(notifications)
}

func (service *Service) closeIdentitySession(serial, code string) {
	service.mu.RLock()
	activeSession := service.sessions[serial]
	service.mu.RUnlock()
	if activeSession == nil {
		return
	}
	activeSession.close(code)
}

func (service *Service) revokeIdentity(ctx context.Context, serial string, now time.Time) error {
	service.mu.Lock()
	if err := service.store.revokeIdentity(ctx, serial, now); err != nil {
		service.mu.Unlock()
		return err
	}
	activeSession := service.sessions[serial]
	service.mu.Unlock()
	if activeSession != nil {
		activeSession.close("revoked")
	}
	return nil
}

func (service *Service) detachSessionLocked(activeSession *session, code string) []streamNotification {
	if !activeSession.registered {
		return nil
	}
	activeSession.registered = false
	delete(service.sessions, activeSession.serial)
	if activeSession.role == enrollment.RoleAgent && service.agent == activeSession {
		service.agent = nil
		service.agentConnected = false
	} else if activeSession.role == enrollment.RoleClient && service.connectedClients > 0 {
		service.connectedClients--
	}
	agent := service.agent
	notifications := make([]streamNotification, 0)
	for _, tracked := range service.streams {
		if activeSession.role != enrollment.RoleAgent && tracked.client != activeSession {
			continue
		}
		service.removeStreamLocked(tracked)
		frame := streamCloseEnvelope(tracked.id, code)
		if tracked.client != activeSession {
			notifications = append(notifications, streamNotification{target: tracked.client, envelope: frame})
		}
		if agent != nil && agent != activeSession {
			notifications = append(notifications, streamNotification{target: agent, envelope: frame})
		}
	}
	return notifications
}

func (service *Service) removeStreamLocked(tracked *stream) {
	if current, exists := service.streams[tracked.id]; !exists || current != tracked {
		return
	}
	delete(service.streams, tracked.id)
	if tracked.client != nil && tracked.client.outbound != nil {
		tracked.client.outbound.discardStreamData(tracked.id)
	}
	if tracked.agent != nil && tracked.agent.outbound != nil {
		tracked.agent.outbound.discardStreamData(tracked.id)
	}
	service.rememberClosedStreamLocked(tracked, time.Now())
	if service.activeStreams > 0 {
		service.activeStreams--
	}
}

func (service *Service) rememberClosedStreamLocked(tracked *stream, now time.Time) {
	if len(service.closedStreams) >= maxClosedStreamTombstones {
		oldestID := ""
		var oldestExpiry time.Time
		for id, tombstone := range service.closedStreams {
			if oldestID == "" || tombstone.expiresAt.Before(oldestExpiry) {
				oldestID = id
				oldestExpiry = tombstone.expiresAt
			}
		}
		delete(service.closedStreams, oldestID)
	}
	service.closedStreams[tracked.id] = closedStreamTombstone{
		client: tracked.client, agent: tracked.agent, state: tracked.state,
		clientDataAllowed: tracked.state == streamOpen,
		expiresAt:         now.Add(closedStreamTombstoneLifetime),
	}
}

func (service *Service) absorbLateStreamFrameLocked(sender *session, role enrollment.Role, envelope protocol.Envelope, now time.Time) bool {
	if err := envelope.Validate(); err != nil {
		return false
	}
	tombstone, exists := service.closedStreams[envelope.StreamID]
	if !exists {
		return false
	}
	if !now.Before(tombstone.expiresAt) {
		delete(service.closedStreams, envelope.StreamID)
		return false
	}
	if role == enrollment.RoleClient {
		if tombstone.client != sender {
			return false
		}
		switch envelope.Type {
		case protocol.TypeData:
			return tombstone.clientDataAllowed
		case protocol.TypeClose:
			return validEnvelopeErrorCode(envelope)
		default:
			return false
		}
	}
	if role != enrollment.RoleAgent || tombstone.agent != sender {
		return false
	}
	switch envelope.Type {
	case protocol.TypeOpened:
		if tombstone.state != streamOpening || tombstone.openingOutcomeSeen || envelope.Payload != "" {
			return false
		}
		tombstone.state = streamOpen
		tombstone.openingOutcomeSeen = true
		service.closedStreams[envelope.StreamID] = tombstone
		return true
	case protocol.TypeRejected:
		if tombstone.state != streamOpening || tombstone.openingOutcomeSeen || !validEnvelopeErrorCode(envelope) {
			return false
		}
		tombstone.openingOutcomeSeen = true
		service.closedStreams[envelope.StreamID] = tombstone
		return true
	case protocol.TypeData:
		return tombstone.state == streamOpen
	case protocol.TypeClose:
		return validEnvelopeErrorCode(envelope)
	default:
		return false
	}
}

func (service *Service) closedStreamIDInUseLocked(streamID string, now time.Time) bool {
	tombstone, exists := service.closedStreams[streamID]
	if !exists {
		return false
	}
	if !now.Before(tombstone.expiresAt) {
		delete(service.closedStreams, streamID)
		return false
	}
	return true
}

func (service *Service) runStreamJanitor() {
	ticker := time.NewTicker(service.sweepInterval)
	defer ticker.Stop()
	for {
		select {
		case now := <-ticker.C:
			service.expireStreams(now)
		case <-service.stopJanitor:
			return
		}
	}
}

func (service *Service) expireStreams(now time.Time) {
	service.mu.Lock()
	for id, tombstone := range service.closedStreams {
		if !now.Before(tombstone.expiresAt) {
			delete(service.closedStreams, id)
		}
	}
	expiredCodes := make([]string, 0)
	expiredSessions := make([]*session, 0)
	notifications := make([]streamNotification, 0)
	agent := service.agent
	for _, tracked := range service.streams {
		code := ""
		if tracked.state == streamOpening && !now.Before(tracked.openingDeadline) {
			code = "opening_timeout"
		} else if !now.Before(tracked.lastActivity.Add(service.idleTimeout)) {
			code = "idle_timeout"
		}
		if code != "" {
			expiredCodes = append(expiredCodes, code)
			service.removeStreamLocked(tracked)
			notifications = append(notifications, service.streamCloseNotificationsLocked(tracked, code, agent)...)
		}
	}
	for _, activeSession := range service.sessions {
		if activeSession.role == enrollment.RoleAgent && !activeSession.livenessExpiring && !now.Before(activeSession.lastInbound.Add(agentSessionLivenessTimeout)) {
			activeSession.livenessExpiring = true
			expiredSessions = append(expiredSessions, activeSession)
		}
	}
	service.mu.Unlock()
	for _, activeSession := range expiredSessions {
		activeSession.close("session_closed")
	}
	for _, code := range expiredCodes {
		_ = service.store.incrementError(context.Background(), code)
	}
	service.dispatchNotifications(notifications)
}

func (activeSession *session) send(envelope protocol.Envelope) error {
	if activeSession.outbound == nil {
		return errSessionUnavailable
	}
	switch activeSession.outbound.enqueue(envelope) {
	case outboundAdmitted:
		return nil
	case outboundDataSaturated:
		return errOutboundDataSaturated
	case outboundControlSaturated:
		activeSession.close("session_closed")
		return errSessionUnavailable
	default:
		return errSessionUnavailable
	}
}

func (activeSession *session) writeLoop() {
	for {
		envelope, ok := activeSession.outbound.wait()
		if !ok {
			return
		}
		message, err := json.Marshal(envelope)
		if err == nil {
			activeSession.writeMu.Lock()
			deadline := time.Now().Add(webSocketWriteTimeout)
			err = activeSession.conn.SetWriteDeadline(deadline)
			if err == nil {
				err = activeSession.conn.WriteMessage(websocket.BinaryMessage, message)
			}
			activeSession.writeMu.Unlock()
		}
		if err != nil {
			activeSession.close("session_closed")
			return
		}
	}
}

func (activeSession *session) close(code string) {
	activeSession.closeOnce.Do(func() {
		if activeSession.outbound != nil {
			activeSession.outbound.close()
		}
		if activeSession.service != nil {
			activeSession.service.detachSession(activeSession, code)
		}
		_ = activeSession.conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.ClosePolicyViolation, code), time.Now().Add(time.Second))
		_ = activeSession.conn.Close()
	})
}

func (service *Service) streamCloseNotificationsLocked(tracked *stream, code string, agent *session) []streamNotification {
	frame := streamCloseEnvelope(tracked.id, code)
	notifications := []streamNotification{{target: tracked.client, envelope: frame}}
	if agent != nil {
		notifications = append(notifications, streamNotification{target: agent, envelope: frame})
	}
	return notifications
}

func (service *Service) dispatchNotifications(notifications []streamNotification) {
	for _, notification := range notifications {
		_ = notification.target.send(notification.envelope)
	}
}

func streamCloseEnvelope(streamID, code string) protocol.Envelope {
	return protocol.Envelope{Version: protocol.Version1, Type: protocol.TypeClose, StreamID: streamID, Payload: encodeRelayError(code)}
}

func encodeRelayError(code string) string {
	if !validErrorCode(code) {
		code = "protocol_error"
	}
	return base64.RawURLEncoding.EncodeToString([]byte(code))
}

func validEnvelopeErrorCode(envelope protocol.Envelope) bool {
	payload, err := envelope.DecodePayload()
	return err == nil && validErrorCode(string(payload))
}

func validErrorCode(code string) bool {
	_, ok := relayErrorCodes[code]
	return ok
}
