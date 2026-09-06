package relayclient

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"testing"
)

func TestHealthClientNegotiatesOptionalPairingEvidenceWithOlderRelayFallback(t *testing.T) {
	for _, field := range []string{"", `,"agentPaired":false`, `,"agentPaired":true`} {
		t.Run(field, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.Header.Get("X-Mobile-Egress-Health-Agent-Pairing") != "1" {
					t.Error("missing pairing evidence opt-in")
				}
				fmt.Fprintf(writer, `{"readiness":true,"agentConnected":false,"connectedClients":0,"activeStreams":0,"totalStreams":0,"byteCount":0,"errorCounts":{}%s}`, field)
			}))
			defer server.Close()
			client := &HealthClient{client: server.Client(), url: server.URL}
			health, err := client.Health(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if field == "" {
				if health.AgentPaired != nil {
					t.Fatal("older relay reported known pairing")
				}
			} else if health.AgentPaired == nil || *health.AgentPaired != (field == `,"agentPaired":true`) {
				t.Fatalf("pairing evidence = %v", health.AgentPaired)
			}
		})
	}
}

func TestHealthClientReusesAuthenticatedConnection(t *testing.T) {
	identity, server, requests := newControlFixture(t, "owner")
	defer server.Close()
	client, err := NewHealthClient(identity)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	for i := 0; i < 2; i++ {
		reused := false
		ctx := httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { reused = info.Reused }})
		health, err := client.Health(ctx)
		if err != nil || !health.Readiness || health.ActiveStreams != 2 {
			t.Fatalf("health = %v / %v", health, err)
		}
		if i == 1 && !reused {
			t.Fatal("second health request created a new TLS connection")
		}
		<-requests
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Health(context.Background()); err == nil {
		t.Fatal("closed client accepted health request")
	}
}

func TestHealthClientPreservesLoopbackTLSNameAndRejectsInvalidOverrides(t *testing.T) {
	identity, server, _ := newControlFixture(t, "owner")
	defer server.Close()
	identity.RelayURL = "https://bridge.tail123.ts.net:8443"
	identity.DialAddress = "127.0.0.1:8443"
	client, err := NewHealthClient(identity)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if client.transport.DialContext == nil || client.transport.TLSClientConfig.ServerName != "bridge.tail123.ts.net" || client.transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("loopback override lost relay TLS verification")
	}
	for _, address := range []string{"127.0.0.1:8444", "192.0.2.1:8443"} {
		identity.DialAddress = address
		if _, err := NewHealthClient(identity); err == nil {
			t.Fatalf("accepted override %s", address)
		}
	}
}
