package relayclient

import (
	"context"
	"net/http/httptrace"
	"testing"
)

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
