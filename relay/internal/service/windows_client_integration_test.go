package service

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"mobile-egress/relay/internal/protocol"
	"mobile-egress/windows-client/relaytransport"
)

func TestWindowsClientRemoteCloseLeavesOtherRelayStreamUsable(t *testing.T) {
	t.Parallel()

	fixture := newRelayFixture(t)
	defer fixture.Close()
	_, devices := enrollDevices(t, fixture, "client", "agent")
	agent := mustDialSession(t, fixture, devices[1].client)
	defer agent.Close()
	session := dialWindowsSession(t, fixture, devices[0])
	defer session.Close()

	streamOne := openWindowsStream(t, session, agent, "1.1.1.1", 443)
	streamTwo := openWindowsStream(t, session, agent, "8.8.8.8", 443)
	defer streamTwo.stream.Close()

	writeEnvelope(t, agent, protocol.Envelope{
		Version: 1, Type: protocol.TypeClose, StreamID: streamOne.id,
		Payload: base64.RawURLEncoding.EncodeToString([]byte("target_closed")),
	})
	oneByte := make([]byte, 1)
	if _, err := streamOne.stream.Read(oneByte); err != io.EOF {
		t.Fatalf("remote close read error = %v, want EOF", err)
	}
	if err := streamOne.stream.Close(); err != nil {
		t.Fatal(err)
	}

	type agentRead struct {
		envelope protocol.Envelope
		err      error
	}
	unexpected := make(chan agentRead, 1)
	go func() {
		_, raw, readErr := agent.ReadMessage()
		if readErr != nil {
			unexpected <- agentRead{err: readErr}
			return
		}
		var envelope protocol.Envelope
		decodeErr := json.Unmarshal(raw, &envelope)
		unexpected <- agentRead{envelope: envelope, err: decodeErr}
	}()
	select {
	case result := <-unexpected:
		if result.err == nil {
			t.Fatalf("remote close disconnected another stream: %#v", result.envelope)
		}
	case <-time.After(150 * time.Millisecond):
	}

	writeEnvelope(t, agent, protocol.Envelope{
		Version: 1, Type: protocol.TypeData, StreamID: streamTwo.id,
		Payload: base64.RawURLEncoding.EncodeToString([]byte("still-usable")),
	})
	buffer := make([]byte, len("still-usable"))
	if _, err := io.ReadFull(streamTwo.stream, buffer); err != nil {
		t.Fatalf("unrelated stream failed after remote close: %v", err)
	}
}

func TestConcurrentWindowsAndAgentCloseLeavesBothSessionsUsable(t *testing.T) {
	fixture := newRelayFixture(t)
	defer fixture.Close()
	_, devices := enrollDevices(t, fixture, "client", "agent")
	agent := mustDialSession(t, fixture, devices[1].client)
	defer agent.Close()
	windowsSession := dialWindowsSession(t, fixture, devices[0])
	defer windowsSession.Close()

	first := openWindowsStream(t, windowsSession, agent, "1.1.1.1", 443)
	fixture.service.mu.RLock()
	relayClient := fixture.service.sessions[devices[0].serial]
	fixture.service.mu.RUnlock()
	if relayClient == nil {
		t.Fatal("relay did not retain the Windows session")
	}
	relayClient.writeMu.Lock()
	writerLocked := true
	defer func() {
		if writerLocked {
			relayClient.writeMu.Unlock()
		}
	}()

	writeEnvelope(t, agent, protocol.Envelope{
		Version: 1, Type: protocol.TypeClose, StreamID: first.id,
		Payload: base64.RawURLEncoding.EncodeToString([]byte("target_closed")),
	})
	waitForStreamRemoval(t, fixture.service, first.id)
	if err := first.stream.Close(); err != nil {
		t.Fatal(err)
	}

	secondResult := make(chan io.ReadWriteCloser, 1)
	secondErrors := make(chan error, 1)
	openContext, cancelOpen := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelOpen()
	go func() {
		stream, err := windowsSession.OpenStream(openContext, "8.8.8.8", 443)
		if err != nil {
			secondErrors <- err
			return
		}
		secondResult <- stream
	}()
	secondOpen := readEnvelope(t, agent)
	if secondOpen.Type != protocol.TypeOpen {
		t.Fatalf("agent received %#v, want second open", secondOpen)
	}
	relayClient.writeMu.Unlock()
	writerLocked = false
	writeEnvelope(t, agent, protocol.Envelope{Version: 1, Type: protocol.TypeOpened, StreamID: secondOpen.StreamID})

	select {
	case second := <-secondResult:
		defer second.Close()
	case err := <-secondErrors:
		t.Fatalf("second Windows stream failed after concurrent closes: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("second Windows stream did not open after concurrent closes")
	}
}

type openedWindowsStream struct {
	stream io.ReadWriteCloser
	id     string
}

func openWindowsStream(t *testing.T, session *relaytransport.Session, agent *websocket.Conn, host string, port uint16) openedWindowsStream {
	t.Helper()
	result := make(chan io.ReadWriteCloser, 1)
	errors := make(chan error, 1)
	go func() {
		stream, err := session.OpenStream(context.Background(), host, port)
		if err != nil {
			errors <- err
			return
		}
		result <- stream
	}()
	envelope := readEnvelope(t, agent)
	writeEnvelope(t, agent, protocol.Envelope{Version: 1, Type: protocol.TypeOpened, StreamID: envelope.StreamID})
	select {
	case stream := <-result:
		return openedWindowsStream{stream: stream, id: envelope.StreamID}
	case err := <-errors:
		t.Fatal(err)
	case <-time.After(2 * time.Second):
		t.Fatal("Windows stream did not open")
	}
	return openedWindowsStream{}
}

func dialWindowsSession(t *testing.T, fixture *relayFixture, device enrolledDevice) *relaytransport.Session {
	t.Helper()
	keyDER, err := x509.MarshalPKCS8PrivateKey(device.key)
	if err != nil {
		t.Fatal(err)
	}
	caPEM, err := os.ReadFile(filepath.Join(fixture.stateDir, caCertFilename))
	if err != nil {
		t.Fatal(err)
	}
	session, err := relaytransport.DialSession(context.Background(), relaytransport.Identity{
		RelayURL: fixture.server.URL, Role: "client", Serial: device.serial,
		PrivateKeyPEM:  string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})),
		CertificatePEM: device.certificate, CACertificatePEM: string(caPEM),
	})
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func waitForStreamRemoval(t *testing.T, service *Service, streamID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		service.mu.RLock()
		_, exists := service.streams[streamID]
		service.mu.RUnlock()
		if !exists {
			return
		}
		if !time.Now().Before(deadline) {
			t.Fatal("relay did not remove first stream")
		}
		time.Sleep(time.Millisecond)
	}
}
