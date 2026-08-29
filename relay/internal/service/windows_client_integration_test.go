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

	keyDER, err := x509.MarshalPKCS8PrivateKey(devices[0].key)
	if err != nil {
		t.Fatal(err)
	}
	caPEM, err := os.ReadFile(filepath.Join(fixture.stateDir, caCertFilename))
	if err != nil {
		t.Fatal(err)
	}
	session, err := relaytransport.DialSession(context.Background(), relaytransport.Identity{
		RelayURL: fixture.server.URL, Role: "client", Serial: devices[0].serial,
		PrivateKeyPEM:  string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})),
		CertificatePEM: devices[0].certificate, CACertificatePEM: string(caPEM),
	})
	if err != nil {
		t.Fatal(err)
	}
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
