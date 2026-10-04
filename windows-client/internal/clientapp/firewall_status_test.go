package clientapp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"mobile-egress/windows-client/internal/nodeservice"
)

type firewallSavedService struct {
	invitationService
	status     nodeservice.StandaloneStatus
	configures int
}

func (s *firewallSavedService) Status() nodeservice.StandaloneStatus { return s.status }
func (s *firewallSavedService) Configure(context.Context, nodeservice.DirectConfiguration) error {
	s.configures++
	return nil
}

func TestFirewallRetryPreservesSavedConfigurationAndInvitation(t *testing.T) {
	expires := time.Now().Add(time.Minute)
	base := &firewallSavedService{status: nodeservice.StandaloneStatus{BindAddress: ":9443", Endpoint: "https://public.example:443", Generation: 7, InvitationExpiresAt: &expires}}
	before := base.status
	svc := withFirewall(base, func(context.Context, uint16) error { t.Fatal("retry invoked Configure firewall path"); return nil }).(*firewallService)
	svc.inspect = func(_ context.Context, port uint16, retry bool) FirewallStatus {
		if port != 9443 || !retry {
			t.Fatalf("wrong target port/retry: %d %v", port, retry)
		}
		return FirewallStatus{State: "allowed", Scope: "port", Port: port}
	}
	status, err := svc.RetryFirewall(context.Background())
	if err != nil || status.State != "allowed" || status.Port != 9443 {
		t.Fatalf("retry: %#v, %v", status, err)
	}
	if base.configures != 0 || base.status != before || base.canceled || base.revoked {
		t.Fatal("retry changed pairing or configuration")
	}
}

func TestFirewallCheckRequiresSavedEndpointAndValidLocalPort(t *testing.T) {
	for _, saved := range []nodeservice.StandaloneStatus{{}, {Endpoint: "https://public.example:443", BindAddress: "bad"}, {Endpoint: "https://public.example:443", BindAddress: ":0"}} {
		svc := withFirewall(&firewallSavedService{status: saved}, nil).(*firewallService)
		svc.inspect = func(context.Context, uint16, bool) FirewallStatus {
			t.Fatal("invalid saved endpoint reached firewall")
			return FirewallStatus{}
		}
		status, err := svc.CheckFirewall(context.Background())
		if err != nil || status.State != "unavailable" {
			t.Fatalf("invalid saved endpoint: %#v %v", status, err)
		}
	}
}

func TestFirewallCheckIsReadOnlyAndHonorsCancellation(t *testing.T) {
	svc := withFirewall(&firewallSavedService{status: nodeservice.StandaloneStatus{Endpoint: "https://public.example", BindAddress: ":8443"}}, nil).(*firewallService)
	svc.inspect = func(ctx context.Context, port uint16, retry bool) FirewallStatus {
		if retry {
			t.Fatal("check requested a mutation")
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 6*time.Second {
			t.Fatal("firewall operation is not bounded")
		}
		return FirewallStatus{State: "blocked", Scope: "port", Port: port}
	}
	if status, err := svc.CheckFirewall(context.Background()); err != nil || status.State != "blocked" {
		t.Fatalf("check: %#v %v", status, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := svc.RetryFirewall(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestMacFirewallStatesPreserveGlobalPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, global, block, app, want string
		retry                          bool
	}{
		{"disabled", "Firewall is disabled. (State = 0)", "", "", "disabled", true},
		{"block all", "Firewall is enabled. (State = 1)", "Firewall has block all state set to enabled.", "", "blocked", true},
		{"allowed", "Firewall is enabled. (State = 1)", "Firewall has block all state set to disabled.", "The application is not blocked", "allowed", false},
		{"native allowed", "Firewall is enabled. (State = 1)", "Firewall has block all state set to disabled.", "Incoming connection to /Library/Application Support/MobileEgressClient/bin/mobile-egress-client is permitted.", "allowed", false},
		{"unrelated application", "Firewall is enabled. (State = 1)", "Firewall has block all state set to disabled.", "Incoming connection to /usr/bin/ssh is permitted.", "unknown", false},
		{"blocked", "Firewall is enabled. (State = 1)", "Firewall has block all state set to disabled.", "The application is blocked", "blocked", false},
		{"unknown", "unrecognized localized output", "", "", "unknown", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := func(_ context.Context, command string, args, env []string) ([]byte, error) {
				if command != "/usr/libexec/ApplicationFirewall/socketfilterfw" {
					t.Fatalf("unexpected executable: %s", command)
				}
				switch args[0] {
				case "--getglobalstate":
					return []byte(tc.global), nil
				case "--getblockall":
					return []byte(tc.block), nil
				case "--getappblocked":
					if len(args) != 2 || args[1] != "/Library/Application Support/MobileEgressClient/bin/mobile-egress-client" {
						t.Fatal("wrong application")
					}
					return []byte(tc.app), nil
				default:
					t.Fatalf("changed global or app policy unexpectedly: %v", args)
					return nil, nil
				}
			}
			status := macFirewall(context.Background(), 8443, tc.retry, func(context.Context) error { return nil }, run)
			if status.State != tc.want || status.Scope != "application" || status.Port != 8443 {
				t.Fatalf("status: %#v", status)
			}
		})
	}
}

func TestMacFirewallRetryReadsBackOnlyTheInstalledApplication(t *testing.T) {
	for _, tc := range []struct {
		name, readback, want string
		fail                 bool
	}{
		{"allowed", "The application is not blocked", "allowed", false},
		{"managed denial", "The application is blocked", "blocked", false},
		{"unsupported output", "unexpected", "unknown", false},
		{"failure", "", "unavailable", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string
			run := func(_ context.Context, command string, args, env []string) ([]byte, error) {
				if command != "/usr/libexec/ApplicationFirewall/socketfilterfw" {
					t.Fatal("wrong executable")
				}
				calls = append(calls, args[0])
				if len(args) > 1 && args[1] != "/Library/Application Support/MobileEgressClient/bin/mobile-egress-client" {
					t.Fatal("wrong installed app")
				}
				switch args[0] {
				case "--getglobalstate":
					return []byte("Firewall is enabled. (State = 1)"), nil
				case "--getblockall":
					return []byte("Firewall has block all state set to disabled."), nil
				case "--add":
					return nil, nil
				case "--unblockapp":
					if tc.fail {
						return []byte("private native error"), errors.New("private error")
					}
					return nil, nil
				case "--getappblocked":
					return []byte(tc.readback), nil
				default:
					t.Fatalf("unexpected command: %v", args)
					return nil, nil
				}
			}
			status := macFirewall(context.Background(), 9443, true, func(context.Context) error { return nil }, run)
			if status.State != tc.want || strings.Contains(status.Message, "private") {
				t.Fatalf("status: %#v", status)
			}
			if !tc.fail && strings.Join(calls, ",") != "--getglobalstate,--getblockall,--add,--unblockapp,--getglobalstate,--getblockall,--getappblocked" {
				t.Fatalf("missing readback: %v", calls)
			}
		})
	}
}

func TestMacFirewallRejectsUntrustedDaemonBeforeAnyCommand(t *testing.T) {
	status := macFirewall(context.Background(), 8443, true, func(context.Context) error { return errors.New("untrusted path") }, func(context.Context, string, []string, []string) ([]byte, error) {
		t.Fatal("untrusted daemon reached firewall")
		return nil, nil
	})
	if status.State != "unavailable" {
		t.Fatalf("status: %#v", status)
	}
}

func TestFirewallCheckWaitingForConfigureHonorsCancellation(t *testing.T) {
	svc := withFirewall(&firewallSavedService{}, nil).(*firewallService)
	svc.configureMu.Lock()
	defer svc.configureMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := svc.CheckFirewall(ctx)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Fatalf("lock ignored context: %v", err)
	}
}

func TestFirewallNativeOutputIsBounded(t *testing.T) {
	var output firewallOutput
	if _, err := output.Write(make([]byte, 16*1024)); err != nil {
		t.Fatal(err)
	}
	if _, err := output.Write([]byte("private oversized output")); err == nil || len(output.data) != 16*1024 {
		t.Fatal("native output is not bounded")
	}
}
