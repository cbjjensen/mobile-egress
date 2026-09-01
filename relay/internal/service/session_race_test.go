package service

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/netip"
	"strconv"
	"testing"
	"time"

	"mobile-egress/relay/internal/enrollment"
	"mobile-egress/relay/internal/protocol"
)

type controlledResolver struct {
	started chan struct{}
	release chan struct{}
}

func (resolver *controlledResolver) LookupNetIP(ctx context.Context, _, _ string) ([]netip.Addr, error) {
	close(resolver.started)
	select {
	case <-resolver.release:
		return []netip.Addr{netip.MustParseAddr("1.1.1.1")}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestRevocationDuringResolutionDoesNotCreateOrForwardStream(t *testing.T) {
	fixture := newRelayFixture(t)
	defer fixture.Close()
	owner, devices := enrollDevices(t, fixture, "client", "agent")
	client := mustDialSession(t, fixture, devices[0].client)
	defer client.Close()
	agent := mustDialSession(t, fixture, devices[1].client)
	defer agent.Close()

	resolver := &controlledResolver{started: make(chan struct{}), release: make(chan struct{})}
	fixture.service.lookupNetIP = resolver.LookupNetIP
	writeEnvelope(t, client, openEnvelope("revoked-during-resolution", "example.test", 443))
	waitForSignal(t, resolver.started, "resolver start")

	if status := postRevocation(t, owner.client, fixture.server.URL, devices[0].serial); status != http.StatusNoContent {
		t.Fatalf("revocation status = %d, want 204", status)
	}
	close(resolver.release)

	agent.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
	if _, _, err := agent.ReadMessage(); err == nil {
		t.Fatal("agent received an open frame after the client revocation completed")
	}
	fixture.service.mu.RLock()
	activeStreams := fixture.service.activeStreams
	fixture.service.mu.RUnlock()
	if activeStreams != 0 {
		t.Fatalf("active streams after revocation = %d, want 0", activeStreams)
	}
}

func TestSessionRegistrationRevalidatesIdentityAfterAuthentication(t *testing.T) {
	fixture := newRelayFixture(t)
	defer fixture.Close()
	_, devices := enrollDevices(t, fixture, "agent")

	type dialResult struct {
		connectionPresent bool
		status            int
		err               error
	}
	result := make(chan dialResult, 1)
	fixture.service.mu.Lock()
	go func() {
		connection, response, err := dialSession(fixture, devices[0].client)
		status := 0
		if response != nil {
			status = response.StatusCode
			_ = response.Body.Close()
		}
		present := connection != nil
		if connection != nil {
			_ = connection.Close()
		}
		result <- dialResult{connectionPresent: present, status: status, err: err}
	}()
	waitForIdentityTouch(t, fixture.service.store, devices[0].serial)
	if err := fixture.service.store.revokeIdentity(context.Background(), devices[0].serial, time.Now().UTC()); err != nil {
		fixture.service.mu.Unlock()
		t.Fatal(err)
	}
	fixture.service.mu.Unlock()

	select {
	case outcome := <-result:
		if outcome.connectionPresent || outcome.err == nil || outcome.status != http.StatusUnauthorized {
			t.Fatalf("post-auth revocation dial = connection %t, status %d, error %v; want rejected 401", outcome.connectionPresent, outcome.status, outcome.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("session admission did not finish")
	}
	fixture.service.mu.RLock()
	agentConnected := fixture.service.agentConnected
	fixture.service.mu.RUnlock()
	if agentConnected {
		t.Fatal("revoked agent occupied the active Agent slot")
	}
}

func TestStreamExpirationDoesNotWaitForBlockedWebSocketWriter(t *testing.T) {
	fixture := newRelayFixture(t)
	defer fixture.Close()
	_, devices := enrollDevices(t, fixture, "client", "agent")
	client := mustDialSession(t, fixture, devices[0].client)
	defer client.Close()
	agent := mustDialSession(t, fixture, devices[1].client)
	defer agent.Close()

	writeEnvelope(t, client, openEnvelope("backpressured-expiration", "1.1.1.1", 443))
	_ = readEnvelope(t, agent)
	fixture.service.mu.RLock()
	clientSession := fixture.service.sessions[devices[0].serial]
	fixture.service.mu.RUnlock()
	clientSession.writeMu.Lock()
	done := make(chan struct{})
	go func() {
		fixture.service.expireStreams(time.Now().Add(31 * time.Second))
		close(done)
	}()

	blocked := false
	select {
	case <-done:
	case <-time.After(150 * time.Millisecond):
		blocked = true
	}
	clientSession.writeMu.Unlock()
	waitForSignal(t, done, "stream expiration completion")
	if blocked {
		t.Fatal("stream expiration waited for a blocked WebSocket writer")
	}
	fixture.service.mu.RLock()
	activeStreams := fixture.service.activeStreams
	fixture.service.mu.RUnlock()
	if activeStreams != 0 {
		t.Fatalf("active streams after expiration = %d, want 0", activeStreams)
	}
}

func TestRevocationDoesNotWaitForBlockedStreamNotification(t *testing.T) {
	fixture := newRelayFixture(t)
	defer fixture.Close()
	owner, devices := enrollDevices(t, fixture, "client", "agent")
	client := mustDialSession(t, fixture, devices[0].client)
	defer client.Close()
	agent := mustDialSession(t, fixture, devices[1].client)
	defer agent.Close()

	writeEnvelope(t, client, openEnvelope("backpressured-revocation", "1.1.1.1", 443))
	_ = readEnvelope(t, agent)
	fixture.service.mu.RLock()
	agentSession := fixture.service.sessions[devices[1].serial]
	fixture.service.mu.RUnlock()
	agentSession.writeMu.Lock()

	type revokeResult struct {
		status int
		err    error
	}
	result := make(chan revokeResult, 1)
	go func() {
		requestBody, _ := json.Marshal(map[string]string{"serial": devices[0].serial})
		response, err := owner.client.Post(fixture.server.URL+"/v1/revoke", "application/json", bytes.NewReader(requestBody))
		if err != nil {
			result <- revokeResult{err: err}
			return
		}
		defer response.Body.Close()
		result <- revokeResult{status: response.StatusCode}
	}()

	blocked := false
	var outcome revokeResult
	select {
	case outcome = <-result:
	case <-time.After(150 * time.Millisecond):
		blocked = true
	}
	agentSession.writeMu.Unlock()
	if blocked {
		select {
		case outcome = <-result:
		case <-time.After(2 * time.Second):
			t.Fatal("revocation did not finish after releasing the writer")
		}
	}
	if outcome.err != nil || outcome.status != http.StatusNoContent {
		t.Fatalf("revocation result = status %d, error %v", outcome.status, outcome.err)
	}
	if blocked {
		t.Fatal("revocation waited for a blocked WebSocket stream notification")
	}
}

func TestAgentOpenedAfterStoredDeadlineClosesStreamBeforeSweep(t *testing.T) {
	fixture := newRelayFixture(t)
	defer fixture.Close()
	_, devices := enrollDevices(t, fixture, "client", "agent")
	client := mustDialSession(t, fixture, devices[0].client)
	defer client.Close()
	agent := mustDialSession(t, fixture, devices[1].client)
	defer agent.Close()

	writeEnvelope(t, client, openEnvelope("late-opened", "1.1.1.1", 443))
	_ = readEnvelope(t, agent)
	fixture.service.mu.Lock()
	fixture.service.streams["late-opened"].openingDeadline = time.Now().Add(-time.Millisecond)
	fixture.service.mu.Unlock()

	writeEnvelope(t, agent, protocol.Envelope{Version: 1, Type: protocol.TypeOpened, StreamID: "late-opened", Payload: ""})
	closed := readEnvelope(t, client)
	if closed.Type != protocol.TypeClose || decodedErrorCode(t, closed) != "opening_timeout" {
		t.Fatalf("late opened response = %#v, want opening_timeout close", closed)
	}
}

func TestClosedStreamTombstonesAreBoundedAndPurgedBySweep(t *testing.T) {
	service := &Service{
		streams:       make(map[string]*stream),
		closedStreams: make(map[string]closedStreamTombstone),
	}
	client := &session{role: enrollment.RoleClient}
	agent := &session{role: enrollment.RoleAgent}
	for index := 0; index < 2048; index++ {
		id := "closed-" + strconv.Itoa(index)
		tracked := &stream{id: id, client: client, agent: agent, state: streamOpen}
		service.streams[id] = tracked
		service.activeStreams++
		service.removeStreamLocked(tracked)
	}
	if got := len(service.closedStreams); got != 1024 {
		t.Fatalf("closed stream tombstones = %d, want 1024", got)
	}
	now := time.Now()
	lateDataPayload := base64.RawURLEncoding.EncodeToString([]byte("late-data"))
	wrongClient := &session{role: enrollment.RoleClient}
	lastStreamID := ""
	for streamID := range service.closedStreams {
		lastStreamID = streamID
		closeEnvelope := protocol.Envelope{Version: 1, Type: protocol.TypeClose, StreamID: streamID, Payload: encodeRelayError("client_closed")}
		if !service.absorbLateStreamFrameLocked(client, enrollment.RoleClient, closeEnvelope, now) {
			t.Fatalf("first late Client close for %q was not absorbed", streamID)
		}
		if !service.absorbLateStreamFrameLocked(client, enrollment.RoleClient, closeEnvelope, now) {
			t.Fatalf("duplicate late Client close for %q was not absorbed", streamID)
		}
		if !service.absorbLateStreamFrameLocked(agent, enrollment.RoleAgent, closeEnvelope, now) {
			t.Fatalf("late Agent close for %q was not absorbed", streamID)
		}
		dataEnvelope := protocol.Envelope{Version: 1, Type: protocol.TypeData, StreamID: streamID, Payload: lateDataPayload}
		if !service.absorbLateStreamFrameLocked(client, enrollment.RoleClient, dataEnvelope, now) {
			t.Fatalf("late Client data for %q was not absorbed", streamID)
		}
		if !service.absorbLateStreamFrameLocked(agent, enrollment.RoleAgent, dataEnvelope, now) {
			t.Fatalf("late Agent data for %q was not absorbed", streamID)
		}
		if service.absorbLateStreamFrameLocked(wrongClient, enrollment.RoleClient, dataEnvelope, now) {
			t.Fatalf("wrong Client's late data for %q was incorrectly absorbed", streamID)
		}
		rejected := protocol.Envelope{Version: 1, Type: protocol.TypeRejected, StreamID: streamID, Payload: encodeRelayError("target_failure")}
		if service.absorbLateStreamFrameLocked(agent, enrollment.RoleAgent, rejected, now) {
			t.Fatalf("late rejection for %q was incorrectly absorbed as a close", streamID)
		}
	}
	if got := len(service.closedStreams); got != 1024 {
		t.Fatalf("closed stream tombstones after late-frame churn = %d, want 1024", got)
	}
	openedForOpenStream := protocol.Envelope{Version: 1, Type: protocol.TypeOpened, StreamID: lastStreamID, Payload: ""}
	if service.absorbLateStreamFrameLocked(agent, enrollment.RoleAgent, openedForOpenStream, now) {
		t.Fatal("late Agent opened for a formerly open stream was incorrectly absorbed")
	}
	rejectedForOpenStream := protocol.Envelope{Version: 1, Type: protocol.TypeRejected, StreamID: lastStreamID, Payload: encodeRelayError("target_failure")}
	if service.absorbLateStreamFrameLocked(agent, enrollment.RoleAgent, rejectedForOpenStream, now) {
		t.Fatal("late Agent rejection for a formerly open stream was incorrectly absorbed")
	}

	opening := &stream{id: "closed-opening-opened", client: client, agent: agent, state: streamOpening}
	service.streams[opening.id] = opening
	service.activeStreams++
	service.removeStreamLocked(opening)
	opened := protocol.Envelope{Version: 1, Type: protocol.TypeOpened, StreamID: opening.id, Payload: ""}
	if !service.absorbLateStreamFrameLocked(agent, enrollment.RoleAgent, opened, now) {
		t.Fatal("late Agent opened for a closed opening stream was not absorbed")
	}
	openedData := protocol.Envelope{Version: 1, Type: protocol.TypeData, StreamID: opening.id, Payload: lateDataPayload}
	if !service.absorbLateStreamFrameLocked(agent, enrollment.RoleAgent, openedData, now) {
		t.Fatal("late Agent data following an absorbed opened frame was not absorbed")
	}
	if service.absorbLateStreamFrameLocked(agent, enrollment.RoleAgent, opened, now) {
		t.Fatal("duplicate late Agent opened was incorrectly absorbed")
	}
	conflictingRejection := protocol.Envelope{Version: 1, Type: protocol.TypeRejected, StreamID: opening.id, Payload: encodeRelayError("target_failure")}
	if service.absorbLateStreamFrameLocked(agent, enrollment.RoleAgent, conflictingRejection, now) {
		t.Fatal("late Agent rejection after opened was incorrectly absorbed")
	}
	invalidOpened := protocol.Envelope{Version: 1, Type: protocol.TypeOpened, StreamID: opening.id, Payload: lateDataPayload}
	if service.absorbLateStreamFrameLocked(agent, enrollment.RoleAgent, invalidOpened, now) {
		t.Fatal("late Agent opened with a payload was incorrectly absorbed")
	}
	open := protocol.Envelope{Version: 1, Type: protocol.TypeOpen, StreamID: opening.id, Payload: ""}
	if service.absorbLateStreamFrameLocked(agent, enrollment.RoleAgent, open, now) {
		t.Fatal("late Agent open for a closed stream was incorrectly absorbed")
	}

	rejectedOpening := &stream{id: "closed-opening-rejected", client: client, agent: agent, state: streamOpening}
	service.streams[rejectedOpening.id] = rejectedOpening
	service.activeStreams++
	service.removeStreamLocked(rejectedOpening)
	rejected := protocol.Envelope{Version: 1, Type: protocol.TypeRejected, StreamID: rejectedOpening.id, Payload: encodeRelayError("target_failure")}
	if !service.absorbLateStreamFrameLocked(agent, enrollment.RoleAgent, rejected, now) {
		t.Fatal("late Agent rejection for a closed opening stream was not absorbed")
	}
	if service.absorbLateStreamFrameLocked(agent, enrollment.RoleAgent, rejected, now) {
		t.Fatal("duplicate late Agent rejection was incorrectly absorbed")
	}
	rejectedData := protocol.Envelope{Version: 1, Type: protocol.TypeData, StreamID: rejectedOpening.id, Payload: lateDataPayload}
	if service.absorbLateStreamFrameLocked(agent, enrollment.RoleAgent, rejectedData, now) {
		t.Fatal("late Agent data following a rejected opening was incorrectly absorbed")
	}

	service.expireStreams(time.Now().Add(time.Minute))
	if got := len(service.closedStreams); got != 0 {
		t.Fatalf("closed stream tombstones after sweep = %d, want 0", got)
	}
	expiredData := protocol.Envelope{Version: 1, Type: protocol.TypeData, StreamID: lastStreamID, Payload: lateDataPayload}
	if service.absorbLateStreamFrameLocked(client, enrollment.RoleClient, expiredData, time.Now().Add(time.Minute)) {
		t.Fatal("late data for an expired tombstone was incorrectly absorbed")
	}
}

func waitForIdentityTouch(t *testing.T, state *store, serial string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		var lastSeen sql.NullInt64
		if err := state.db.QueryRow(`SELECT last_seen_at FROM identities WHERE serial = ?`, serial).Scan(&lastSeen); err != nil {
			t.Fatal(err)
		}
		if lastSeen.Valid {
			return
		}
		if !time.Now().Before(deadline) {
			t.Fatal("session authentication did not touch the identity")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func waitForSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}
