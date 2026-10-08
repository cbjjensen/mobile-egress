package clientapp

import (
	"context"
	"errors"
	"mobile-egress/windows-client/internal/nodeservice"
	"net"
	"strconv"
	"sync"
	"time"
)

// FirewallStatus describes only host firewall policy, never external reachability.
type FirewallStatus struct {
	State   string `json:"state"`
	Scope   string `json:"scope"`
	Port    uint16 `json:"port"`
	Message string `json:"message"`
}

type FirewallService interface {
	CheckFirewall(context.Context) (FirewallStatus, error)
	RetryFirewall(context.Context) (FirewallStatus, error)
}

type firewallService struct {
	DirectService
	apply       func(context.Context, uint16) error
	inspect     func(context.Context, uint16, bool) FirewallStatus
	configureMu sync.Mutex
	runtimeMode string
}

func WithHostFirewall(service DirectService) DirectService {
	return withFirewall(service, configureHostFirewall)
}

// WithUserFirewall preserves all management interfaces without privileged
// firewall commands. Direct access remains an explicit manual policy decision.
func WithUserFirewall(service DirectService) DirectService {
	return &firewallService{DirectService: service, runtimeMode: "app", apply: func(context.Context, uint16) error { return nil }, inspect: func(_ context.Context, port uint16, _ bool) FirewallStatus {
		return FirewallStatus{State: "manual", Scope: "application", Port: port, Message: "Allow Inevitable Mobile Relay in System Settings > Network > Firewall if local or managed policy requires it. Ask your administrator when policy is managed. This app does not change firewall rules. Keep proxy ports 1080–1099 private."}
	}}
}

func (s *firewallService) RuntimeMode() string {
	if s.runtimeMode == "app" {
		return "app"
	}
	return "service"
}
func withFirewall(service DirectService, apply func(context.Context, uint16) error) DirectService {
	return &firewallService{DirectService: service, apply: apply, inspect: inspectHostFirewall}
}

func (s *firewallService) CheckFirewall(ctx context.Context) (FirewallStatus, error) {
	return s.firewallStatus(ctx, false)
}

func (s *firewallService) RetryFirewall(ctx context.Context) (FirewallStatus, error) {
	return s.firewallStatus(ctx, true)
}

func (s *firewallService) firewallStatus(ctx context.Context, retry bool) (FirewallStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// Waiting for Configure must also honor IPC cancellation and the timeout.
	for !s.configureMu.TryLock() {
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return FirewallStatus{}, ctx.Err()
		case <-timer.C:
		}
	}
	defer s.configureMu.Unlock()
	if err := ctx.Err(); err != nil {
		return FirewallStatus{}, err
	}
	saved := s.DirectService.Status()
	if saved.Transport == "hosted" {
		return FirewallStatus{State: "not_required", Scope: hostFirewallScope, Message: "Hosted connectivity uses outbound TLS. No inbound firewall rule is required."}, nil
	}
	_, rawPort, err := net.SplitHostPort(saved.BindAddress)
	port, portErr := strconv.ParseUint(rawPort, 10, 16)
	if saved.Endpoint == "" || err != nil || portErr != nil || port == 0 {
		return FirewallStatus{State: "unavailable", Scope: hostFirewallScope, Message: "Save the computer address and local listener port before checking its firewall."}, nil
	}
	return s.inspect(ctx, uint16(port), retry), nil
}
func (s *firewallService) Configure(ctx context.Context, c nodeservice.DirectConfiguration) error {
	s.configureMu.Lock()
	defer s.configureMu.Unlock()
	bind := c.BindAddress
	if bind == "" {
		bind = ":8443"
	}
	_, rawPort, err := net.SplitHostPort(bind)
	if err != nil {
		return errors.New("Enter a local listener address and TCP port, for example :8443.")
	}
	port, err := strconv.ParseUint(rawPort, 10, 16)
	if err != nil || port == 0 {
		return errors.New("Choose a listener TCP port from 1 to 65535.")
	}
	if err := s.DirectService.Configure(ctx, c); err != nil {
		return err
	}
	if err := s.apply(ctx, uint16(port)); err != nil {
		return errors.New("Endpoint saved, but the host firewall rule could not be configured. Use Retry firewall or allow the Client program on its selected TCP listener port; keep proxy ports 1080 and 1081 private.")
	}
	return nil
}
