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
	maxClosedStreamTombstones     = 128
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
	conn    *websocket.Conn

	writeMu    sync.Mutex
	closeOnce  sync.Once
	registered bool
}

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
	client    *session
	agent     *session
	expiresAt time.Time
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
	connection, err := sessionUpgrader.Upgrade(writer, request, nil)
	if err != nil {
		service.mu.Unlock()
		return
	}
	activeSession := &session{service: service, serial: serial, role: role, conn: connection, registered: true}
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
	service.detachSession(activeSession, "session_closed")
}

func (activeSession *session) readLoop() {
	activeSession.conn.SetReadLimit(maxWebSocketMessageBytes)
	for {
		messageType, raw, err := activeSession.conn.ReadMessage()
		if err != nil {
			return
		}
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
	} else if err := agent.send(forward); err != nil {
		service.removeStreamLocked(tracked)
		errorCode = "agent_unavailable"
	}
	service.mu.Unlock()
	if errorCode != "" {
		service.rejectOpen(client, envelope.StreamID, errorCode)
	}
}

func (service *Service) handleClientStreamFrame(client *session, envelope protocol.Envelope) error {
	if envelope.Type != protocol.TypeData && envelope.Type != protocol.TypeClose {
		return errors.New("role-incompatible client frame")
	}
	service.mu.Lock()
	tracked, exists := service.streams[envelope.StreamID]
	if !exists {
		if service.absorbLateCloseLocked(client, enrollment.RoleClient, envelope, time.Now()) {
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
	if err := agent.send(envelope); err != nil {
		return nil
	}
	if envelope.Type == protocol.TypeData {
		payload, _ := envelope.DecodePayload()
		_ = service.store.addBytes(context.Background(), int64(len(payload)))
	}
	return nil
}

func (service *Service) handleAgentStreamFrame(agent *session, envelope protocol.Envelope) error {
	if envelope.Type != protocol.TypeOpened && envelope.Type != protocol.TypeRejected && envelope.Type != protocol.TypeData && envelope.Type != protocol.TypeClose {
		return errors.New("role-incompatible agent frame")
	}
	service.mu.Lock()
	tracked, exists := service.streams[envelope.StreamID]
	if !exists {
		if service.absorbLateCloseLocked(agent, enrollment.RoleAgent, envelope, time.Now()) {
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
	if envelope.Type == protocol.TypeRejected || envelope.Type == protocol.TypeClose {
		service.removeStreamLocked(tracked)
	}
	service.mu.Unlock()
	if err := client.send(envelope); err != nil {
		return nil
	}
	if envelope.Type == protocol.TypeData {
		payload, _ := envelope.DecodePayload()
		_ = service.store.addBytes(context.Background(), int64(len(payload)))
	}
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
	service.detachSession(activeSession, "protocol_error")
	activeSession.close("protocol_error")
}

func (service *Service) detachSession(activeSession *session, code string) {
	service.mu.Lock()
	notifications := service.detachSessionLocked(activeSession, code)
	service.mu.Unlock()
	service.dispatchNotifications(notifications)
}

func (service *Service) closeIdentitySession(serial, code string) {
	service.mu.Lock()
	activeSession := service.sessions[serial]
	var notifications []streamNotification
	if activeSession != nil {
		notifications = service.detachSessionLocked(activeSession, code)
	}
	service.mu.Unlock()
	if activeSession == nil {
		return
	}
	service.dispatchNotifications(notifications)
	activeSession.close(code)
}

func (service *Service) revokeIdentity(ctx context.Context, serial string, now time.Time) error {
	service.mu.Lock()
	if err := service.store.revokeIdentity(ctx, serial, now); err != nil {
		service.mu.Unlock()
		return err
	}
	activeSession := service.sessions[serial]
	var notifications []streamNotification
	if activeSession != nil {
		notifications = service.detachSessionLocked(activeSession, "revoked")
	}
	service.mu.Unlock()
	service.dispatchNotifications(notifications)
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
		client: tracked.client, agent: tracked.agent, expiresAt: now.Add(closedStreamTombstoneLifetime),
	}
}

func (service *Service) absorbLateCloseLocked(sender *session, role enrollment.Role, envelope protocol.Envelope, now time.Time) bool {
	if envelope.Type != protocol.TypeClose || !validEnvelopeErrorCode(envelope) {
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
		return tombstone.client == sender
	}
	return role == enrollment.RoleAgent && tombstone.agent == sender
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
	service.mu.Unlock()
	for _, code := range expiredCodes {
		_ = service.store.incrementError(context.Background(), code)
	}
	service.dispatchNotifications(notifications)
}

func (activeSession *session) send(envelope protocol.Envelope) error {
	message, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	deadline := time.Now().Add(webSocketWriteTimeout)
	for !activeSession.writeMu.TryLock() {
		if !time.Now().Before(deadline) {
			return errors.New("WebSocket writer unavailable")
		}
		time.Sleep(time.Millisecond)
	}
	defer activeSession.writeMu.Unlock()
	if err := activeSession.conn.SetWriteDeadline(deadline); err != nil {
		return err
	}
	return activeSession.conn.WriteMessage(websocket.BinaryMessage, message)
}

func (activeSession *session) close(code string) {
	activeSession.closeOnce.Do(func() {
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
		notification := notification
		go func() {
			_ = notification.target.send(notification.envelope)
		}()
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
