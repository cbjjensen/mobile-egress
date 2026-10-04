package clientapp

import (
	"context"
	"encoding/json"
	"net"
	"testing"

	"mobile-egress/windows-client/internal/nodeservice"
	"mobile-egress/windows-client/internal/securestore"
)

func TestFirewallWrapperForwardsHostedOperationsWithoutInboundRule(t *testing.T) {
	direct := nodeservice.NewDirect(nodeservice.NewRepository(securestore.NewMemoryStore()), "windows", "amd64", "test")
	applied := false
	wrapped := withFirewall(direct, func(context.Context, uint16) error { applied = true; return nil })
	hosted, ok := wrapped.(HostedService)
	if !ok {
		t.Fatal("firewall wrapper hid hosted service")
	}
	view, err := hosted.ResumeHostedActivation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if view.State != "inactive" || applied {
		t.Fatal("hosted activation changed firewall")
	}
}

func TestHostedIPCRejectsSecretsAndAcceptsSafeResume(t *testing.T) {
	direct := nodeservice.NewDirect(nodeservice.NewRepository(securestore.NewMemoryStore()), "windows", "amd64", "test")
	service := withFirewall(direct, func(context.Context, uint16) error { t.Fatal("unexpected firewall"); return nil })
	for _, value := range []string{"", `{"deviceToken":"secret"}`} {
		client, server := net.Pipe()
		go serveConnection(context.Background(), server, service)
		if err := json.NewEncoder(client).Encode(Request{Method: "resume-hosted-activation", Value: value}); err != nil {
			t.Fatal(err)
		}
		var response Response
		if err := json.NewDecoder(client).Decode(&response); err != nil {
			t.Fatal(err)
		}
		client.Close()
		if value == "" && (response.Activation == nil || response.Activation.State != "inactive") {
			t.Fatalf("resume: %+v", response)
		}
		if value != "" && response.Error == "" {
			t.Fatal("secret-bearing activation request accepted")
		}
	}
}

func TestBrowserOpenerRejectsArbitraryURLs(t *testing.T) {
	opened := ""
	app := NewWithBrowser(nil, nil, func(raw string) error { opened = raw; return nil })
	if err := app.openActivationBrowser("https://attacker.example/"); err == nil || opened != "" {
		t.Fatal("unsafe browser URL")
	}
	safe := "https://inevitableproxies.com/mobile-egress/approve?requestId=12345678-1234-4234-8234-123456789012"
	if err := app.openActivationBrowser(safe); err != nil || opened != safe {
		t.Fatalf("approved opener: %v", err)
	}
}
