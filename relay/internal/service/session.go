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

const maxWebSocketMessageBytes = 2 << 20

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
	state           streamState
	openingDeadline time.Time
	lastActivity    time.Time
}

type clientOpenRequest struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

type agentOpenRequest struct {
	IP   string `json:"ip"`
	Port int    `json:"port"`
}

type expiredStream struct {
	stream *stream
	code   string
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
	addresses, err := net.DefaultResolver.LookupNetIP(resolveContext, "ip", target.Host)
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
	if _, exists := service.streams[envelope.StreamID]; exists {
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
		id: envelope.StreamID, client: client, state: streamOpening,
		openingDeadline: now.Add(service.openingTimeout), lastActivity: now,
	}
	service.streams[envelope.StreamID] = tracked
	service.activeStreams++
	service.mu.Unlock()

	if err := service.store.incrementTotalStreams(context.Background()); err != nil {
		service.removeStream(tracked)
		service.rejectOpen(client, envelope.StreamID, "agent_unavailable")
		return
	}
	if err := agent.send(forward); err != nil {
		service.removeStream(tracked)
		service.rejectOpen(client, envelope.StreamID, "agent_unavailable")
	}
}

func (service *Service) handleClientStreamFrame(client *session, envelope protocol.Envelope) error {
	if envelope.Type != protocol.TypeData && envelope.Type != protocol.TypeClose {
		return errors.New("role-incompatible client frame")
	}
	service.mu.Lock()
	tracked, exists := service.streams[envelope.StreamID]
	if !exists || tracked.client != client {
		service.mu.Unlock()
		return errors.New("client does not own stream")
	}
	if envelope.Type == protocol.TypeData && tracked.state != streamOpen {
		service.mu.Unlock()
		return errors.New("data before stream opened")
	}
	agent := service.agent
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
	if !exists || service.agent != agent {
		service.mu.Unlock()
		return errors.New("agent stream not found")
	}
	if envelope.Type == protocol.TypeOpened {
		if tracked.state != streamOpening || envelope.Payload != "" {
			service.mu.Unlock()
			return errors.New("invalid opened frame")
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
	if !activeSession.registered {
		service.mu.Unlock()
		return
	}
	activeSession.registered = false
	delete(service.sessions, activeSession.serial)
	if activeSession.role == enrollment.RoleAgent && service.agent == activeSession {
		service.agent = nil
		service.agentConnected = false
	} else if activeSession.role == enrollment.RoleClient && service.connectedClients > 0 {
		service.connectedClients--
	}
	affected := make([]*stream, 0)
	for _, tracked := range service.streams {
		if activeSession.role == enrollment.RoleAgent || tracked.client == activeSession {
			affected = append(affected, tracked)
			service.removeStreamLocked(tracked)
		}
	}
	agent := service.agent
	service.mu.Unlock()

	for _, tracked := range affected {
		closeFrame := protocol.Envelope{Version: protocol.Version1, Type: protocol.TypeClose, StreamID: tracked.id, Payload: encodeRelayError(code)}
		if tracked.client != activeSession {
			_ = tracked.client.send(closeFrame)
		}
		if agent != nil && agent != activeSession {
			_ = agent.send(closeFrame)
		}
	}
}

func (service *Service) closeIdentitySession(serial, code string) {
	service.mu.RLock()
	activeSession := service.sessions[serial]
	service.mu.RUnlock()
	if activeSession == nil {
		return
	}
	service.detachSession(activeSession, code)
	activeSession.close(code)
}

func (service *Service) removeStream(tracked *stream) {
	service.mu.Lock()
	service.removeStreamLocked(tracked)
	service.mu.Unlock()
}

func (service *Service) removeStreamLocked(tracked *stream) {
	if current, exists := service.streams[tracked.id]; !exists || current != tracked {
		return
	}
	delete(service.streams, tracked.id)
	if service.activeStreams > 0 {
		service.activeStreams--
	}
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
	expired := make([]expiredStream, 0)
	for _, tracked := range service.streams {
		code := ""
		if tracked.state == streamOpening && !now.Before(tracked.openingDeadline) {
			code = "opening_timeout"
		} else if !now.Before(tracked.lastActivity.Add(service.idleTimeout)) {
			code = "idle_timeout"
		}
		if code != "" {
			expired = append(expired, expiredStream{stream: tracked, code: code})
			service.removeStreamLocked(tracked)
		}
	}
	agent := service.agent
	service.mu.Unlock()
	for _, item := range expired {
		_ = service.store.incrementError(context.Background(), item.code)
		frame := protocol.Envelope{Version: protocol.Version1, Type: protocol.TypeClose, StreamID: item.stream.id, Payload: encodeRelayError(item.code)}
		_ = item.stream.client.send(frame)
		if agent != nil {
			_ = agent.send(frame)
		}
	}
}

func (activeSession *session) send(envelope protocol.Envelope) error {
	message, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	activeSession.writeMu.Lock()
	defer activeSession.writeMu.Unlock()
	return activeSession.conn.WriteMessage(websocket.BinaryMessage, message)
}

func (activeSession *session) close(code string) {
	activeSession.closeOnce.Do(func() {
		activeSession.writeMu.Lock()
		_ = activeSession.conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.ClosePolicyViolation, code), time.Now().Add(time.Second))
		_ = activeSession.conn.Close()
		activeSession.writeMu.Unlock()
	})
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
