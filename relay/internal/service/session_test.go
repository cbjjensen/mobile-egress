package service

import (
	"crypto"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"mobile-egress/relay/internal/protocol"
)

type enrolledDevice struct {
	key         crypto.Signer
	certificate string
	serial      string
	client      *http.Client
}

func TestSessionRequiresActiveClientOrAgentCertificateAndOneAgent(t *testing.T) {
	t.Parallel()

	fixture := newRelayFixture(t)
	defer fixture.Close()
	owner, devices := enrollDevices(t, fixture, "client", "agent", "agent")

	if connection, response, err := dialSession(fixture, owner.client); err == nil || response == nil || response.StatusCode != http.StatusForbidden {
		if connection != nil {
			connection.Close()
		}
		t.Fatalf("owner session result = response %#v, error %v; want 403", response, err)
	}

	clientConnection, response, err := dialSession(fixture, devices[0].client)
	if err != nil {
		t.Fatalf("client session failed: response %#v, error %v", response, err)
	}
	defer clientConnection.Close()
	agentConnection, response, err := dialSession(fixture, devices[1].client)
	if err != nil {
		t.Fatalf("agent session failed: response %#v, error %v", response, err)
	}
	defer agentConnection.Close()

	if secondAgent, response, err := dialSession(fixture, devices[2].client); err == nil || response == nil || response.StatusCode != http.StatusConflict {
		if secondAgent != nil {
			secondAgent.Close()
		}
		t.Fatalf("second agent session result = response %#v, error %v; want 409", response, err)
	}

	if status := postRevocation(t, owner.client, fixture.server.URL, devices[0].serial); status != http.StatusNoContent {
		t.Fatalf("active client revocation status = %d, want 204", status)
	}
	clientConnection.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := clientConnection.ReadMessage(); err == nil {
		clientConnection.SetReadDeadline(time.Now().Add(2 * time.Second))
		if _, _, err := clientConnection.ReadMessage(); err == nil {
			t.Fatal("revocation did not close the active client session")
		}
	}
	if revokedConnection, response, err := dialSession(fixture, devices[0].client); err == nil || response == nil || response.StatusCode != http.StatusUnauthorized {
		if revokedConnection != nil {
			revokedConnection.Close()
		}
		t.Fatalf("revoked session result = response %#v, error %v; want 401", response, err)
	}
}

func TestStaleAgentSessionExpiresAndReleasesSlotForReconnect(t *testing.T) {
	fixture := newRelayFixture(t)
	defer fixture.Close()
	_, devices := enrollDevices(t, fixture, "agent", "agent")

	stale := mustDialSession(t, fixture, devices[0].client)
	defer stale.Close()

	fixture.service.mu.Lock()
	fixture.service.agent.lastInbound = time.Now().Add(-agentSessionLivenessTimeout)
	fixture.service.mu.Unlock()
	fixture.service.expireStreams(time.Now())

	replacement, response, err := dialSession(fixture, devices[1].client)
	if err != nil {
		if response != nil {
			_ = response.Body.Close()
		}
		t.Fatalf("replacement Agent session failed: response %#v, error %v", response, err)
	}
	defer replacement.Close()

	fixture.service.mu.RLock()
	activeAgent := fixture.service.agent
	fixture.service.mu.RUnlock()
	if activeAgent == nil || activeAgent.serial != devices[1].serial {
		t.Fatal("stale Agent remained registered after liveness expiry")
	}
}

func TestClientOpenResolvesPublicTargetAndRoutesOnlyOwningStream(t *testing.T) {
	t.Parallel()

	fixture := newRelayFixture(t)
	defer fixture.Close()
	_, devices := enrollDevices(t, fixture, "client", "client", "agent")
	clientOne := mustDialSession(t, fixture, devices[0].client)
	defer clientOne.Close()
	clientTwo := mustDialSession(t, fixture, devices[1].client)
	defer clientTwo.Close()
	agent := mustDialSession(t, fixture, devices[2].client)
	defer agent.Close()

	writeEnvelope(t, clientOne, openEnvelope("owned-stream", "1.1.1.1", 443))
	forwarded := readEnvelope(t, agent)
	if forwarded.Type != protocol.TypeOpen || forwarded.StreamID != "owned-stream" {
		t.Fatalf("agent received %#v, want owned-stream open", forwarded)
	}
	forwardedPayload, err := forwarded.DecodePayload()
	if err != nil {
		t.Fatal(err)
	}
	var target map[string]any
	if err := json.Unmarshal(forwardedPayload, &target); err != nil {
		t.Fatal(err)
	}
	if target["ip"] != "1.1.1.1" || target["port"] != float64(443) || len(target) != 2 {
		t.Fatalf("agent open target = %#v, want only resolved IP and port", target)
	}

	writeEnvelope(t, agent, protocol.Envelope{Version: 1, Type: protocol.TypeOpened, StreamID: "owned-stream", Payload: ""})
	if opened := readEnvelope(t, clientOne); opened.Type != protocol.TypeOpened {
		t.Fatalf("owning client received %q, want opened", opened.Type)
	}
	clientTwo.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
	if _, _, err := clientTwo.ReadMessage(); err == nil {
		t.Fatal("non-owning client received another client's stream frame")
	}

	data := base64.RawURLEncoding.EncodeToString([]byte("abc"))
	writeEnvelope(t, clientOne, protocol.Envelope{Version: 1, Type: protocol.TypeData, StreamID: "owned-stream", Payload: data})
	if received := readEnvelope(t, agent); received.Payload != data || received.StreamID != "owned-stream" {
		t.Fatalf("agent received wrong data envelope: %#v", received)
	}
	writeEnvelope(t, agent, protocol.Envelope{Version: 1, Type: protocol.TypeClose, StreamID: "owned-stream", Payload: encodeErrorCode("target_closed")})
	if closed := readEnvelope(t, clientOne); closed.Type != protocol.TypeClose || decodedErrorCode(t, closed) != "target_closed" {
		t.Fatalf("owning client received wrong close: %#v", closed)
	}
}

func TestLateStreamFrameFromWrongClientIsAProtocolViolation(t *testing.T) {
	t.Parallel()

	for _, lateFrame := range []protocol.Envelope{
		{Version: 1, Type: protocol.TypeClose, Payload: encodeErrorCode("client_closed")},
		{Version: 1, Type: protocol.TypeData, Payload: base64.RawURLEncoding.EncodeToString([]byte("wrong-owner-late-data"))},
	} {
		lateFrame := lateFrame
		t.Run(string(lateFrame.Type), func(t *testing.T) {
			fixture := newRelayFixture(t)
			defer fixture.Close()
			_, devices := enrollDevices(t, fixture, "client", "client", "agent")
			owner := mustDialSession(t, fixture, devices[0].client)
			defer owner.Close()
			wrongClient := mustDialSession(t, fixture, devices[1].client)
			defer wrongClient.Close()
			agent := mustDialSession(t, fixture, devices[2].client)
			defer agent.Close()

			writeEnvelope(t, owner, openEnvelope("closed-owner-stream", "1.1.1.1", 443))
			if forwarded := readEnvelope(t, agent); forwarded.StreamID != "closed-owner-stream" {
				t.Fatalf("agent received wrong open: %#v", forwarded)
			}
			writeEnvelope(t, owner, protocol.Envelope{
				Version: 1, Type: protocol.TypeClose, StreamID: "closed-owner-stream",
				Payload: encodeErrorCode("client_closed"),
			})
			if closed := readEnvelope(t, agent); closed.Type != protocol.TypeClose {
				t.Fatalf("agent received %#v, want owner close", closed)
			}

			lateFrame.StreamID = "closed-owner-stream"
			writeEnvelope(t, wrongClient, lateFrame)
			wrongClient.SetReadDeadline(time.Now().Add(2 * time.Second))
			if _, _, err := wrongClient.ReadMessage(); err == nil {
				t.Fatalf("wrong client remained connected after sending late %s for another client's tombstone", lateFrame.Type)
			}

			writeEnvelope(t, owner, openEnvelope("owner-still-usable", "8.8.8.8", 443))
			if forwarded := readEnvelope(t, agent); forwarded.StreamID != "owner-still-usable" || forwarded.Type != protocol.TypeOpen {
				t.Fatalf("correct client/agent sessions were damaged: %#v", forwarded)
			}
		})
	}
}

func TestLateDataForClosedStreamFromOriginalOwnerIsAbsorbed(t *testing.T) {
	t.Parallel()

	fixture := newRelayFixture(t)
	defer fixture.Close()
	_, devices := enrollDevices(t, fixture, "client", "agent")
	client := mustDialSession(t, fixture, devices[0].client)
	defer client.Close()
	agent := mustDialSession(t, fixture, devices[1].client)
	defer agent.Close()

	writeEnvelope(t, client, openEnvelope("terminal-stream", "1.1.1.1", 443))
	_ = readEnvelope(t, agent)
	writeEnvelope(t, agent, protocol.Envelope{Version: 1, Type: protocol.TypeOpened, StreamID: "terminal-stream", Payload: ""})
	_ = readEnvelope(t, client)
	writeEnvelope(t, client, protocol.Envelope{
		Version: 1, Type: protocol.TypeClose, StreamID: "terminal-stream",
		Payload: encodeErrorCode("client_closed"),
	})
	_ = readEnvelope(t, agent)
	writeEnvelope(t, client, protocol.Envelope{
		Version: 1, Type: protocol.TypeData, StreamID: "terminal-stream",
		Payload: base64.RawURLEncoding.EncodeToString([]byte("late-data")),
	})
	writeEnvelope(t, client, openEnvelope("owner-still-usable-after-late-data", "8.8.8.8", 443))
	agent.SetReadDeadline(time.Now().Add(2 * time.Second))
	if forwarded := readEnvelope(t, agent); forwarded.Type != protocol.TypeOpen || forwarded.StreamID != "owner-still-usable-after-late-data" {
		t.Fatalf("agent received %#v, want next stream open after absorbed late data", forwarded)
	}

	writeEnvelope(t, agent, protocol.Envelope{Version: 1, Type: protocol.TypePing})
	if pong := readEnvelope(t, agent); pong.Type != protocol.TypePong {
		t.Fatalf("agent received %#v, want pong after client protocol violation", pong)
	}
}

func TestLateAgentOpeningOutcomeLeavesUnrelatedStreamUsable(t *testing.T) {
	t.Parallel()

	for _, outcome := range []protocol.Envelope{
		{Version: 1, Type: protocol.TypeOpened, Payload: ""},
		{Version: 1, Type: protocol.TypeRejected, Payload: encodeErrorCode("target_failure")},
	} {
		outcome := outcome
		t.Run(string(outcome.Type), func(t *testing.T) {
			fixture := newRelayFixture(t)
			defer fixture.Close()
			_, devices := enrollDevices(t, fixture, "client", "agent")
			client := mustDialSession(t, fixture, devices[0].client)
			defer client.Close()
			agent := mustDialSession(t, fixture, devices[1].client)
			defer agent.Close()

			writeEnvelope(t, client, openEnvelope("unrelated-open-stream", "1.1.1.1", 443))
			_ = readEnvelope(t, agent)
			writeEnvelope(t, agent, protocol.Envelope{Version: 1, Type: protocol.TypeOpened, StreamID: "unrelated-open-stream", Payload: ""})
			_ = readEnvelope(t, client)

			writeEnvelope(t, client, openEnvelope("canceled-opening-stream", "8.8.8.8", 443))
			_ = readEnvelope(t, agent)
			writeEnvelope(t, client, protocol.Envelope{
				Version: 1, Type: protocol.TypeClose, StreamID: "canceled-opening-stream",
				Payload: encodeErrorCode("client_closed"),
			})
			if closed := readEnvelope(t, agent); closed.Type != protocol.TypeClose || closed.StreamID != "canceled-opening-stream" {
				t.Fatalf("agent received %#v, want canceled opening close", closed)
			}

			outcome.StreamID = "canceled-opening-stream"
			writeEnvelope(t, agent, outcome)
			if outcome.Type == protocol.TypeOpened {
				writeEnvelope(t, agent, protocol.Envelope{
					Version: 1, Type: protocol.TypeData, StreamID: "canceled-opening-stream",
					Payload: base64.RawURLEncoding.EncodeToString([]byte("already-in-flight")),
				})
			}
			writeEnvelope(t, agent, protocol.Envelope{
				Version: 1, Type: protocol.TypeData, StreamID: "unrelated-open-stream",
				Payload: base64.RawURLEncoding.EncodeToString([]byte("still-usable")),
			})
			client.SetReadDeadline(time.Now().Add(2 * time.Second))
			if data := readEnvelope(t, client); data.Type != protocol.TypeData || data.StreamID != "unrelated-open-stream" {
				t.Fatalf("client received %#v, want unrelated stream data after late %s", data, outcome.Type)
			}
		})
	}
}

func TestSessionEnforcesPerClientAndAgentWideStreamLimits(t *testing.T) {
	t.Parallel()

	fixture := newRelayFixture(t)
	defer fixture.Close()
	_, devices := enrollDevices(t, fixture,
		"client", "client", "client", "client", "client", "client", "client", "client", "client", "agent",
	)
	clients := make([]*websocket.Conn, 0, 9)
	for index := 0; index < 9; index++ {
		client := mustDialSession(t, fixture, devices[index].client)
		defer client.Close()
		clients = append(clients, client)
	}
	clientOne := clients[0]
	agent := mustDialSession(t, fixture, devices[9].client)
	defer agent.Close()

	for index := 0; index < 32; index++ {
		streamID := "client-one-" + string(rune('a'+index))
		writeEnvelope(t, clientOne, openEnvelope(streamID, "1.1.1.1", 443))
		if forwarded := readEnvelope(t, agent); forwarded.StreamID != streamID || forwarded.Type != protocol.TypeOpen {
			t.Fatalf("agent received wrong open: %#v", forwarded)
		}
	}
	writeEnvelope(t, clientOne, openEnvelope("client-one-over-limit", "1.1.1.1", 443))
	if rejected := readEnvelope(t, clientOne); rejected.Type != protocol.TypeRejected || decodedErrorCode(t, rejected) != "client_stream_limit" {
		t.Fatalf("per-client limit response = %#v", rejected)
	}

	writeEnvelope(t, clientOne, protocol.Envelope{
		Version: 1, Type: protocol.TypeClose, StreamID: "client-one-a", Payload: encodeErrorCode("client_closed"),
	})
	if forwarded := readEnvelope(t, agent); forwarded.StreamID != "client-one-a" || forwarded.Type != protocol.TypeClose {
		t.Fatalf("agent received wrong close: %#v", forwarded)
	}
	writeEnvelope(t, clientOne, openEnvelope("client-one-reused-slot", "1.1.1.1", 443))
	if forwarded := readEnvelope(t, agent); forwarded.StreamID != "client-one-reused-slot" || forwarded.Type != protocol.TypeOpen {
		t.Fatalf("agent received wrong replacement open: %#v", forwarded)
	}

	for clientIndex := 1; clientIndex < 8; clientIndex++ {
		for streamIndex := 0; streamIndex < 32; streamIndex++ {
			streamID := fmt.Sprintf("client-%d-%d", clientIndex+1, streamIndex+1)
			writeEnvelope(t, clients[clientIndex], openEnvelope(streamID, "1.1.1.1", 443))
			if forwarded := readEnvelope(t, agent); forwarded.StreamID != streamID || forwarded.Type != protocol.TypeOpen {
				t.Fatalf("agent received wrong open: %#v", forwarded)
			}
		}
	}
	writeEnvelope(t, clients[8], openEnvelope("agent-over-limit", "1.1.1.1", 443))
	if rejected := readEnvelope(t, clients[8]); rejected.Type != protocol.TypeRejected || decodedErrorCode(t, rejected) != "agent_stream_limit" {
		t.Fatalf("agent-wide limit response = %#v", rejected)
	}
}

func TestSessionRoutesThirtyTwoKiBDataAndRetainsOversizeProtocolRejection(t *testing.T) {
	t.Parallel()

	fixture := newRelayFixture(t)
	defer fixture.Close()
	_, devices := enrollDevices(t, fixture, "client", "agent")
	client := mustDialSession(t, fixture, devices[0].client)
	defer client.Close()
	agent := mustDialSession(t, fixture, devices[1].client)
	defer agent.Close()

	writeEnvelope(t, client, openEnvelope("frame-boundary", "1.1.1.1", 443))
	if opened := readEnvelope(t, agent); opened.Type != protocol.TypeOpen || opened.StreamID != "frame-boundary" {
		t.Fatalf("Agent received %#v, want frame-boundary open", opened)
	}
	writeEnvelope(t, agent, protocol.Envelope{Version: 1, Type: protocol.TypeOpened, StreamID: "frame-boundary"})
	if opened := readEnvelope(t, client); opened.Type != protocol.TypeOpened || opened.StreamID != "frame-boundary" {
		t.Fatalf("Client received %#v, want frame-boundary opened", opened)
	}

	payload := strings.Repeat("x", 32<<10)
	writeEnvelope(t, client, protocol.Envelope{
		Version: 1, Type: protocol.TypeData, StreamID: "frame-boundary",
		Payload: base64.RawURLEncoding.EncodeToString([]byte(payload)),
	})
	forwarded := readEnvelope(t, agent)
	decoded, err := forwarded.DecodePayload()
	if err != nil {
		t.Fatalf("decode forwarded 32 KiB frame: %v", err)
	}
	if forwarded.Type != protocol.TypeData || forwarded.StreamID != "frame-boundary" || len(decoded) != 32<<10 {
		t.Fatalf("forwarded 32 KiB frame = %s/%s/%d bytes", forwarded.Type, forwarded.StreamID, len(decoded))
	}

	writeEnvelope(t, client, protocol.Envelope{
		Version: 1, Type: protocol.TypeData, StreamID: "frame-boundary",
		Payload: base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("z", 32<<10+1))),
	})
	client.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := client.ReadMessage(); err == nil {
		t.Fatal("Client remained connected after sending an over-limit payload")
	} else {
		var closeError *websocket.CloseError
		if !errors.As(err, &closeError) || closeError.Code != websocket.ClosePolicyViolation || closeError.Text != "protocol_error" {
			t.Fatalf("over-limit payload close = %v, want policy-violation protocol_error", err)
		}
	}
}

func TestSessionRejectsAgentDataOverThirtyTwoKiB(t *testing.T) {
	t.Parallel()

	fixture := newRelayFixture(t)
	defer fixture.Close()
	_, devices := enrollDevices(t, fixture, "client", "agent")
	client := mustDialSession(t, fixture, devices[0].client)
	defer client.Close()
	agent := mustDialSession(t, fixture, devices[1].client)
	defer agent.Close()

	writeEnvelope(t, client, openEnvelope("agent-frame-boundary", "1.1.1.1", 443))
	_ = readEnvelope(t, agent)
	writeEnvelope(t, agent, protocol.Envelope{Version: 1, Type: protocol.TypeOpened, StreamID: "agent-frame-boundary"})
	_ = readEnvelope(t, client)
	writeEnvelope(t, agent, protocol.Envelope{
		Version: 1, Type: protocol.TypeData, StreamID: "agent-frame-boundary",
		Payload: base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("z", 32<<10+1))),
	})
	agent.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := agent.ReadMessage(); err == nil {
		t.Fatal("Agent remained connected after sending a data payload larger than 32 KiB")
	} else {
		var closeError *websocket.CloseError
		if !errors.As(err, &closeError) || closeError.Code != websocket.ClosePolicyViolation || closeError.Text != "protocol_error" {
			t.Fatalf("over-limit Agent payload close = %v, want policy-violation protocol_error", err)
		}
	}
}

func TestSessionRejectsNonPublicResolvedTargetsWithoutForwarding(t *testing.T) {
	t.Parallel()

	fixture := newRelayFixture(t)
	defer fixture.Close()
	_, devices := enrollDevices(t, fixture, "client", "agent")
	client := mustDialSession(t, fixture, devices[0].client)
	defer client.Close()
	agent := mustDialSession(t, fixture, devices[1].client)
	defer agent.Close()

	writeEnvelope(t, client, openEnvelope("blocked", "localhost", 80))
	rejected := readEnvelope(t, client)
	if rejected.Type != protocol.TypeRejected || decodedErrorCode(t, rejected) != "policy_denied" {
		t.Fatalf("policy response = %#v, want policy_denied rejection", rejected)
	}
	agent.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
	if _, _, err := agent.ReadMessage(); err == nil {
		t.Fatal("agent received a blocked destination")
	}

	healthResponse, err := fixture.server.Client().Get(fixture.server.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer healthResponse.Body.Close()
	var health struct {
		ErrorCounts map[string]int64 `json:"errorCounts"`
	}
	if err := json.NewDecoder(healthResponse.Body).Decode(&health); err != nil {
		t.Fatal(err)
	}
	if health.ErrorCounts["policy_denied"] != 1 {
		t.Fatalf("policy error count = %d, want 1", health.ErrorCounts["policy_denied"])
	}
}

func TestAggregateMetricsPersistWithoutDestinationOrPayloadData(t *testing.T) {
	t.Parallel()

	fixture := newRelayFixture(t)
	defer fixture.Close()
	_, devices := enrollDevices(t, fixture, "client", "agent")
	client := mustDialSession(t, fixture, devices[0].client)
	defer client.Close()
	agent := mustDialSession(t, fixture, devices[1].client)
	defer agent.Close()

	writeEnvelope(t, client, openEnvelope("metrics-stream", "8.8.8.8", 443))
	_ = readEnvelope(t, agent)
	writeEnvelope(t, agent, protocol.Envelope{Version: 1, Type: protocol.TypeOpened, StreamID: "metrics-stream", Payload: ""})
	_ = readEnvelope(t, client)
	writeEnvelope(t, client, protocol.Envelope{Version: 1, Type: protocol.TypeData, StreamID: "metrics-stream", Payload: base64.RawURLEncoding.EncodeToString([]byte("secret-payload"))})
	_ = readEnvelope(t, agent)
	writeEnvelope(t, agent, protocol.Envelope{Version: 1, Type: protocol.TypeClose, StreamID: "metrics-stream", Payload: encodeErrorCode("target_closed")})
	_ = readEnvelope(t, client)

	healthResponse, err := fixture.server.Client().Get(fixture.server.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer healthResponse.Body.Close()
	var health struct {
		ActiveStreams int   `json:"activeStreams"`
		TotalStreams  int64 `json:"totalStreams"`
		ByteCount     int64 `json:"byteCount"`
	}
	if err := json.NewDecoder(healthResponse.Body).Decode(&health); err != nil {
		t.Fatal(err)
	}
	if health.ActiveStreams != 0 || health.TotalStreams != 1 || health.ByteCount != int64(len("secret-payload")) {
		t.Fatalf("aggregate health = %#v", health)
	}

	for _, name := range []string{"state.db", "state.db-wal"} {
		contents, err := os.ReadFile(filepath.Join(fixture.stateDir, name))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"8.8.8.8", "secret-payload", "metrics-stream"} {
			if strings.Contains(string(contents), forbidden) {
				t.Fatalf("%s persisted forbidden stream detail %q", name, forbidden)
			}
		}
	}
}

func enrollDevices(t *testing.T, fixture *relayFixture, roles ...string) (enrolledDevice, []enrolledDevice) {
	t.Helper()
	ownerKey, ownerCSR := newDeviceCSR(t)
	status, ownerIdentity := postEnrollment(t, fixture.server.Client(), fixture.server.URL, fixture.ownerCode, "owner", ownerCSR)
	if status != http.StatusCreated {
		t.Fatalf("owner enrollment status = %d", status)
	}
	owner := enrolledDevice{key: ownerKey, certificate: ownerIdentity.CertificatePEM, serial: ownerIdentity.Serial}
	owner.client = fixture.authenticatedClient(t, owner.key, owner.certificate)

	devices := make([]enrolledDevice, 0, len(roles))
	for _, role := range roles {
		status, pairing := postPairing(t, owner.client, fixture.server.URL, role)
		if status != http.StatusCreated {
			t.Fatalf("%s pairing status = %d", role, status)
		}
		key, csr := newDeviceCSR(t)
		status, identity := postEnrollment(t, fixture.server.Client(), fixture.server.URL, pairing.Code, role, csr)
		if status != http.StatusCreated {
			t.Fatalf("%s enrollment status = %d", role, status)
		}
		device := enrolledDevice{key: key, certificate: identity.CertificatePEM, serial: identity.Serial}
		device.client = fixture.authenticatedClient(t, device.key, device.certificate)
		devices = append(devices, device)
	}
	return owner, devices
}

func dialSession(fixture *relayFixture, client *http.Client) (*websocket.Conn, *http.Response, error) {
	transport := client.Transport.(*http.Transport)
	dialer := websocket.Dialer{TLSClientConfig: transport.TLSClientConfig.Clone()}
	return dialer.Dial("wss"+strings.TrimPrefix(fixture.server.URL, "https")+"/v1/session", nil)
}

func mustDialSession(t *testing.T, fixture *relayFixture, client *http.Client) *websocket.Conn {
	t.Helper()
	connection, response, err := dialSession(fixture, client)
	if err != nil {
		t.Fatalf("dial relay session: response %#v, error %v", response, err)
	}
	return connection
}

func openEnvelope(streamID, host string, port int) protocol.Envelope {
	payload, _ := json.Marshal(map[string]any{"host": host, "port": port})
	return protocol.Envelope{
		Version: 1, Type: protocol.TypeOpen, StreamID: streamID,
		Payload: base64.RawURLEncoding.EncodeToString(payload),
	}
}

func writeEnvelope(t *testing.T, connection *websocket.Conn, envelope protocol.Envelope) {
	t.Helper()
	message, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if err := connection.WriteMessage(websocket.BinaryMessage, message); err != nil {
		t.Fatalf("write WebSocket envelope: %v", err)
	}
}

func readEnvelope(t *testing.T, connection *websocket.Conn) protocol.Envelope {
	t.Helper()
	connection.SetReadDeadline(time.Now().Add(2 * time.Second))
	messageType, message, err := connection.ReadMessage()
	if err != nil {
		t.Fatalf("read WebSocket envelope: %v", err)
	}
	if messageType != websocket.BinaryMessage {
		t.Fatalf("WebSocket message type = %d, want binary", messageType)
	}
	envelope, err := protocol.ParseEnvelope(message)
	if err != nil {
		t.Fatalf("parse WebSocket envelope: %v", err)
	}
	return envelope
}

func encodeErrorCode(code string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(code))
}

func decodedErrorCode(t *testing.T, envelope protocol.Envelope) string {
	t.Helper()
	payload, err := envelope.DecodePayload()
	if err != nil {
		t.Fatal(err)
	}
	return string(payload)
}
