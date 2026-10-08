package clientapp

import (
	"context"
	"strings"
	"testing"

	"mobile-egress/windows-client/internal/nodeservice"
	"mobile-egress/windows-client/internal/securestore"
)

func TestAppFirewallPreservesHostedPhoneAndDirectManagement(t *testing.T) {
	direct := nodeservice.NewDirect(nodeservice.NewRepository(securestore.NewMemoryStore()), "macos", "arm64", "test")
	service := WithUserFirewall(direct)
	for _, kind := range []string{"direct", "phones", "hosted", "firewall"} {
		supported := false
		switch kind {
		case "direct":
			_, supported = service.(DirectService)
		case "phones":
			_, supported = service.(PhoneService)
		case "hosted":
			_, supported = service.(HostedService)
		case "firewall":
			_, supported = service.(FirewallService)
		}
		if !supported {
			t.Fatalf("app firewall hid %s interface", kind)
		}
	}
	if _, err := service.IssueInvitation(context.Background()); err == nil {
		t.Fatal("invitation bypassed configuration")
	}
	firewall := service.(FirewallService)
	status, err := firewall.RetryFirewall(context.Background())
	if err != nil || status.State != "not_required" {
		t.Fatalf("hosted app requested a firewall rule: %#v %v", status, err)
	}
	if err := service.Configure(context.Background(), nodeservice.DirectConfiguration{Transport: "direct", BindAddress: "127.0.0.1:9443", Endpoint: "https://client.example:9443", DisplayName: "Mac"}); err != nil {
		t.Fatalf("app attempted privileged firewall setup: %v", err)
	}
	for _, check := range []func(context.Context) (FirewallStatus, error){firewall.CheckFirewall, firewall.RetryFirewall} {
		status, err = check(context.Background())
		if err != nil || status.State != "manual" || status.Port != 9443 || !strings.Contains(status.Message, "System Settings") || !strings.Contains(status.Message, "proxy ports") {
			t.Fatalf("app lacked actionable manual policy: %#v %v", status, err)
		}
	}
	invitation, err := service.(PhoneService).AddPhone(context.Background(), "Phone")
	if err != nil || invitation.PhoneID == "" {
		t.Fatalf("phone management lost: %#v %v", invitation, err)
	}
	app := New(service, nil)
	if app.SetupInfo().RuntimeMode != "app" {
		t.Fatal("app lifecycle metadata unavailable to UI")
	}
	if New(&testService{}, nil).SetupInfo().RuntimeMode != "service" {
		t.Fatal("ordinary service lifecycle changed")
	}
}

func TestAppOperationsStopAtAppLifetimeCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	direct := nodeservice.NewDirect(nodeservice.NewRepository(securestore.NewMemoryStore()), "macos", "arm64", "test")
	app := NewWithLifetime(WithUserFirewall(direct), ctx, nil, nil)
	if err := app.Configure("127.0.0.1:9443", "https://client.example:9443", "Mac"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Phones(); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := app.Configure("127.0.0.1:9443", "https://client.example:9443", "Mac"); err == nil {
		t.Fatal("configuration survived app cancellation")
	}
	if _, err := app.Phones(); err == nil {
		t.Fatal("phone operation survived app cancellation")
	}
	if _, err := app.StartHostedActivation("Mac"); err == nil {
		t.Fatal("activation survived app cancellation")
	}
	if direct.Status().Endpoint != "https://client.example:9443" {
		t.Fatal("canceled app altered existing configuration")
	}
}
