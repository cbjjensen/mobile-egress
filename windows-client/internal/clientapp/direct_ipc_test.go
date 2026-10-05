package clientapp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"testing"

	"mobile-egress/windows-client/internal/nodeservice"
)

type invitationService struct {
	testService
	canceled, revoked bool
	configureErr      error
}

func (s *invitationService) Configure(context.Context, nodeservice.DirectConfiguration) error {
	return s.configureErr
}

func TestConfigureInvalidatesCachedInvitationEvenWhenFirewallFailsAfterSave(t *testing.T) {
	for _, configureErr := range []error{nil, errors.New("Endpoint saved; firewall needs attention")} {
		service := &invitationService{configureErr: configureErr}
		copied := false
		app := New(service, func(string) error { copied = true; return nil })
		if _, err := app.IssueInvitation(); err != nil {
			t.Fatal(err)
		}
		_ = app.Configure(":8443", "https://new.example:8443", "Client")
		if err := app.CopyInvitation(); err == nil || copied {
			t.Fatal("copied invalidated invitation")
		}
	}
}

func (*invitationService) IssueInvitation(context.Context) (string, error) {
	return base64.RawURLEncoding.EncodeToString([]byte(`{"type":"direct-invitation"}`)), nil
}
func (s *invitationService) CancelInvitation(context.Context) error { s.canceled = true; return nil }
func (*invitationService) ExportEndpointUpdate(context.Context) (string, error) {
	return "signed-update", nil
}
func (s *invitationService) Revoke(context.Context) error { s.revoked = true; return nil }

func directExchange(t *testing.T, service Service, request string) Response {
	t.Helper()
	server, client := net.Pipe()
	defer client.Close()
	go serveConnection(context.Background(), server, service)
	if _, err := client.Write([]byte(request + "\n")); err != nil {
		t.Fatal(err)
	}
	var response Response
	if err := json.NewDecoder(client).Decode(&response); err != nil {
		t.Fatal(err)
	}
	return response
}

func TestDirectInvitationAndEndpointExportUseAuthenticatedIPC(t *testing.T) {
	service := &invitationService{}
	invitation, _ := service.IssueInvitation(context.Background())
	for _, tc := range []struct{ method, want string }{{"issue-invitation", invitation}, {"export-update", "signed-update"}} {
		response := directExchange(t, service, `{"method":"`+tc.method+`"}`)
		if response.Error != "" || response.Value != tc.want {
			t.Fatalf("%s failed: %#v", tc.method, response)
		}
	}
	for _, method := range []string{"cancel-invitation", "revoke"} {
		if response := directExchange(t, service, `{"method":"`+method+`"}`); response.Error != "" {
			t.Fatal(response.Error)
		}
	}
	if !service.canceled || !service.revoked {
		t.Fatal("cancellation or revocation did not reach service")
	}
}

func TestDirectIPCRejectsLegacyPairAndImport(t *testing.T) {
	service := &testService{}
	for _, method := range []string{"pair", "import"} {
		response := directExchange(t, service, `{"method":"`+method+`","value":"legacy-secret"}`)
		if response.Error == "" || service.paired != "" {
			t.Fatalf("legacy %s accepted", method)
		}
	}
}
