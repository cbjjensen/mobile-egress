package relayclient

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestStreamRejectionDoesNotDisableOtherOpens(t *testing.T) {
	stop := make(chan struct{})
	fixture := newCustomSessionFixture(t, func(conn *websocket.Conn) {
		defer func() { <-stop }()
		first := readTestWireEnvelope(t, conn)
		writeWireEnvelope(t, conn, wireEnvelope{Version: 1, Type: "rejected", StreamID: first.StreamID, Payload: base64.RawURLEncoding.EncodeToString([]byte("agent_unavailable"))})
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		second, err := parseWireEnvelope(raw)
		if err != nil {
			t.Error(err)
			return
		}
		writeWireEnvelope(t, conn, wireEnvelope{Version: 1, Type: "opened", StreamID: second.StreamID})
	})
	defer fixture.Close()
	defer close(stop)
	session, err := DialSession(context.Background(), fixture.identity)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := session.OpenStream(ctx, "first.test", 443); err == nil {
		t.Fatal("expected rejection")
	}
	if !session.Healthy() {
		t.Fatal("one rejected stream disabled the whole Agent")
	}
	stream, err := session.OpenStream(ctx, "second.test", 443)
	if err != nil {
		t.Fatalf("unrelated open rejected: %v", err)
	}
	defer stream.Close()
}

func TestClientRejectsBinaryBeforeAdvertisement(t *testing.T) {
	stop := make(chan struct{})
	fixture := newCustomSessionFixture(t, func(conn *websocket.Conn) {
		defer func() { <-stop }()
		_ = conn.WriteMessage(websocket.BinaryMessage, []byte{2, 4, 0, 1, 's', 1})
	})
	defer fixture.Close()
	defer close(stop)
	session, err := DialSession(context.Background(), fixture.identity)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	select {
	case <-session.Done():
	case <-time.After(time.Second):
		t.Fatal("unadvertised binary session remained open")
	}
}

func TestNegotiatedSessionSendsRawBinaryData(t *testing.T) {
	stop := make(chan struct{})
	fixture := newCustomSessionFixture(t, func(conn *websocket.Conn) {
		defer func() { <-stop }()
		writeWireEnvelope(t, conn, wireEnvelope{Version: 1, Type: "ping", Payload: base64.RawURLEncoding.EncodeToString([]byte("mobile-egress.transport.v2"))})
		var opened wireEnvelope
		for {
			e := readTestWireEnvelope(t, conn)
			if e.Type == "open" {
				opened = e
				break
			}
		}
		writeWireEnvelope(t, conn, wireEnvelope{Version: 1, Type: "opened", StreamID: opened.StreamID})
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if len(raw) > 0 && raw[0] == '{' {
				e, _ := parseWireEnvelope(raw)
				if e.Type == "pong" {
					continue
				}
			}
			expected := append([]byte{2, 4, 0, byte(len(opened.StreamID))}, []byte(opened.StreamID)...)
			expected = append(expected, 0, 255, 128)
			if !bytes.Equal(raw, expected) {
				t.Errorf("data used legacy or malformed framing: %d bytes", len(raw))
				return
			}
			_ = conn.WriteMessage(websocket.BinaryMessage, raw)
			return
		}
	})
	defer fixture.Close()
	defer close(stop)
	session, err := DialSession(context.Background(), fixture.identity)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	stream, err := session.OpenStream(ctx, "example.test", 443)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if _, err = stream.Write([]byte{0, 255, 128}); err != nil {
		t.Fatal(err)
	}
	// Bound a malformed-server failure without waiting for health polling.
	timer := time.AfterFunc(time.Second, func() { session.Close() })
	defer timer.Stop()
	payload := make([]byte, 3)
	if _, err = io.ReadFull(stream, payload); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(payload, []byte{0, 255, 128}) {
		t.Fatal("binary payload corrupted")
	}
}
