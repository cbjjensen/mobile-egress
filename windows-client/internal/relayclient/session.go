package relayclient

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

var (
	ErrRelayUnavailable = errors.New("healthy relay agent unavailable")
	ErrStreamLimit      = errors.New("local relay stream limit reached")
)

type RelayError struct{ Code string }

func (err RelayError) Error() string { return "relay rejected stream: " + err.Code }

type SessionStatus struct {
	Connected      bool
	AgentAvailable bool
	ActiveStreams  int
	BytesUp        int64
	BytesDown      int64
}

type Session struct {
	identity  Identity
	conn      *websocket.Conn
	client    *http.Client
	transport *http.Transport

	ctx       context.Context
	cancel    context.CancelFunc
	writeMu   sync.Mutex
	mu        sync.Mutex
	streams   map[string]*relayStream
	connected bool
	agent     bool
	closeOnce sync.Once
	bytesUp   atomic.Int64
	bytesDown atomic.Int64
}

type relayStream struct {
	session    *Session
	id         string
	inbound    chan []byte
	done       chan struct{}
	openResult chan error
	openOnce   sync.Once
	finishOnce sync.Once
	sendClose  sync.Once
	readMu     sync.Mutex
	remaining  []byte
}

func DialSession(ctx context.Context, identity Identity) (*Session, error) {
	if identity.Role != "client" {
		return nil, errors.New("only a client identity may establish a tunnel session")
	}
	baseURL, err := validateRelayURL(identity.RelayURL)
	if err != nil {
		return nil, err
	}
	httpClient, transport, err := identityHTTPClient(identity)
	if err != nil {
		return nil, err
	}
	httpClient.Timeout = 10 * time.Second
	agentAvailable, err := fetchAgentHealth(ctx, httpClient, baseURL.String())
	if err != nil {
		transport.CloseIdleConnections()
		return nil, err
	}
	tlsConfig := transport.TLSClientConfig.Clone()
	webSocketURL := *baseURL
	webSocketURL.Scheme = "wss"
	webSocketURL.Path = "/v1/session"
	dialer := websocket.Dialer{TLSClientConfig: tlsConfig, HandshakeTimeout: 10 * time.Second}
	connection, response, err := dialer.DialContext(ctx, webSocketURL.String(), nil)
	if response != nil && response.Body != nil {
		response.Body.Close()
	}
	if err != nil {
		transport.CloseIdleConnections()
		return nil, fmt.Errorf("connect relay session: %w", err)
	}
	sessionContext, cancel := context.WithCancel(context.Background())
	session := &Session{
		identity: identity, conn: connection, client: httpClient, transport: transport,
		ctx: sessionContext, cancel: cancel, streams: make(map[string]*relayStream),
		connected: true, agent: agentAvailable,
	}
	go session.readLoop()
	go session.healthLoop(baseURL.String())
	return session, nil
}

func (session *Session) Healthy() bool {
	session.mu.Lock()
	healthy := session.connected && session.agent
	session.mu.Unlock()
	return healthy
}

func (session *Session) Status() SessionStatus {
	session.mu.Lock()
	status := SessionStatus{Connected: session.connected, AgentAvailable: session.agent, ActiveStreams: len(session.streams)}
	session.mu.Unlock()
	status.BytesUp = session.bytesUp.Load()
	status.BytesDown = session.bytesDown.Load()
	return status
}

func (session *Session) OpenStream(ctx context.Context, host string, port uint16) (io.ReadWriteCloser, error) {
	if host == "" || port == 0 {
		return nil, errors.New("invalid target")
	}
	session.mu.Lock()
	if !session.connected || !session.agent {
		session.mu.Unlock()
		return nil, ErrRelayUnavailable
	}
	if len(session.streams) >= 4 {
		session.mu.Unlock()
		return nil, ErrStreamLimit
	}
	streamID, err := newStreamID()
	if err != nil {
		session.mu.Unlock()
		return nil, err
	}
	stream := &relayStream{
		session: session, id: streamID, inbound: make(chan []byte, 4),
		done: make(chan struct{}), openResult: make(chan error, 1),
	}
	session.streams[streamID] = stream
	session.mu.Unlock()
	payload, _ := json.Marshal(struct {
		Host string `json:"host"`
		Port uint16 `json:"port"`
	}{Host: host, Port: port})
	if err := session.send(wireEnvelope{
		Version: 1, Type: "open", StreamID: streamID,
		Payload: base64.RawURLEncoding.EncodeToString(payload),
	}); err != nil {
		session.removeStream(streamID)
		stream.finish(err)
		return nil, ErrRelayUnavailable
	}
	select {
	case err := <-stream.openResult:
		if err != nil {
			return nil, err
		}
		return stream, nil
	case <-ctx.Done():
		stream.Close()
		return nil, ctx.Err()
	case <-session.ctx.Done():
		return nil, ErrRelayUnavailable
	}
}

func (session *Session) Close() error {
	session.closeOnce.Do(func() {
		session.cancel()
		_ = session.conn.Close()
		session.transport.CloseIdleConnections()
		session.failAll(ErrRelayUnavailable)
	})
	return nil
}

func (session *Session) readLoop() {
	defer session.Close()
	session.conn.SetReadLimit(2 << 20)
	for {
		messageType, raw, err := session.conn.ReadMessage()
		if err != nil {
			return
		}
		if messageType != websocket.BinaryMessage {
			return
		}
		envelope, err := parseWireEnvelope(raw)
		if err != nil {
			return
		}
		if envelope.Type == "ping" {
			if session.send(wireEnvelope{Version: 1, Type: "pong", StreamID: "", Payload: ""}) != nil {
				return
			}
			continue
		}
		if envelope.Type == "pong" {
			continue
		}
		if envelope.Type != "opened" && envelope.Type != "rejected" && envelope.Type != "data" && envelope.Type != "close" {
			return
		}
		session.mu.Lock()
		stream := session.streams[envelope.StreamID]
		session.mu.Unlock()
		if stream == nil {
			return
		}
		switch envelope.Type {
		case "opened":
			if envelope.Payload != "" {
				return
			}
			stream.resolve(nil)
		case "rejected":
			code, err := decodeRelayCode(envelope.Payload)
			if err != nil {
				return
			}
			if code == "agent_unavailable" {
				session.mu.Lock()
				session.agent = false
				session.mu.Unlock()
			}
			session.removeStream(stream.id)
			stream.finish(RelayError{Code: code})
		case "data":
			payload, err := decodeWirePayload(envelope.Payload)
			if err != nil {
				return
			}
			select {
			case stream.inbound <- payload:
				session.bytesDown.Add(int64(len(payload)))
			case <-stream.done:
			case <-session.ctx.Done():
				return
			}
		case "close":
			if _, err := decodeRelayCode(envelope.Payload); err != nil {
				return
			}
			session.removeStream(stream.id)
			stream.finish(io.EOF)
		}
	}
}

func (session *Session) healthLoop(baseURL string) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(session.ctx, 5*time.Second)
			agent, err := fetchAgentHealth(ctx, session.client, baseURL)
			cancel()
			session.mu.Lock()
			if session.connected {
				session.agent = err == nil && agent
			}
			session.mu.Unlock()
		case <-session.ctx.Done():
			return
		}
	}
}

func (session *Session) send(envelope wireEnvelope) error {
	raw, err := marshalWireEnvelope(envelope)
	if err != nil {
		return err
	}
	session.writeMu.Lock()
	defer session.writeMu.Unlock()
	if err := session.conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	return session.conn.WriteMessage(websocket.BinaryMessage, raw)
}

func (session *Session) removeStream(id string) {
	session.mu.Lock()
	delete(session.streams, id)
	session.mu.Unlock()
}

func (session *Session) failAll(err error) {
	session.mu.Lock()
	session.connected = false
	session.agent = false
	streams := session.streams
	session.streams = make(map[string]*relayStream)
	session.mu.Unlock()
	for _, stream := range streams {
		stream.finish(err)
	}
}

func (stream *relayStream) Read(buffer []byte) (int, error) {
	stream.readMu.Lock()
	defer stream.readMu.Unlock()
	for len(stream.remaining) == 0 {
		select {
		case value := <-stream.inbound:
			stream.remaining = value
		case <-stream.done:
			return 0, io.EOF
		}
	}
	written := copy(buffer, stream.remaining)
	stream.remaining = stream.remaining[written:]
	return written, nil
}

func (stream *relayStream) Write(value []byte) (int, error) {
	select {
	case <-stream.done:
		return 0, io.ErrClosedPipe
	default:
	}
	written := 0
	for len(value) > 0 {
		length := min(len(value), 32<<10)
		chunk := value[:length]
		if err := stream.session.send(wireEnvelope{
			Version: 1, Type: "data", StreamID: stream.id,
			Payload: base64.RawURLEncoding.EncodeToString(chunk),
		}); err != nil {
			stream.session.Close()
			return written, err
		}
		stream.session.bytesUp.Add(int64(length))
		written += length
		value = value[length:]
	}
	return written, nil
}

func (stream *relayStream) Close() error {
	stream.sendClose.Do(func() {
		stream.session.removeStream(stream.id)
		_ = stream.session.send(wireEnvelope{
			Version: 1, Type: "close", StreamID: stream.id,
			Payload: base64.RawURLEncoding.EncodeToString([]byte("client_closed")),
		})
		stream.finish(io.EOF)
	})
	return nil
}

func (stream *relayStream) resolve(err error) {
	stream.openOnce.Do(func() { stream.openResult <- err })
}

func (stream *relayStream) finish(err error) {
	stream.resolve(err)
	stream.finishOnce.Do(func() { close(stream.done) })
}

func newStreamID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func decodeRelayCode(payload string) (string, error) {
	decoded, err := decodeWirePayload(payload)
	if err != nil || len(decoded) == 0 || len(decoded) > 64 || bytes.ContainsAny(decoded, " \t\r\n") {
		return "", errors.New("invalid relay error code")
	}
	return string(decoded), nil
}

func fetchAgentHealth(ctx context.Context, client *http.Client, baseURL string) (bool, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/healthz", nil)
	if err != nil {
		return false, err
	}
	response, err := client.Do(request)
	if err != nil {
		return false, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return false, fmt.Errorf("relay health returned HTTP %d", response.StatusCode)
	}
	var health struct {
		Readiness      bool `json:"readiness"`
		AgentConnected bool `json:"agentConnected"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxControlResponseBytes+1))
	if err := decoder.Decode(&health); err != nil {
		return false, errors.New("relay returned invalid health")
	}
	return health.Readiness && health.AgentConnected, nil
}
