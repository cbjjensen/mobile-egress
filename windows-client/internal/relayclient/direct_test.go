package relayclient

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func directSessionPair(t *testing.T) (*Session, *websocket.Conn) {
	t.Helper()
	accepted := make(chan *Session, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, e := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if e != nil {
			return
		}
		s, e := AcceptAgent(c)
		if e == nil {
			accepted <- s
		}
	}))
	t.Cleanup(server.Close)
	peer, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { peer.Close() })
	session := <-accepted
	t.Cleanup(func() { session.Close() })
	_, _, err = peer.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	return session, peer
}
func directWait(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("condition did not become true")
		}
		time.Sleep(time.Millisecond)
	}
}
func TestDirectErrorCloseAbortsOrderlyTailWithoutClosingOtherStreams(t *testing.T) {
	session, peer := directSessionPair(t)
	result := make(chan io.ReadWriteCloser, 1)
	go func() { stream, _ := session.OpenStream(context.Background(), "1.1.1.1", 443); result <- stream }()
	open := readTestWireEnvelope(t, peer)
	writeWireEnvelope(t, peer, wireEnvelope{Version: 1, Type: "opened", StreamID: open.StreamID})
	stream := <-result
	if stream == nil {
		t.Fatal("open failed")
	}
	defer stream.Close()
	writeWireEnvelope(t, peer, wireEnvelope{Version: 1, Type: "data", StreamID: open.StreamID, Payload: base64.RawURLEncoding.EncodeToString([]byte("discard on abort"))})
	writeWireEnvelope(t, peer, wireEnvelope{Version: 1, Type: "close", StreamID: open.StreamID, Payload: base64.RawURLEncoding.EncodeToString([]byte("target_closed"))})
	directWait(t, func() bool { session.mu.Lock(); defer session.mu.Unlock(); return len(session.draining) == 1 })
	writeWireEnvelope(t, peer, wireEnvelope{Version: 1, Type: "close", StreamID: open.StreamID, Payload: base64.RawURLEncoding.EncodeToString([]byte("connect_failed"))})
	directWait(t, func() bool { frames, _ := session.inboundBudget.outstanding(); return frames == 0 })
	if !session.Healthy() {
		t.Fatal("aborting one draining stream closed unrelated tunnel")
	}
	if raw, _ := io.ReadAll(stream); len(raw) != 0 {
		t.Fatal("abort delivered queued tail")
	}
}
func TestDirectCloseBeforeOpenedDoesNotLeakDrainingStream(t *testing.T) {
	session, peer := directSessionPair(t)
	result := make(chan error, 1)
	go func() { _, err := session.OpenStream(context.Background(), "1.1.1.1", 443); result <- err }()
	open := readTestWireEnvelope(t, peer)
	writeWireEnvelope(t, peer, wireEnvelope{Version: 1, Type: "close", StreamID: open.StreamID, Payload: base64.RawURLEncoding.EncodeToString([]byte("target_closed"))})
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("closed opening stream succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("pending open leaked")
	}
	session.mu.Lock()
	count := len(session.draining) + len(session.streams)
	session.mu.Unlock()
	if count != 0 {
		t.Fatal("unreachable stream retained after close before opened")
	}
}

func TestAcceptedAgentDeliversTailBeforeEOF(t *testing.T) {
	accepted := make(chan *Session, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, e := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if e != nil {
			return
		}
		s, e := AcceptAgent(c)
		if e == nil {
			accepted <- s
		}
	}))
	defer server.Close()
	peer, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	session := <-accepted
	defer session.Close()
	go func() {
		for {
			kind, raw, e := peer.ReadMessage()
			if e != nil {
				return
			}
			frame, e := parseWireEnvelope(raw)
			if e != nil {
				return
			}
			if frame.Type == "ping" {
				out, _ := (wireEnvelope{Version: 1, Type: "pong"}).MarshalForPeer(false)
				_ = peer.WriteMessage(kind, out)
				continue
			}
			if frame.Type != "open" {
				continue
			}
			payload, _ := frame.DecodePayload()
			var target struct {
				IP   string   `json:"ip"`
				Port int      `json:"port"`
				IPs  []string `json:"ips"`
			}
			if json.Unmarshal(payload, &target) != nil || target.IP != "1.1.1.1" {
				return
			}
			for _, f := range []wireEnvelope{{Version: 1, Type: "opened", StreamID: frame.StreamID}, {Version: 1, Type: "data", StreamID: frame.StreamID, Data: []byte("exact final bytes")}, {Version: 1, Type: "close", StreamID: frame.StreamID, Payload: base64.RawURLEncoding.EncodeToString([]byte("target_closed"))}} {
				out, _ := f.MarshalForPeer(true)
				if peer.WriteMessage(kind, out) != nil {
					return
				}
			}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	stream, err := session.OpenStream(ctx, "1.1.1.1", 443)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	time.Sleep(25 * time.Millisecond)
	raw, err := io.ReadAll(stream)
	if err != nil || string(raw) != "exact final bytes" {
		t.Fatalf("tail=%q err=%v", raw, err)
	}
	if frames, bytes := session.inboundBudget.outstanding(); frames != 0 || bytes != 0 {
		t.Fatalf("budget leaked: %d/%d", frames, bytes)
	}
}

func TestDirectResolverRejectsMixedPrivateAnswers(t *testing.T) {
	resolver := newDirectResolver(func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("127.0.0.1")}, nil
	})
	if _, err := resolver(context.Background(), "mixed.example", 443); err == nil {
		t.Fatal("private DNS answer accepted")
	}
}
