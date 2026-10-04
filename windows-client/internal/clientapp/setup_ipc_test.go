package clientapp

import (
	"context"
	"testing"
)

type setupFirewallService struct {
	testService
	checks, retries int
}

func (s *setupFirewallService) CheckFirewall(context.Context) (FirewallStatus, error) {
	s.checks++
	return FirewallStatus{State: "blocked", Scope: "application", Port: 8443, Message: "Check firewall settings."}, nil
}
func (s *setupFirewallService) RetryFirewall(context.Context) (FirewallStatus, error) {
	s.retries++
	return FirewallStatus{State: "allowed", Scope: "application", Port: 8443, Message: "Application allowed."}, nil
}

func TestSetupFirewallIPCOnlyExposesFixedServiceOperations(t *testing.T) {
	service := &setupFirewallService{}
	for _, method := range []string{"check-firewall", "retry-firewall"} {
		response := directExchange(t, service, `{"method":"`+method+`"}`)
		if response.Error != "" || response.Firewall == nil || response.Firewall.Port != 8443 {
			t.Fatalf("firewall operation failed: %#v", response)
		}
		response = directExchange(t, service, `{"method":"`+method+`","value":"arbitrary-program.exe"}`)
		if response.Error == "" {
			t.Fatal("caller-controlled firewall arguments accepted")
		}
	}
	if service.checks != 1 || service.retries != 1 {
		t.Fatal("invalid request reached firewall")
	}
	response := directExchange(t, &testService{}, `{"method":"check-firewall"}`)
	if response.Error == "" {
		t.Fatal("unsupported service pretended to check firewall")
	}
}

func TestSetupFirewallBindingsPreserveCachedInvitation(t *testing.T) {
	service := &setupFirewallService{}
	app := New(service, nil)
	app.invitation = "private-invitation"
	checked, err := app.CheckFirewall()
	if err != nil || checked.State != "blocked" {
		t.Fatalf("check: %#v %v", checked, err)
	}
	retried, err := app.RetryFirewall()
	if err != nil || retried.State != "allowed" {
		t.Fatalf("retry: %#v %v", retried, err)
	}
	if app.invitation != "private-invitation" {
		t.Fatal("firewall action invalidated pairing")
	}
}
