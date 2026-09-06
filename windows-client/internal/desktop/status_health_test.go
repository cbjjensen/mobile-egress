package desktop

import (
	"context"
	"mobile-egress/windows-client/internal/relayclient"
	"testing"
)

type fakeControllerHealth struct {
	closed bool
	calls  int
	health relayclient.RelayHealth
}

func (h *fakeControllerHealth) Health(context.Context) (relayclient.RelayHealth, error) {
	h.calls++
	return h.health, nil
}

func TestControllerHealthPropagatesLiveAgentEvidence(t *testing.T) {
	paired := true
	h := controllerHealth{create: func(relayclient.Identity) (controllerHealthClient, error) {
		return &fakeControllerHealth{health: relayclient.RelayHealth{Readiness: true, AgentConnected: true, AgentPaired: &paired}}, nil
	}}
	result, err := h.read(context.Background(), relayclient.Identity{Role: "owner"}, true)
	if err != nil || !result.agentConnected || result.agentPaired == nil || !*result.agentPaired {
		t.Fatalf("agent evidence = %+v / %v", result, err)
	}
}
func (h *fakeControllerHealth) Close() error { h.closed = true; return nil }
func TestControllerHealthReplacesEveryIdentityOrEndpointChange(t *testing.T) {
	var clients []*fakeControllerHealth
	h := controllerHealth{create: func(relayclient.Identity) (controllerHealthClient, error) {
		c := &fakeControllerHealth{}
		clients = append(clients, c)
		return c, nil
	}}
	identity := relayclient.Identity{RelayURL: "https://one.example", Role: "owner", Serial: "AA", PrivateKeyPEM: "key", CertificatePEM: "cert", CACertificatePEM: "ca"}
	for range 3 {
		if _, err := h.read(context.Background(), identity, true); err != nil {
			t.Fatal(err)
		}
	}
	if len(clients) != 1 || clients[0].calls != 3 {
		t.Fatal("did not reuse health client")
	}
	changes := []func(){func() { identity.RelayURL = "https://two.example" }, func() { identity.DialAddress = "127.0.0.1:8443" }, func() { identity.PrivateKeyPEM = "newkey" }, func() { identity.CertificatePEM = "newcert" }, func() { identity.CACertificatePEM = "newca" }, func() { identity.Serial = "BB" }}
	for _, change := range changes {
		old := clients[len(clients)-1]
		change()
		if _, err := h.read(context.Background(), identity, true); err != nil {
			t.Fatal(err)
		}
		if !old.closed {
			t.Fatal("old health transport remained open")
		}
	}
	last := clients[len(clients)-1]
	h.read(context.Background(), relayclient.Identity{}, false)
	if !last.closed || h.client != nil || h.identity != (relayclient.Identity{}) {
		t.Fatal("missing Owner retained identity or connections")
	}
}
