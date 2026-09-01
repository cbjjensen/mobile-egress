package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"mobile-egress/relay/internal/enrollment"
	"mobile-egress/relay/internal/protocol"
)

func TestBlockedAgentWriterDoesNotHoldServiceMutexOrBlockAnotherClientOpen(t *testing.T) {
	fixture := newRelayFixture(t)
	defer fixture.Close()
	_, devices := enrollDevices(t, fixture, "client", "client", "agent")

	blockedConnection := newRecordingSessionConnection(true)
	agent := newSession(fixture.service, devices[2].serial, enrollment.RoleAgent, blockedConnection)
	clientOne := newSession(fixture.service, devices[0].serial, enrollment.RoleClient, newRecordingSessionConnection(false))
	clientTwo := newSession(fixture.service, devices[1].serial, enrollment.RoleClient, newRecordingSessionConnection(false))
	registerTestSessions(fixture.service, agent, clientOne, clientTwo)
	defer closeTestSessions(agent, clientOne, clientTwo)

	firstDone := make(chan struct{})
	go func() {
		fixture.service.handleClientOpen(clientOne, openEnvelope("blocked-agent-one", "1.1.1.1", 443))
		close(firstDone)
	}()
	waitForSignal(t, blockedConnection.writeStarted, "blocked Agent write")
	waitForSignal(t, firstDone, "first Client open routing")

	secondDone := make(chan struct{})
	go func() {
		fixture.service.handleClientOpen(clientTwo, openEnvelope("blocked-agent-two", "8.8.8.8", 443))
		close(secondDone)
	}()
	waitForSignal(t, secondDone, "second Client open routing")
	if !fixture.service.mu.TryLock() {
		t.Fatal("blocked Agent writer held the global service mutex")
	}
	fixture.service.mu.Unlock()
}

func TestBlockedClientWriterDoesNotHoldServiceMutexOrBlockAnotherClientRoute(t *testing.T) {
	fixture := newRelayFixture(t)
	defer fixture.Close()
	_, devices := enrollDevices(t, fixture, "client", "client", "agent")

	blockedConnection := newRecordingSessionConnection(true)
	peerConnection := newRecordingSessionConnection(false)
	agent := newSession(fixture.service, devices[2].serial, enrollment.RoleAgent, newRecordingSessionConnection(false))
	clientOne := newSession(fixture.service, devices[0].serial, enrollment.RoleClient, blockedConnection)
	clientTwo := newSession(fixture.service, devices[1].serial, enrollment.RoleClient, peerConnection)
	registerTestSessions(fixture.service, agent, clientOne, clientTwo)
	defer closeTestSessions(agent, clientOne, clientTwo)
	fixture.service.mu.Lock()
	fixture.service.streams["blocked-client"] = &stream{id: "blocked-client", client: clientOne, agent: agent, state: streamOpen, lastActivity: time.Now()}
	fixture.service.streams["peer-client"] = &stream{id: "peer-client", client: clientTwo, agent: agent, state: streamOpen, lastActivity: time.Now()}
	fixture.service.activeStreams = 2
	fixture.service.mu.Unlock()

	firstDone := make(chan error, 1)
	go func() {
		firstDone <- fixture.service.handleAgentStreamFrame(agent, dataEnvelope("blocked-client", "YQ"))
	}()
	waitForSignal(t, blockedConnection.writeStarted, "blocked Client write")
	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatalf("first route returned an error: %v", err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("blocked Client writer stalled its routing call")
	}

	if err := fixture.service.handleAgentStreamFrame(agent, dataEnvelope("peer-client", "Yg")); err != nil {
		t.Fatalf("peer route returned an error: %v", err)
	}
	if received := readRecordedEnvelope(t, peerConnection); received.StreamID != "peer-client" || received.Type != protocol.TypeData {
		t.Fatalf("peer Client received %#v, want peer data", received)
	}
	if !fixture.service.mu.TryLock() {
		t.Fatal("blocked Client writer held the global service mutex")
	}
	fixture.service.mu.Unlock()
}

func TestDataSaturationClosesOnlyAffectedStream(t *testing.T) {
	fixture := newRelayFixture(t)
	defer fixture.Close()
	_, devices := enrollDevices(t, fixture, "client", "agent")

	clientConnection := newRecordingSessionConnection(true)
	agentConnection := newRecordingSessionConnection(false)
	client := newSession(fixture.service, devices[0].serial, enrollment.RoleClient, clientConnection)
	agent := newSession(fixture.service, devices[1].serial, enrollment.RoleAgent, agentConnection)
	registerTestSessions(fixture.service, agent, client)
	defer closeTestSessions(agent, client)
	fixture.service.mu.Lock()
	fixture.service.streams["saturated"] = &stream{id: "saturated", client: client, agent: agent, state: streamOpen, lastActivity: time.Now()}
	fixture.service.streams["peer"] = &stream{id: "peer", client: client, agent: agent, state: streamOpen, lastActivity: time.Now()}
	fixture.service.activeStreams = 2
	fixture.service.mu.Unlock()

	if err := fixture.service.handleAgentStreamFrame(agent, dataEnvelope("saturated", "YQ")); err != nil {
		t.Fatal(err)
	}
	waitForSignal(t, clientConnection.writeStarted, "first saturated-stream write")
	for _, payload := range []string{"Yg", "Yw", "ZA"} {
		if err := fixture.service.handleAgentStreamFrame(agent, dataEnvelope("saturated", payload)); err != nil {
			t.Fatalf("saturated stream route returned an error: %v", err)
		}
	}

	closeFrame := readRecordedEnvelope(t, agentConnection)
	if closeFrame.Type != protocol.TypeClose || closeFrame.StreamID != "saturated" || decodedErrorCode(t, closeFrame) != "agent_unavailable" {
		t.Fatalf("Agent received %#v, want saturated-stream agent_unavailable close", closeFrame)
	}
	fixture.service.mu.RLock()
	_, saturatedExists := fixture.service.streams["saturated"]
	_, peerExists := fixture.service.streams["peer"]
	clientRegistered := fixture.service.sessions[client.serial] == client
	fixture.service.mu.RUnlock()
	if saturatedExists || !peerExists || !clientRegistered {
		t.Fatalf("post-saturation state = saturated:%t peer:%t client:%t, want false/true/true", saturatedExists, peerExists, clientRegistered)
	}

	if err := fixture.service.handleAgentStreamFrame(agent, dataEnvelope("peer", "cGVlcg")); err != nil {
		t.Fatalf("peer route returned an error after saturation: %v", err)
	}
	clientConnection.release()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case envelope := <-clientConnection.writes:
			if envelope.StreamID == "peer" && envelope.Type == protocol.TypeData {
				return
			}
		case <-deadline:
			t.Fatal("peer data was not delivered after saturated stream failed")
		}
	}
}

func TestRequiredControlSaturationTerminatesOnlyAffectedSession(t *testing.T) {
	service := newWriterTestService()
	blockedConnection := newRecordingSessionConnection(true)
	peerConnection := newRecordingSessionConnection(false)
	blocked := newSession(service, "blocked", enrollment.RoleClient, blockedConnection)
	peer := newSession(service, "peer", enrollment.RoleClient, peerConnection)
	registerTestSessions(service, blocked, peer)
	defer closeTestSessions(blocked, peer)

	if err := blocked.send(protocol.Envelope{Version: 1, Type: protocol.TypePong}); err != nil {
		t.Fatalf("initial control enqueue returned an error: %v", err)
	}
	waitForSignal(t, blockedConnection.writeStarted, "blocked control write")
	for index := 0; index < 64; index++ {
		envelope := protocol.Envelope{Version: 1, Type: protocol.TypeClose, StreamID: fmt.Sprintf("control-%d", index), Payload: encodeRelayError("client_closed")}
		if err := blocked.send(envelope); err != nil {
			t.Fatalf("control enqueue %d returned an error: %v", index+1, err)
		}
	}
	if err := blocked.send(protocol.Envelope{Version: 1, Type: protocol.TypePong}); err == nil {
		t.Fatal("required-control overflow was accepted")
	}
	waitForSignal(t, blockedConnection.closed, "saturated session close")
	service.mu.RLock()
	blockedRegistered := service.sessions[blocked.serial] == blocked
	peerRegistered := service.sessions[peer.serial] == peer
	service.mu.RUnlock()
	if blockedRegistered || !peerRegistered {
		t.Fatalf("session registration after control saturation = blocked:%t peer:%t, want false/true", blockedRegistered, peerRegistered)
	}
	if err := peer.send(protocol.Envelope{Version: 1, Type: protocol.TypePong}); err != nil {
		t.Fatalf("peer control enqueue returned an error: %v", err)
	}
	if envelope := readRecordedEnvelope(t, peerConnection); envelope.Type != protocol.TypePong {
		t.Fatalf("peer received %#v, want pong", envelope)
	}
}

func TestSessionWriterFailureTerminatesOnlyAffectedSession(t *testing.T) {
	service := newWriterTestService()
	failingConnection := newRecordingSessionConnection(false)
	failingConnection.failWrites.Store(true)
	peerConnection := newRecordingSessionConnection(false)
	failing := newSession(service, "failing", enrollment.RoleClient, failingConnection)
	peer := newSession(service, "peer", enrollment.RoleClient, peerConnection)
	registerTestSessions(service, failing, peer)
	defer closeTestSessions(failing, peer)

	if err := failing.send(protocol.Envelope{Version: 1, Type: protocol.TypePong}); err != nil {
		t.Fatalf("failed writer enqueue returned an error: %v", err)
	}
	waitForSignal(t, failingConnection.closed, "failed writer session close")
	service.mu.RLock()
	failingRegistered := service.sessions[failing.serial] == failing
	peerRegistered := service.sessions[peer.serial] == peer
	service.mu.RUnlock()
	if failingRegistered || !peerRegistered {
		t.Fatalf("session registration after writer failure = failing:%t peer:%t, want false/true", failingRegistered, peerRegistered)
	}
	if err := peer.send(protocol.Envelope{Version: 1, Type: protocol.TypePong}); err != nil {
		t.Fatalf("peer control enqueue returned an error: %v", err)
	}
	_ = readRecordedEnvelope(t, peerConnection)
}

func TestMassTeardownDoesNotCreateGoroutinePerNotification(t *testing.T) {
	service := newWriterTestService()
	agent := newSession(service, "agent", enrollment.RoleAgent, newRecordingSessionConnection(false))
	clients := make([]*session, 0, 256)
	connections := make([]*recordingSessionConnection, 0, 256)
	service.mu.Lock()
	agent.registered = true
	service.agent = agent
	service.sessions[agent.serial] = agent
	for index := 0; index < 256; index++ {
		connection := newRecordingSessionConnection(true)
		client := newSession(service, fmt.Sprintf("client-%d", index), enrollment.RoleClient, connection)
		client.registered = true
		service.sessions[client.serial] = client
		tracked := &stream{id: fmt.Sprintf("stream-%d", index), client: client, agent: agent, state: streamOpen, lastActivity: time.Now()}
		service.streams[tracked.id] = tracked
		service.activeStreams++
		clients = append(clients, client)
		connections = append(connections, connection)
	}
	service.mu.Unlock()
	defer func() {
		closeTestSessions(agent)
		closeTestSessions(clients...)
	}()

	baseline := runtime.NumGoroutine()
	service.detachSession(agent, "session_closed")
	for _, connection := range connections {
		waitForSignal(t, connection.writeStarted, "teardown notification write")
	}
	after := runtime.NumGoroutine()
	if added := after - baseline; added > 8 {
		t.Fatalf("mass teardown added %d goroutines, want no per-notification fan-out", added)
	}
}

func dataEnvelope(streamID, payload string) protocol.Envelope {
	return protocol.Envelope{Version: 1, Type: protocol.TypeData, StreamID: streamID, Payload: payload}
}

func newWriterTestService() *Service {
	return &Service{
		sessions: make(map[string]*session), streams: make(map[string]*stream),
		closedStreams: make(map[string]closedStreamTombstone),
	}
}

func registerTestSessions(service *Service, sessions ...*session) {
	service.mu.Lock()
	defer service.mu.Unlock()
	for _, activeSession := range sessions {
		activeSession.registered = true
		service.sessions[activeSession.serial] = activeSession
		if activeSession.role == enrollment.RoleAgent {
			service.agent = activeSession
			service.agentConnected = true
		} else {
			service.connectedClients++
		}
	}
}

func closeTestSessions(sessions ...*session) {
	for _, activeSession := range sessions {
		activeSession.close("session_closed")
	}
}

func readRecordedEnvelope(t *testing.T, connection *recordingSessionConnection) protocol.Envelope {
	t.Helper()
	select {
	case envelope := <-connection.writes:
		return envelope
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for recorded session write")
	}
	return protocol.Envelope{}
}

type recordingSessionConnection struct {
	blockWrites   bool
	releaseWrites chan struct{}
	writeStarted  chan struct{}
	writes        chan protocol.Envelope
	closed        chan struct{}
	closeOnce     sync.Once
	failWrites    atomic.Bool
	activeWrites  atomic.Int32
	maximumWrites atomic.Int32
}

func newRecordingSessionConnection(blockWrites bool) *recordingSessionConnection {
	return &recordingSessionConnection{
		blockWrites: blockWrites, releaseWrites: make(chan struct{}), writeStarted: make(chan struct{}, 1),
		writes: make(chan protocol.Envelope, 1024), closed: make(chan struct{}),
	}
}

func (connection *recordingSessionConnection) SetReadLimit(int64) {}

func (connection *recordingSessionConnection) ReadMessage() (int, []byte, error) {
	return 0, nil, errors.New("recording connection has no reader")
}

func (connection *recordingSessionConnection) SetWriteDeadline(time.Time) error { return nil }

func (connection *recordingSessionConnection) WriteMessage(_ int, raw []byte) error {
	active := connection.activeWrites.Add(1)
	defer connection.activeWrites.Add(-1)
	for {
		maximum := connection.maximumWrites.Load()
		if active <= maximum || connection.maximumWrites.CompareAndSwap(maximum, active) {
			break
		}
	}
	select {
	case connection.writeStarted <- struct{}{}:
	default:
	}
	if connection.blockWrites {
		select {
		case <-connection.releaseWrites:
		case <-connection.closed:
			return errors.New("recording connection closed")
		}
	}
	if connection.failWrites.Load() {
		return errors.New("injected session writer failure")
	}
	var envelope protocol.Envelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return err
	}
	select {
	case connection.writes <- envelope:
		return nil
	case <-connection.closed:
		return errors.New("recording connection closed")
	}
}

func (connection *recordingSessionConnection) WriteControl(int, []byte, time.Time) error { return nil }

func (connection *recordingSessionConnection) Close() error {
	connection.closeOnce.Do(func() { close(connection.closed) })
	return nil
}

func (connection *recordingSessionConnection) release() {
	select {
	case <-connection.releaseWrites:
	default:
		close(connection.releaseWrites)
	}
}
