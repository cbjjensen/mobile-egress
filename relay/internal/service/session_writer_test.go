package service

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"mobile-egress/relay/internal/enrollment"
	"mobile-egress/relay/internal/protocol"
)

func TestBlockedSessionUpgradeDoesNotHoldServiceMutex(t *testing.T) {
	fixture := newRelayFixture(t)
	defer fixture.Close()
	_, devices := enrollDevices(t, fixture, "client")
	writer := newBlockingUpgradeResponseWriter()
	defer writer.release()
	request := authenticatedSessionRequest(t, devices[0])

	done := make(chan struct{})
	go func() {
		fixture.service.handleSession(writer, request)
		close(done)
	}()
	waitForSignal(t, writer.connection.writeStarted, "blocked WebSocket handshake write")
	if !fixture.service.mu.TryLock() {
		t.Fatal("blocked WebSocket handshake held the global service mutex")
	}
	fixture.service.mu.Unlock()
	writer.release()
	waitForSignal(t, done, "blocked session upgrade completion")
}

func TestPendingSessionUpgradeRejectsDuplicateWithoutWaiting(t *testing.T) {
	fixture := newRelayFixture(t)
	defer fixture.Close()
	_, devices := enrollDevices(t, fixture, "client")
	writer := newBlockingUpgradeResponseWriter()
	defer writer.release()
	request := authenticatedSessionRequest(t, devices[0])

	firstDone := make(chan struct{})
	go func() {
		fixture.service.handleSession(writer, request)
		close(firstDone)
	}()
	waitForSignal(t, writer.connection.writeStarted, "pending WebSocket handshake")

	duplicate := httptest.NewRecorder()
	duplicateRequest := authenticatedSessionRequest(t, devices[0])
	duplicateDone := make(chan struct{})
	go func() {
		fixture.service.handleSession(duplicate, duplicateRequest)
		close(duplicateDone)
	}()
	select {
	case <-duplicateDone:
		if duplicate.Code != http.StatusConflict {
			t.Fatalf("duplicate pending session status = %d, want 409", duplicate.Code)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("duplicate pending session waited for the first WebSocket handshake")
	}

	writer.release()
	waitForSignal(t, firstDone, "first pending session completion")
}

func TestPendingAgentUpgradeReservesSingleAgentSlot(t *testing.T) {
	fixture := newRelayFixture(t)
	defer fixture.Close()
	_, devices := enrollDevices(t, fixture, "agent", "agent")
	writer := newBlockingUpgradeResponseWriter()
	defer writer.release()
	firstRequest := authenticatedSessionRequest(t, devices[0])
	secondRequest := authenticatedSessionRequest(t, devices[1])

	firstDone := make(chan struct{})
	go func() {
		fixture.service.handleSession(writer, firstRequest)
		close(firstDone)
	}()
	waitForSignal(t, writer.connection.writeStarted, "pending Agent WebSocket handshake")

	second := httptest.NewRecorder()
	secondDone := make(chan struct{})
	go func() {
		fixture.service.handleSession(second, secondRequest)
		close(secondDone)
	}()
	select {
	case <-secondDone:
		if second.Code != http.StatusConflict {
			t.Fatalf("second pending Agent status = %d, want 409", second.Code)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("second Agent waited for the pending Agent WebSocket handshake")
	}

	writer.release()
	waitForSignal(t, firstDone, "first pending Agent session completion")
}

func TestFailedSessionUpgradeReleasesPendingReservation(t *testing.T) {
	fixture := newRelayFixture(t)
	defer fixture.Close()
	_, devices := enrollDevices(t, fixture, "client")

	for attempt := 1; attempt <= 2; attempt++ {
		writer := httptest.NewRecorder()
		fixture.service.handleSession(writer, authenticatedSessionRequest(t, devices[0]))
		if writer.Code != http.StatusInternalServerError {
			t.Fatalf("failed upgrade attempt %d status = %d, want 500 instead of a retained reservation", attempt, writer.Code)
		}
	}
}

func TestClientOpenWaitsForPendingAgentWithoutHoldingServiceMutex(t *testing.T) {
	fixture := newRelayFixture(t)
	defer fixture.Close()
	_, devices := enrollDevices(t, fixture, "client", "agent")
	client := newDormantSession(fixture.service, devices[0].serial, enrollment.RoleClient)
	registerTestSessions(fixture.service, client)
	agentWriter := newBlockingUpgradeResponseWriter()
	defer agentWriter.release()
	agentRequest := authenticatedSessionRequest(t, devices[1])

	agentDone := make(chan struct{})
	go func() {
		fixture.service.handleSession(agentWriter, agentRequest)
		close(agentDone)
	}()
	waitForSignal(t, agentWriter.connection.writeStarted, "pending Agent upgrade")

	openDone := make(chan struct{})
	go func() {
		fixture.service.handleClientOpen(client, openEnvelope("pending-agent-open", "1.1.1.1", 443))
		close(openDone)
	}()
	openFinishedEarly := false
	select {
	case <-openDone:
		openFinishedEarly = true
	case <-time.After(250 * time.Millisecond):
	}
	if !fixture.service.mu.TryLock() {
		t.Fatal("Client open waiting for a pending Agent held the global service mutex")
	}
	fixture.service.mu.Unlock()
	agentWriter.release()
	waitForSignal(t, agentDone, "pending Agent handler completion")
	if !openFinishedEarly {
		waitForSignal(t, openDone, "Client open after pending Agent completion")
	}
	if openFinishedEarly {
		t.Fatal("Client open was rejected before the pending Agent handshake completed")
	}
}

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

func TestSessionWriterPrioritizesControlAndRoundRobinsStreamData(t *testing.T) {
	service := newWriterTestService()
	connection := newRecordingSessionConnection(true)
	activeSession := newSession(service, "ordered-writer", enrollment.RoleAgent, connection)
	registerTestSessions(service, activeSession)
	defer closeTestSessions(activeSession)

	gate := protocol.Envelope{Version: protocol.Version1, Type: protocol.TypePong}
	if err := activeSession.send(gate); err != nil {
		t.Fatalf("gate control enqueue returned an error: %v", err)
	}
	waitForSignal(t, connection.writeStarted, "gate write")
	queued := []protocol.Envelope{
		dataEnvelope("alpha", "YQ"),
		dataEnvelope("alpha", "Yg"),
		dataEnvelope("bravo", "Yw"),
		{Version: protocol.Version1, Type: protocol.TypeClose, StreamID: "control", Payload: encodeRelayError("client_closed")},
	}
	for _, envelope := range queued {
		if err := activeSession.send(envelope); err != nil {
			t.Fatalf("enqueue %s/%s returned an error: %v", envelope.Type, envelope.StreamID, err)
		}
	}
	connection.release()

	want := []protocol.Envelope{
		{Version: 1, Type: protocol.TypePong},
		{Version: 1, Type: protocol.TypeClose, StreamID: "control", Payload: "Y2xpZW50X2Nsb3NlZA"},
		{Version: 1, Type: protocol.TypeData, StreamID: "alpha", Payload: "YQ"},
		{Version: 1, Type: protocol.TypeData, StreamID: "bravo", Payload: "Yw"},
		{Version: 1, Type: protocol.TypeData, StreamID: "alpha", Payload: "Yg"},
	}
	for index, expected := range want {
		actual := readRecordedEnvelope(t, connection)
		if actual != expected {
			t.Fatalf("WebSocket write %d = %#v, want %#v", index+1, actual, expected)
		}
	}
}

func TestSessionWriterCompletionRefundsInFlightReservation(t *testing.T) {
	service := newWriterTestService()
	service.agentToClientsBudget = newOutboundDataBudget(1, 2)
	connection := newRecordingSessionConnection(true)
	activeSession := newSession(service, "completion-refund", enrollment.RoleClient, connection)
	registerTestSessions(service, activeSession)
	defer closeTestSessions(activeSession)

	if err := activeSession.send(dataEnvelope("alpha", "YQ")); err != nil {
		t.Fatalf("first data enqueue returned an error: %v", err)
	}
	waitForSignal(t, connection.writeStarted, "in-flight data write")
	if err := activeSession.send(dataEnvelope("bravo", "Yg")); !errors.Is(err, errOutboundDataSaturated) {
		t.Fatalf("second data enqueue while first is in flight = %v, want saturation", err)
	}
	connection.release()
	_ = readRecordedEnvelope(t, connection)

	deadline := time.Now().Add(2 * time.Second)
	for {
		err := activeSession.send(dataEnvelope("bravo", "Yg"))
		if err == nil {
			return
		}
		if !errors.Is(err, errOutboundDataSaturated) {
			t.Fatalf("second data enqueue after completion = %v, want admitted", err)
		}
		if !time.Now().Before(deadline) {
			t.Fatal("writer completion did not refund the in-flight reservation")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestSessionWriterFailureRefundsInFlightReservation(t *testing.T) {
	service := newWriterTestService()
	service.agentToClientsBudget = newOutboundDataBudget(1, 2)
	failingConnection := newRecordingSessionConnection(false)
	failingConnection.failWrites.Store(true)
	peerConnection := newRecordingSessionConnection(false)
	failing := newSession(service, "failing-data", enrollment.RoleClient, failingConnection)
	peer := newSession(service, "peer-data", enrollment.RoleClient, peerConnection)
	registerTestSessions(service, failing, peer)
	defer closeTestSessions(failing, peer)

	if err := failing.send(dataEnvelope("alpha", "YQ")); err != nil {
		t.Fatalf("failed writer data enqueue returned an error: %v", err)
	}
	waitForSignal(t, failingConnection.closed, "failed data writer session close")
	if err := peer.send(dataEnvelope("bravo", "Yg")); err != nil {
		t.Fatalf("peer data enqueue after writer failure = %v, want admitted", err)
	}
	if envelope := readRecordedEnvelope(t, peerConnection); envelope.StreamID != "bravo" || envelope.Type != protocol.TypeData {
		t.Fatalf("peer received %#v after writer failure, want bravo data", envelope)
	}
}

func TestCloseCannotLoseToConcurrentDataAdmission(t *testing.T) {
	tests := []struct {
		name       string
		dataRoute  func(*Service, *session, protocol.Envelope) error
		closeRoute func(*Service, *session, protocol.Envelope) error
		dataRole   enrollment.Role
	}{
		{
			name: "Client data and Agent close",
			dataRoute: func(service *Service, sender *session, envelope protocol.Envelope) error {
				return service.handleClientStreamFrame(sender, envelope)
			},
			closeRoute: func(service *Service, sender *session, envelope protocol.Envelope) error {
				return service.handleAgentStreamFrame(sender, envelope)
			},
			dataRole: enrollment.RoleClient,
		},
		{
			name: "Agent data and Client close",
			dataRoute: func(service *Service, sender *session, envelope protocol.Envelope) error {
				return service.handleAgentStreamFrame(sender, envelope)
			},
			closeRoute: func(service *Service, sender *session, envelope protocol.Envelope) error {
				return service.handleClientStreamFrame(sender, envelope)
			},
			dataRole: enrollment.RoleAgent,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newRelayFixture(t)
			defer fixture.Close()
			service := fixture.service
			client := newDormantSession(service, "client", enrollment.RoleClient)
			agent := newDormantSession(service, "agent", enrollment.RoleAgent)
			registerTestSessions(service, client, agent)
			tracked := &stream{id: "racing-stream", client: client, agent: agent, state: streamOpen, lastActivity: time.Now()}
			service.mu.Lock()
			service.streams[tracked.id] = tracked
			service.activeStreams = 1
			service.mu.Unlock()

			admissionReached := make(chan struct{})
			releaseAdmission := make(chan struct{})
			var signalOnce sync.Once
			service.beforeDataAdmission = func() {
				signalOnce.Do(func() { close(admissionReached) })
				<-releaseAdmission
			}
			defer func() { service.beforeDataAdmission = nil }()

			dataSender, closeSender := client, agent
			dataTarget := agent
			if test.dataRole == enrollment.RoleAgent {
				dataSender, closeSender = agent, client
				dataTarget = client
			}
			dataDone := make(chan error, 1)
			go func() {
				dataDone <- test.dataRoute(service, dataSender, dataEnvelope(tracked.id, "YQ"))
			}()
			waitForSignal(t, admissionReached, "data admission race point")

			closeStarted := make(chan struct{})
			closeDone := make(chan error, 1)
			go func() {
				close(closeStarted)
				closeDone <- test.closeRoute(service, closeSender, streamCloseEnvelope(tracked.id, "client_closed"))
			}()
			waitForSignal(t, closeStarted, "concurrent close start")
			closeFinishedEarly := false
			select {
			case err := <-closeDone:
				if err != nil {
					t.Fatalf("concurrent close returned an error: %v", err)
				}
				closeFinishedEarly = true
			case <-time.After(250 * time.Millisecond):
			}
			close(releaseAdmission)
			if err := <-dataDone; err != nil {
				t.Fatalf("concurrent data returned an error: %v", err)
			}
			if !closeFinishedEarly {
				if err := <-closeDone; err != nil {
					t.Fatalf("concurrent close returned an error: %v", err)
				}
			}
			service.mu.RLock()
			_, streamStillTracked := service.streams[tracked.id]
			service.mu.RUnlock()
			if streamStillTracked {
				t.Fatal("concurrent close did not remove the stream")
			}

			for {
				item, ok := dataTarget.outbound.poll()
				if !ok {
					break
				}
				envelope := item.envelope
				item.complete()
				if envelope.Type == protocol.TypeData && envelope.StreamID == tracked.id {
					t.Fatal("data was admitted after the stream was removed")
				}
			}
		})
	}
}

func TestDataSaturationClosesOnlyAffectedStream(t *testing.T) {
	fixture := newRelayFixture(t)
	defer fixture.Close()
	_, devices := enrollDevices(t, fixture, "client", "agent")

	fixture.service.agentToClientsBudget = newOutboundDataBudget(3, 64)
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
	for index := 0; index < 512; index++ {
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

	previousProcs := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previousProcs)
	baseline := runtime.NumGoroutine()
	service.detachSession(agent, "session_closed")
	after := runtime.NumGoroutine()
	if added := after - baseline; added > 8 {
		t.Fatalf("mass teardown added %d goroutines, want no per-notification fan-out", added)
	}
	for _, connection := range connections {
		waitForSignal(t, connection.writeStarted, "teardown notification write")
	}
}

func TestExpiringAgentIgnoresLateInboundLivenessRefresh(t *testing.T) {
	service := newWriterTestService()
	agent := newDormantSession(service, "agent", enrollment.RoleAgent)
	registerTestSessions(service, agent)
	defer closeTestSessions(agent)

	stale := time.Now().Add(-agentSessionLivenessTimeout)
	service.mu.Lock()
	agent.lastInbound = stale
	agent.livenessExpiring = true
	service.mu.Unlock()

	agent.noteInbound(time.Now())

	service.mu.RLock()
	lastInbound := agent.lastInbound
	service.mu.RUnlock()
	if !lastInbound.Equal(stale) {
		t.Fatalf("late inbound refreshed an expiring Agent from %s to %s", stale, lastInbound)
	}
}

func dataEnvelope(streamID, payload string) protocol.Envelope {
	return protocol.Envelope{Version: 1, Type: protocol.TypeData, StreamID: streamID, Payload: payload}
}

func newWriterTestService() *Service {
	return &Service{
		sessions: make(map[string]*session), streams: make(map[string]*stream),
		closedStreams:        make(map[string]closedStreamTombstone),
		agentToClientsBudget: newOutboundDataBudget(8_192, 64<<20),
	}
}

func newDormantSession(service *Service, serial string, role enrollment.Role) *session {
	dataBudget := service.agentToClientsBudget
	if role == enrollment.RoleAgent {
		dataBudget = newOutboundDataBudget(8_192, 64<<20)
	} else if dataBudget == nil {
		dataBudget = newOutboundDataBudget(8_192, 64<<20)
		service.agentToClientsBudget = dataBudget
	}
	return &session{
		service: service, serial: serial, role: role,
		conn: newRecordingSessionConnection(false), outbound: newSessionOutboundMailbox(role, dataBudget),
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

func (connection *recordingSessionConnection) SetPingHandler(func(string) error) {}

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

func authenticatedSessionRequest(t *testing.T, device enrolledDevice) *http.Request {
	t.Helper()
	block, _ := pem.Decode([]byte(device.certificate))
	if block == nil || block.Type != "CERTIFICATE" {
		t.Fatal("enrolled device certificate does not begin with a PEM certificate")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "https://relay.test/v1/session", nil)
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", "websocket")
	request.Header.Set("Sec-WebSocket-Version", "13")
	request.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	request.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{certificate}}}
	return request
}

type blockingUpgradeResponseWriter struct {
	header     http.Header
	connection *blockingUpgradeConnection
}

func newBlockingUpgradeResponseWriter() *blockingUpgradeResponseWriter {
	return &blockingUpgradeResponseWriter{
		header: make(http.Header), connection: &blockingUpgradeConnection{
			writeStarted: make(chan struct{}, 1), releaseWrite: make(chan struct{}), closed: make(chan struct{}),
		},
	}
}

func (writer *blockingUpgradeResponseWriter) Header() http.Header { return writer.header }

func (writer *blockingUpgradeResponseWriter) Write(value []byte) (int, error) { return len(value), nil }

func (writer *blockingUpgradeResponseWriter) WriteHeader(int) {}

func (writer *blockingUpgradeResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return writer.connection, bufio.NewReadWriter(bufio.NewReader(writer.connection), bufio.NewWriter(writer.connection)), nil
}

func (writer *blockingUpgradeResponseWriter) release() {
	writer.connection.releaseOnce.Do(func() { close(writer.connection.releaseWrite) })
}

type blockingUpgradeConnection struct {
	writeStarted chan struct{}
	releaseWrite chan struct{}
	closed       chan struct{}
	releaseOnce  sync.Once
	closeOnce    sync.Once
}

func (connection *blockingUpgradeConnection) Read([]byte) (int, error) { return 0, io.EOF }

func (connection *blockingUpgradeConnection) Write(value []byte) (int, error) {
	select {
	case connection.writeStarted <- struct{}{}:
	default:
	}
	select {
	case <-connection.releaseWrite:
		return len(value), nil
	case <-connection.closed:
		return 0, errors.New("blocked upgrade connection closed")
	}
}

func (connection *blockingUpgradeConnection) Close() error {
	connection.closeOnce.Do(func() { close(connection.closed) })
	return nil
}

func (connection *blockingUpgradeConnection) LocalAddr() net.Addr { return fixedTestAddr("local") }

func (connection *blockingUpgradeConnection) RemoteAddr() net.Addr { return fixedTestAddr("remote") }

func (connection *blockingUpgradeConnection) SetDeadline(time.Time) error { return nil }

func (connection *blockingUpgradeConnection) SetReadDeadline(time.Time) error { return nil }

func (connection *blockingUpgradeConnection) SetWriteDeadline(time.Time) error { return nil }

type fixedTestAddr string

func (address fixedTestAddr) Network() string { return "test" }

func (address fixedTestAddr) String() string { return string(address) }
