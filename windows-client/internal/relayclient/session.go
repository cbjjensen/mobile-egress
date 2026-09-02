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

	"mobile-egress/internal/capacity"
)

var (
	ErrRelayUnavailable = errors.New("healthy relay agent unavailable")
	ErrStreamLimit      = errors.New("local relay stream limit reached")
)

const (
	MaxConcurrentStreams      = capacity.ClientMaxConcurrentStreams
	maxClosedStreamTombstones = capacity.StreamTombstones
	maxOutboundDataChunkSize  = 16 << 10
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

	ctx           context.Context
	cancel        context.CancelFunc
	writeMu       sync.Mutex
	mu            sync.Mutex
	streams       map[string]*relayStream
	draining      map[string]*relayStream
	closedStreams map[string]struct{}
	closedOrder   []string
	connected     bool
	agent         bool
	closeOnce     sync.Once
	inboundBudget *inboundBudget
	bytesUp       atomic.Int64
	bytesDown     atomic.Int64
}

type inboundBudget struct {
	mu         sync.Mutex
	frameLimit int
	byteLimit  int
	frames     int
	bytes      int
}

type inboundReservation struct {
	budget    *inboundBudget
	stream    *relayStream
	byteCount int
	release   sync.Once
}

type inboundFrame struct {
	payload     []byte
	reservation *inboundReservation
}

type relayStream struct {
	session    *Session
	id         string
	inbound    chan inboundFrame
	done       chan struct{}
	openResult chan error
	// beforeWriteChunk is set only by deterministic concurrency tests.
	beforeWriteChunk func(int)
	openOnce         sync.Once
	finishOnce       sync.Once
	discardOnce      sync.Once
	sendClose        sync.Once
	sendMu           sync.Mutex
	readMu           sync.Mutex
	inboundMu        sync.Mutex
	remaining        []byte
	remainingInbound *inboundReservation
	inboundFrames    int
	terminal         atomic.Bool
	drainInbound     atomic.Bool
}

func newInboundBudget(frameLimit, byteLimit int) *inboundBudget {
	return &inboundBudget{frameLimit: frameLimit, byteLimit: byteLimit}
}

func (budget *inboundBudget) tryReserve(byteCount int) (*inboundReservation, bool) {
	budget.mu.Lock()
	defer budget.mu.Unlock()
	if budget.frames >= budget.frameLimit || byteCount > budget.byteLimit-budget.bytes {
		return nil, false
	}
	budget.frames++
	budget.bytes += byteCount
	return &inboundReservation{budget: budget, byteCount: byteCount}, true
}

func (budget *inboundBudget) outstanding() (int, int) {
	budget.mu.Lock()
	defer budget.mu.Unlock()
	return budget.frames, budget.bytes
}

func (reservation *inboundReservation) refund() {
	if reservation == nil {
		return
	}
	reservation.release.Do(func() {
		if reservation.stream != nil {
			reservation.stream.inboundMu.Lock()
			reservation.stream.inboundFrames--
			reservation.stream.inboundMu.Unlock()
		}
		reservation.budget.mu.Lock()
		reservation.budget.frames--
		reservation.budget.bytes -= reservation.byteCount
		reservation.budget.mu.Unlock()
	})
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
	if transport.DialContext != nil {
		dialer.NetDialContext = transport.DialContext
	}
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
		draining:      make(map[string]*relayStream),
		closedStreams: make(map[string]struct{}),
		inboundBudget: newInboundBudget(capacity.DataFramesPerLane, capacity.DataBytesPerLane),
		connected:     true, agent: agentAvailable,
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
	if len(session.streams) >= MaxConcurrentStreams {
		session.mu.Unlock()
		return nil, ErrStreamLimit
	}
	streamID, err := newStreamID()
	if err != nil {
		session.mu.Unlock()
		return nil, err
	}
	stream := &relayStream{
		session: session, id: streamID, inbound: make(chan inboundFrame, capacity.DataFramesPerStream),
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
		_, locallyClosed := session.closedStreams[envelope.StreamID]
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
			if stream.enqueueInbound(payload) {
				session.bytesDown.Add(int64(len(payload)))
			} else {
				// A single consumer must never stall the WebSocket reader and
				// therefore every other relay stream. The bounded stream is
				// closed with the finite v1 client_closed reason.
				if session.claimLocalClose(stream) {
					stream.finish(io.EOF)
					go func() {
						stream.sendMu.Lock()
						defer stream.sendMu.Unlock()
						_ = session.send(wireEnvelope{
							Version: 1, Type: "close", StreamID: stream.id,
							Payload: base64.RawURLEncoding.EncodeToString([]byte("client_closed")),
						})
					}()
				}
			}
		case "close":
			if _, err := decodeRelayCode(envelope.Payload); err != nil {
				return
			}
			if session.beginInboundDrain(stream) {
				stream.finishAfterInboundDrain()
			}
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

func (session *Session) beginInboundDrain(stream *relayStream) bool {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.streams[stream.id] != stream {
		return false
	}
	delete(session.streams, stream.id)
	if session.draining == nil {
		session.draining = make(map[string]*relayStream)
	}
	session.draining[stream.id] = stream
	return true
}

func (session *Session) forgetInboundDrain(stream *relayStream) {
	session.mu.Lock()
	if session.draining[stream.id] == stream {
		delete(session.draining, stream.id)
	}
	session.mu.Unlock()
}

func (session *Session) claimLocalClose(stream *relayStream) bool {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.streams[stream.id] != stream {
		if session.draining[stream.id] == stream {
			delete(session.draining, stream.id)
		}
		return false
	}
	delete(session.streams, stream.id)
	session.rememberClosedLocked(stream.id)
	return true
}

func (session *Session) rememberClosedLocked(id string) {
	if _, exists := session.closedStreams[id]; exists {
		return
	}
	session.closedStreams[id] = struct{}{}
	session.closedOrder = append(session.closedOrder, id)
	if len(session.closedOrder) > maxClosedStreamTombstones {
		oldest := session.closedOrder[0]
		session.closedOrder = session.closedOrder[1:]
		delete(session.closedStreams, oldest)
	}
}

func (session *Session) failAll(err error) {
	session.mu.Lock()
	session.connected = false
	session.agent = false
	streams := make([]*relayStream, 0, len(session.streams)+len(session.draining))
	for _, stream := range session.streams {
		streams = append(streams, stream)
	}
	for _, stream := range session.draining {
		streams = append(streams, stream)
	}
	session.streams = make(map[string]*relayStream)
	session.draining = make(map[string]*relayStream)
	session.closedStreams = make(map[string]struct{})
	session.closedOrder = nil
	session.mu.Unlock()
	for _, stream := range streams {
		stream.finish(err)
	}
}

func (stream *relayStream) enqueueInbound(payload []byte) bool {
	stream.inboundMu.Lock()
	if stream.terminal.Load() || stream.inboundFrames >= cap(stream.inbound) {
		stream.inboundMu.Unlock()
		return false
	}
	reservation, ok := stream.session.inboundBudget.tryReserve(len(payload))
	if !ok {
		stream.inboundMu.Unlock()
		return false
	}
	reservation.stream = stream
	stream.inboundFrames++
	frame := inboundFrame{payload: payload, reservation: reservation}
	select {
	case stream.inbound <- frame:
		stream.inboundMu.Unlock()
		return true
	default:
		stream.inboundFrames--
		reservation.stream = nil
		stream.inboundMu.Unlock()
		reservation.refund()
		return false
	}
}

func (stream *relayStream) Read(buffer []byte) (int, error) {
	if len(buffer) == 0 {
		return 0, nil
	}
	stream.readMu.Lock()
	defer stream.readMu.Unlock()
	for len(stream.remaining) == 0 {
		stream.releaseRemainingInbound()
		if stream.terminal.Load() && !stream.drainInbound.Load() {
			return 0, io.EOF
		}
		if stream.drainInbound.Load() {
			select {
			case frame := <-stream.inbound:
				stream.remaining = frame.payload
				stream.remainingInbound = frame.reservation
				continue
			default:
				stream.completeInboundDrain()
				return 0, io.EOF
			}
		}
		select {
		case frame := <-stream.inbound:
			if stream.terminal.Load() && !stream.drainInbound.Load() {
				frame.reservation.refund()
				return 0, io.EOF
			}
			stream.remaining = frame.payload
			stream.remainingInbound = frame.reservation
		case <-stream.done:
			if stream.drainInbound.Load() {
				continue
			}
			return 0, io.EOF
		}
	}
	written := copy(buffer, stream.remaining)
	stream.remaining = stream.remaining[written:]
	if len(stream.remaining) == 0 {
		stream.releaseRemainingInbound()
		if stream.drainInbound.Load() && len(stream.inbound) == 0 {
			stream.completeInboundDrain()
		}
	}
	return written, nil
}

func (stream *relayStream) releaseRemainingInbound() {
	stream.remainingInbound.refund()
	stream.remainingInbound = nil
}

func (stream *relayStream) completeInboundDrain() {
	stream.session.forgetInboundDrain(stream)
}

func (stream *relayStream) Write(value []byte) (int, error) {
	if len(value) == 0 {
		select {
		case <-stream.done:
			return 0, io.ErrClosedPipe
		default:
			return 0, nil
		}
	}
	written := 0
	for len(value) > 0 {
		if stream.beforeWriteChunk != nil {
			stream.beforeWriteChunk(written / maxOutboundDataChunkSize)
		}
		stream.sendMu.Lock()
		select {
		case <-stream.done:
			stream.sendMu.Unlock()
			return written, io.ErrClosedPipe
		default:
		}
		length := min(len(value), maxOutboundDataChunkSize)
		chunk := value[:length]
		err := stream.session.send(wireEnvelope{
			Version: 1, Type: "data", StreamID: stream.id,
			Payload: base64.RawURLEncoding.EncodeToString(chunk),
		})
		if err == nil {
			stream.session.bytesUp.Add(int64(length))
			written += length
			value = value[length:]
		}
		stream.sendMu.Unlock()
		if err != nil {
			stream.session.Close()
			return written, err
		}
	}
	return written, nil
}

func (stream *relayStream) Close() error {
	stream.sendClose.Do(func() {
		stream.sendMu.Lock()
		defer stream.sendMu.Unlock()
		if stream.session.claimLocalClose(stream) {
			_ = stream.session.send(wireEnvelope{
				Version: 1, Type: "close", StreamID: stream.id,
				Payload: base64.RawURLEncoding.EncodeToString([]byte("client_closed")),
			})
		}
		stream.finish(io.EOF)
	})
	return nil
}

func (stream *relayStream) resolve(err error) {
	stream.openOnce.Do(func() { stream.openResult <- err })
}

func (stream *relayStream) finish(err error) {
	stream.signalTerminal(err, false)
	stream.discardInbound()
}

func (stream *relayStream) finishAfterInboundDrain() {
	stream.signalTerminal(io.EOF, true)
}

func (stream *relayStream) signalTerminal(err error, drain bool) {
	stream.resolve(err)
	stream.finishOnce.Do(func() {
		stream.inboundMu.Lock()
		stream.drainInbound.Store(drain)
		stream.terminal.Store(true)
		close(stream.done)
		stream.inboundMu.Unlock()
	})
}

func (stream *relayStream) discardInbound() {
	stream.discardOnce.Do(func() {
		stream.inboundMu.Lock()
		stream.drainInbound.Store(false)
		var discarded []*inboundReservation
		for {
			select {
			case frame := <-stream.inbound:
				discarded = append(discarded, frame.reservation)
			default:
				stream.inboundMu.Unlock()
				for _, reservation := range discarded {
					reservation.refund()
				}
				stream.readMu.Lock()
				stream.remaining = nil
				stream.releaseRemainingInbound()
				stream.readMu.Unlock()
				stream.session.forgetInboundDrain(stream)
				return
			}
		}
	})
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
