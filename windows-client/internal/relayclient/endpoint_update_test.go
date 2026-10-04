package relayclient

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func TestEndpointUpdateWaitsForExplicitPhoneCapabilityAndKeepsLatest(t *testing.T) {
	s, peer := directSessionPair(t)
	if err := s.SendEndpointUpdate("old"); err != nil {
		t.Fatal(err)
	}
	if err := s.SendEndpointUpdate("latest"); err != nil {
		t.Fatal(err)
	}
	writeWireEnvelope(t, peer, wireEnvelope{Version: 1, Type: "pong"})
	// A normal ping response is the ordering barrier: no unsupported update may
	// have been emitted to an older phone before the explicit advertisement.
	writeWireEnvelope(t, peer, wireEnvelope{Version: 1, Type: "ping"})
	peer.SetReadDeadline(time.Now().Add(time.Second))
	if got := readTestWireEnvelope(t, peer); got.Type != "pong" {
		t.Fatal("sent update before capability")
	}
	writeWireEnvelope(t, peer, wireEnvelope{Version: 1, Type: "pong", Payload: base64.RawURLEncoding.EncodeToString([]byte("mobile-egress.endpoint-update.v1"))})
	got := readTestWireEnvelope(t, peer)
	payload, err := got.DecodePayload()
	if err != nil || got.Type != "endpoint_update" || got.StreamID != "" || string(payload) != "latest" {
		t.Fatal("wrong endpoint control")
	}
	if err := s.SendEndpointUpdate(strings.Repeat("a", 87385)); err == nil {
		t.Fatal("unbounded update accepted")
	}
}
