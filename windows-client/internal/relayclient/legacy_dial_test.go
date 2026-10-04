package relayclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gorilla/websocket"
	"io"
	"mobile-egress/internal/capacity"
	"net/http"
	"time"
)

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
	// Health controls admission, not relay connectivity. A health outage must
	// not prevent an independently authenticated WebSocket connection.
	agentAvailable, healthErr := fetchAgentHealth(ctx, httpClient, baseURL.String())
	agentAvailable = healthErr == nil && agentAvailable
	tlsConfig := transport.TLSClientConfig.Clone()
	webSocketURL := *baseURL
	webSocketURL.Scheme = "wss"
	webSocketURL.Path = "/v1/session"
	webSocketURL.RawQuery = "transport=2"
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
		if response != nil && (response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden) {
			return nil, ErrClientUnauthorized
		}
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
