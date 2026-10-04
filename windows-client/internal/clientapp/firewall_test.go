package clientapp

import (
	"context"
	"errors"
	"mobile-egress/windows-client/internal/nodeservice"
	"sync/atomic"
	"testing"
	"time"
)

func TestConcurrentConfigurationKeepsFirewallOnTheLastCommittedPort(t *testing.T) {
	base := &configurableService{}
	firstApplying, releaseFirst := make(chan struct{}), make(chan struct{})
	var allowed atomic.Uint32
	service := withFirewall(base, func(_ context.Context, port uint16) error {
		if port == 8443 {
			close(firstApplying)
			<-releaseFirst
		}
		allowed.Store(uint32(port))
		return nil
	})
	doneA, doneB := make(chan error, 1), make(chan error, 1)
	go func() {
		doneA <- service.Configure(context.Background(), nodeservice.DirectConfiguration{BindAddress: ":8443", Endpoint: "https://a.example:8443"})
	}()
	<-firstApplying
	go func() {
		doneB <- service.Configure(context.Background(), nodeservice.DirectConfiguration{BindAddress: ":9443", Endpoint: "https://b.example:9443"})
	}()
	var secondError error
	secondFinished := false
	select {
	case secondError = <-doneB:
		secondFinished = true
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseFirst)
	if err := <-doneA; err != nil {
		t.Fatal(err)
	}
	if !secondFinished {
		secondError = <-doneB
	}
	if secondError != nil {
		t.Fatal(secondError)
	}
	if base.configuration.BindAddress != ":9443" || allowed.Load() != 9443 {
		t.Fatal("firewall and active endpoint diverged")
	}
}

type configurableService struct {
	invitationService
	configuration nodeservice.DirectConfiguration
}

func (s *configurableService) Configure(_ context.Context, c nodeservice.DirectConfiguration) error {
	s.configuration = c
	return nil
}

func TestEndpointConfigurationScopesFirewallToListenerPort(t *testing.T) {
	base := &configurableService{}
	var allowedPort uint16
	service := withFirewall(base, func(_ context.Context, port uint16) error { allowedPort = port; return nil })
	config := nodeservice.DirectConfiguration{BindAddress: "127.0.0.1:9443", Endpoint: "https://public.example:443", DisplayName: "Workload"}
	if err := service.Configure(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	if allowedPort != 9443 || base.configuration.Endpoint != "https://public.example:443" {
		t.Fatal("firewall used advertised port or lost endpoint")
	}
}
func TestInvalidListenerCannotMutateFirewallOrConfiguration(t *testing.T) {
	base := &configurableService{}
	service := withFirewall(base, func(context.Context, uint16) error { t.Fatal("invalid listener reached firewall"); return nil })
	if err := service.Configure(context.Background(), nodeservice.DirectConfiguration{BindAddress: "any;danger", Endpoint: "https://public.example"}); err == nil {
		t.Fatal("invalid listener accepted")
	}
	if base.configuration.Endpoint != "" {
		t.Fatal("invalid listener changed service")
	}
}
func TestFirewallFailureIsActionableAndDoesNotEchoNativeOutput(t *testing.T) {
	service := withFirewall(&configurableService{}, func(context.Context, uint16) error { return errors.New("sensitive native failure") })
	err := service.Configure(context.Background(), nodeservice.DirectConfiguration{BindAddress: ":8443", Endpoint: "https://public.example:8443"})
	if err == nil || err.Error() == "sensitive native failure" {
		t.Fatal("firewall failure hidden or leaked")
	}
}
