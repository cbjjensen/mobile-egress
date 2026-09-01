//go:build capacityharness

package capacityharness

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"mobile-egress/pairing"
	"mobile-egress/windows-client/internal/relayclient"
)

const (
	maximumHarnessSessionStreams = holderStreams + 1
	maximumHarnessPayloadBytes   = 1 << 20
	maximumHarnessDataBytes      = 32 << 10
	maximumHarnessInboundFrames  = 8
	maximumHarnessTombstones     = 128
	maximumControlResponseBytes  = 256 << 10
	defaultSessionCloseTimeout   = 5 * time.Second
)

var harnessRelayCodes = map[string]struct{}{
	"agent_stream_limit": {}, "agent_unavailable": {}, "client_closed": {},
	"client_stream_limit": {}, "connect_failed": {}, "idle_timeout": {},
	"opening_timeout": {}, "policy_denied": {}, "protocol_error": {},
	"target_closed": {},
}

type ProductionSessionDialer struct{}

func (ProductionSessionDialer) Dial(ctx context.Context, credential *ClientCredential) (CapacitySession, error) {
	return dialCapacitySession(ctx, credential)
}

type capacityWireEnvelope struct {
	Version  int    `json:"version"`
	Type     string `json:"type"`
	StreamID string `json:"streamId"`
	Payload  string `json:"payload"`
}

type capacitySession struct {
	connection      *websocket.Conn
	transport       *http.Transport
	ctx             context.Context
	cancel          context.CancelFunc
	writeGate       chan struct{}
	mu              sync.Mutex
	streams         map[string]*capacityStream
	closed          map[string]struct{}
	closedOrder     []string
	connected       bool
	closeOnce       sync.Once
	readDone        chan struct{}
	afterDisconnect func()
}

type capacityStream struct {
	session      *capacitySession
	id           string
	inbound      chan []byte
	done         chan struct{}
	openResult   chan error
	ctx          context.Context
	cancel       context.CancelFunc
	openOnce     sync.Once
	finishOnce   sync.Once
	closeOnce    sync.Once
	closeErr     error
	stateMu      sync.Mutex
	terminal     bool
	closeClaimed bool
	readMu       sync.Mutex
	remaining    []byte
}

func dialCapacitySession(ctx context.Context, credential *ClientCredential) (*capacitySession, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	baseURL, tlsConfig, dialContext, err := capacitySessionTransport(credential)
	if err != nil {
		return nil, CategorizedError{Category: FailureTLS, cause: err}
	}
	transport := &http.Transport{TLSClientConfig: tlsConfig.Clone(), DialContext: dialContext}
	httpClient := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	if err := requireAvailableAgent(ctx, httpClient, baseURL.String()); err != nil {
		transport.CloseIdleConnections()
		return nil, CategorizedError{Category: FailureSession, cause: err}
	}
	webSocketURL := *baseURL
	webSocketURL.Scheme = "wss"
	webSocketURL.Path = "/v1/session"
	dialer := websocket.Dialer{TLSClientConfig: tlsConfig.Clone(), HandshakeTimeout: 10 * time.Second, NetDialContext: dialContext}
	connection, response, err := dialer.DialContext(ctx, webSocketURL.String(), nil)
	if response != nil && response.Body != nil {
		response.Body.Close()
	}
	if err != nil {
		transport.CloseIdleConnections()
		category := FailureSession
		if response != nil && (response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden) {
			category = FailureAuthentication
		}
		return nil, CategorizedError{Category: category, cause: err}
	}
	sessionCtx, cancel := context.WithCancel(context.Background())
	session := &capacitySession{
		connection: connection, transport: transport, ctx: sessionCtx, cancel: cancel,
		streams: make(map[string]*capacityStream), closed: make(map[string]struct{}), connected: true,
		writeGate: make(chan struct{}, 1), readDone: make(chan struct{}),
	}
	go session.readLoop()
	return session, nil
}

func capacitySessionTransport(credential *ClientCredential) (*url.URL, *tls.Config, func(context.Context, string, string) (net.Conn, error), error) {
	if credential == nil || credential.Role != "client" || !validIdentitySerial(credential.Serial) || len(credential.PrivateKeyPEM) == 0 {
		return nil, nil, nil, errors.New("capacity Client identity is invalid")
	}
	baseURL, err := url.Parse(strings.TrimSpace(credential.RelayURL))
	if err != nil || baseURL.Scheme != "https" || baseURL.Host == "" || baseURL.User != nil || baseURL.RawQuery != "" || baseURL.Fragment != "" || (baseURL.Path != "" && baseURL.Path != "/") {
		return nil, nil, nil, errors.New("capacity relay URL is invalid")
	}
	baseURL.Path = ""
	certificate, err := tls.X509KeyPair([]byte(credential.CertificatePEM), credential.PrivateKeyPEM)
	if err != nil || len(certificate.Certificate) == 0 {
		return nil, nil, nil, errors.New("capacity Client key pair is invalid")
	}
	leaf, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil || strings.ToUpper(leaf.SerialNumber.Text(16)) != strings.ToUpper(credential.Serial) {
		return nil, nil, nil, errors.New("capacity Client certificate is invalid")
	}
	ca, err := pairing.CACertificate(credential.CACertificatePEM)
	if err != nil || !ca.IsCA || !ca.BasicConstraintsValid || ca.KeyUsage&x509.KeyUsageCertSign == 0 {
		return nil, nil, nil, errors.New("capacity relay CA is invalid")
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	intermediates := x509.NewCertPool()
	for _, raw := range certificate.Certificate[1:] {
		candidate, parseErr := x509.ParseCertificate(raw)
		if parseErr != nil {
			return nil, nil, nil, errors.New("capacity Client certificate chain is invalid")
		}
		if !candidate.Equal(ca) {
			intermediates.AddCert(candidate)
		}
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		return nil, nil, nil, errors.New("capacity Client certificate is invalid")
	}
	certificate.Leaf = leaf
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{certificate}, RootCAs: roots, ServerName: baseURL.Hostname(),
		MinVersion: tls.VersionTLS13, MaxVersion: tls.VersionTLS13,
	}
	var dialContext func(context.Context, string, string) (net.Conn, error)
	if credential.DialAddress != "" {
		host, port, splitErr := net.SplitHostPort(credential.DialAddress)
		if splitErr != nil || host != "127.0.0.1" || port != "8443" {
			return nil, nil, nil, errors.New("capacity relay dial override is invalid")
		}
		dialer := &net.Dialer{Timeout: 10 * time.Second}
		dialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "tcp4", credential.DialAddress)
		}
	}
	return baseURL, tlsConfig, dialContext, nil
}

func requireAvailableAgent(ctx context.Context, client *http.Client, baseURL string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/healthz", nil)
	if err != nil {
		return errors.New("capacity relay health request is invalid")
	}
	response, err := client.Do(request)
	if err != nil {
		return errors.New("capacity relay health is unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maximumControlResponseBytes))
		return errors.New("capacity relay health is unavailable")
	}
	var health relayclient.RelayHealth
	decoder := json.NewDecoder(io.LimitReader(response.Body, maximumControlResponseBytes+1))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&health) != nil || decoder.Decode(&struct{}{}) != io.EOF || !health.Readiness || !health.AgentConnected ||
		health.ConnectedClients < 0 || health.ActiveStreams < 0 || health.TotalStreams < 0 || health.ByteCount < 0 || health.ErrorCounts == nil {
		return errors.New("capacity relay health is invalid")
	}
	return nil
}

func (session *capacitySession) OpenStream(ctx context.Context, host string, port uint16) (CapacityStream, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if !validPublicHostname(host) || port != 443 {
		return nil, errors.New("capacity target is invalid")
	}
	session.mu.Lock()
	if !session.connected {
		session.mu.Unlock()
		return nil, CategorizedError{Category: FailureSession}
	}
	if len(session.streams) >= maximumHarnessSessionStreams {
		session.mu.Unlock()
		return nil, errors.New("capacity harness pending stream limit reached")
	}
	streamID, err := newCapacityStreamID()
	if err != nil {
		session.mu.Unlock()
		return nil, errors.New("capacity stream ID generation failed")
	}
	streamCtx, cancelStream := context.WithCancel(session.ctx)
	stream := &capacityStream{
		session: session, id: streamID, inbound: make(chan []byte, maximumHarnessInboundFrames),
		done: make(chan struct{}), openResult: make(chan error, 1), ctx: streamCtx, cancel: cancelStream,
	}
	session.streams[streamID] = stream
	session.mu.Unlock()
	payload, _ := json.Marshal(struct {
		Host string `json:"host"`
		Port uint16 `json:"port"`
	}{Host: host, Port: port})
	if err := session.send(ctx, capacityWireEnvelope{Version: 1, Type: "open", StreamID: streamID, Payload: base64.RawURLEncoding.EncodeToString(payload)}); err != nil {
		session.removeStream(streamID, true)
		stream.finish(err)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, CategorizedError{Category: FailureSession, cause: err}
	}
	select {
	case openErr := <-stream.openResult:
		if openErr != nil {
			return nil, openErr
		}
		return stream, nil
	case <-ctx.Done():
		_ = stream.Close()
		return nil, ctx.Err()
	case <-session.ctx.Done():
		return nil, CategorizedError{Category: FailureSession}
	}
}

func (session *capacitySession) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), defaultSessionCloseTimeout)
	defer cancel()
	return session.CloseContext(ctx)
}

func (session *capacitySession) closeLocal() {
	session.closeOnce.Do(func() {
		session.mu.Lock()
		session.connected = false
		session.cancel()
		afterDisconnect := session.afterDisconnect
		session.mu.Unlock()
		if afterDisconnect != nil {
			afterDisconnect()
		}
		if session.connection != nil {
			_ = session.connection.Close()
		}
		if session.transport != nil {
			session.transport.CloseIdleConnections()
		}
		session.failAll(CategorizedError{Category: FailureSession})
	})
}

func (session *capacitySession) CloseContext(ctx context.Context) error {
	// Closing a Gorilla WebSocket closes the local network connection and does
	// not wait for a peer response. Join the one owned reader directly without
	// adding a cleanup watcher or another goroutine.
	if ctx == nil {
		ctx = context.Background()
	}
	session.closeLocal()
	if session.readDone == nil {
		return nil
	}
	select {
	case <-session.readDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (session *capacitySession) readLoop() {
	defer close(session.readDone)
	defer session.closeLocal()
	session.connection.SetReadLimit(2 << 20)
	for {
		messageType, raw, err := session.connection.ReadMessage()
		if err != nil || messageType != websocket.BinaryMessage {
			return
		}
		envelope, err := parseCapacityWireEnvelope(raw)
		if err != nil {
			return
		}
		if envelope.Type == "ping" {
			if session.send(session.ctx, capacityWireEnvelope{Version: 1, Type: "pong"}) != nil {
				return
			}
			continue
		}
		if envelope.Type == "pong" {
			continue
		}
		session.mu.Lock()
		stream := session.streams[envelope.StreamID]
		_, locallyClosed := session.closed[envelope.StreamID]
		session.mu.Unlock()
		if stream == nil {
			if locallyClosed {
				continue
			}
			return
		}
		switch envelope.Type {
		case "opened":
			if envelope.Payload != "" {
				return
			}
			stream.resolve(nil)
		case "rejected":
			code, codeErr := decodeCapacityRelayCode(envelope.Payload)
			if codeErr != nil {
				return
			}
			session.removeStream(stream.id, false)
			stream.finish(RelayRejection{Code: code})
		case "data":
			payload, payloadErr := decodeCapacityPayload(envelope.Payload)
			if payloadErr != nil {
				return
			}
			select {
			case stream.inbound <- payload:
			case <-stream.done:
			default:
				return
			}
		case "close":
			if _, codeErr := decodeCapacityRelayCode(envelope.Payload); codeErr != nil {
				return
			}
			session.removeStream(stream.id, false)
			stream.finish(io.EOF)
		default:
			return
		}
	}
}

func (session *capacitySession) send(ctx context.Context, envelope capacityWireEnvelope) error {
	raw, err := marshalCapacityWireEnvelope(envelope)
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case session.writeGate <- struct{}{}:
		defer func() { <-session.writeGate }()
	case <-ctx.Done():
		return ctx.Err()
	case <-session.ctx.Done():
		return io.ErrClosedPipe
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-session.ctx.Done():
		return io.ErrClosedPipe
	default:
	}
	deadline := time.Now().Add(5 * time.Second)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := session.connection.SetWriteDeadline(deadline); err != nil {
		return err
	}
	return session.connection.WriteMessage(websocket.BinaryMessage, raw)
}

func (session *capacitySession) removeStream(id string, remember bool) {
	session.mu.Lock()
	delete(session.streams, id)
	if remember {
		if _, exists := session.closed[id]; !exists {
			session.closed[id] = struct{}{}
			session.closedOrder = append(session.closedOrder, id)
			if len(session.closedOrder) > maximumHarnessTombstones {
				oldest := session.closedOrder[0]
				session.closedOrder = session.closedOrder[1:]
				delete(session.closed, oldest)
			}
		}
	}
	session.mu.Unlock()
}

func (session *capacitySession) failAll(err error) {
	session.mu.Lock()
	session.connected = false
	streams := session.streams
	session.streams = make(map[string]*capacityStream)
	session.mu.Unlock()
	for _, stream := range streams {
		stream.finish(err)
	}
}

func (stream *capacityStream) Read(buffer []byte) (int, error) {
	stream.readMu.Lock()
	defer stream.readMu.Unlock()
	for len(stream.remaining) == 0 {
		select {
		case payload := <-stream.inbound:
			stream.remaining = payload
		case <-stream.done:
			return 0, io.EOF
		}
	}
	written := copy(buffer, stream.remaining)
	stream.remaining = stream.remaining[written:]
	return written, nil
}

func (stream *capacityStream) Write(value []byte) (int, error) {
	select {
	case <-stream.done:
		return 0, io.ErrClosedPipe
	default:
	}
	written := 0
	for len(value) > 0 {
		length := min(len(value), echoPayloadBytes)
		if err := stream.session.send(stream.ctx, capacityWireEnvelope{
			Version: 1, Type: "data", StreamID: stream.id,
			Payload: base64.RawURLEncoding.EncodeToString(value[:length]),
		}); err != nil {
			stream.session.Close()
			return written, err
		}
		written += length
		value = value[length:]
	}
	return written, nil
}

func (stream *capacityStream) Close() error {
	stream.closeOnce.Do(func() {
		stream.session.removeStream(stream.id, true)
		stream.finish(io.EOF)
		closeCtx, cancelClose := context.WithTimeout(stream.session.ctx, 5*time.Second)
		defer cancelClose()
		stream.closeErr = stream.session.send(closeCtx, capacityWireEnvelope{
			Version: 1, Type: "close", StreamID: stream.id,
			Payload: base64.RawURLEncoding.EncodeToString([]byte("client_closed")),
		})
	})
	return stream.closeErr
}

func (stream *capacityStream) Done() <-chan struct{} { return stream.done }
func (stream *capacityStream) TryBeginClose() bool {
	if stream.session != nil {
		stream.session.mu.Lock()
		defer stream.session.mu.Unlock()
		if !stream.session.connected || stream.session.ctx == nil || stream.session.ctx.Err() != nil {
			return false
		}
	}
	stream.stateMu.Lock()
	defer stream.stateMu.Unlock()
	if stream.terminal {
		return false
	}
	stream.closeClaimed = true
	return true
}
func (stream *capacityStream) resolve(err error) {
	stream.openOnce.Do(func() { stream.openResult <- err })
}
func (stream *capacityStream) finish(err error) {
	stream.resolve(err)
	stream.finishOnce.Do(func() {
		stream.stateMu.Lock()
		stream.terminal = true
		stream.cancel()
		close(stream.done)
		stream.stateMu.Unlock()
	})
}

func newCapacityStreamID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func parseCapacityWireEnvelope(raw []byte) (capacityWireEnvelope, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var envelope capacityWireEnvelope
	if decoder.Decode(&envelope) != nil || decoder.Decode(&struct{}{}) != io.EOF || envelope.Version != 1 || !validCapacityWireType(envelope.Type) {
		return capacityWireEnvelope{}, errors.New("capacity relay envelope is invalid")
	}
	keepalive := envelope.Type == "ping" || envelope.Type == "pong"
	if keepalive != (envelope.StreamID == "") {
		return capacityWireEnvelope{}, errors.New("capacity relay envelope is invalid")
	}
	payloadLimit := maximumHarnessPayloadBytes
	if envelope.Type == "data" {
		payloadLimit = maximumHarnessDataBytes
	}
	if _, err := decodeCapacityPayloadLimit(envelope.Payload, payloadLimit); err != nil {
		return capacityWireEnvelope{}, err
	}
	return envelope, nil
}

func marshalCapacityWireEnvelope(envelope capacityWireEnvelope) ([]byte, error) {
	if envelope.Version != 1 || !validCapacityWireType(envelope.Type) {
		return nil, errors.New("capacity relay envelope is invalid")
	}
	return json.Marshal(envelope)
}

func validCapacityWireType(value string) bool {
	switch value {
	case "open", "opened", "rejected", "data", "close", "ping", "pong":
		return true
	default:
		return false
	}
}

func decodeCapacityPayload(encoded string) ([]byte, error) {
	return decodeCapacityPayloadLimit(encoded, maximumHarnessPayloadBytes)
}

func decodeCapacityPayloadLimit(encoded string, maximumBytes int) ([]byte, error) {
	if base64.RawURLEncoding.DecodedLen(len(encoded)) > maximumBytes {
		return nil, errors.New("capacity relay payload is invalid")
	}
	payload, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil || len(payload) > maximumBytes {
		return nil, errors.New("capacity relay payload is invalid")
	}
	return payload, nil
}

func decodeCapacityRelayCode(encoded string) (string, error) {
	payload, err := decodeCapacityPayload(encoded)
	if err != nil || len(payload) == 0 || len(payload) > 64 || bytes.ContainsAny(payload, " \t\r\n") {
		return "", errors.New("capacity relay error code is invalid")
	}
	code := string(payload)
	if _, allowed := harnessRelayCodes[code]; !allowed {
		return "", errors.New("capacity relay error code is invalid")
	}
	return code, nil
}
