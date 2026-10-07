package clientapp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"mobile-egress/windows-client/internal/nodeservice"
	"mobile-egress/windows-client/internal/securestore"
	"strings"
	"testing"
)

// This boundary double models two different phone credentials. Choosing a
// legacy operation or dropping the phone ID would expose the wrong credential.
type phoneTestService struct {
	invitationService
	names   map[string]string
	removed string
	retried string
	pending string
}

func (s *phoneTestService) Phones(context.Context) (nodeservice.PhonesStatus, error) {
	view := nodeservice.PhonesStatus{MaxPhones: 10, PendingPhoneID: s.pending, Phones: []nodeservice.PhoneStatus{{ID: "phone-a", Name: s.names["phone-a"], Slot: 0, Paired: true, HTTPAddress: "127.0.0.1:1081"}, {ID: "phone-b", Name: s.names["phone-b"], Slot: 1, Paired: true, HTTPAddress: "127.0.0.1:1083"}}}
	if s.pending != "" {
		view.Phones = append(view.Phones, nodeservice.PhoneStatus{ID: s.pending, Name: "Pending", Slot: 2})
	}
	return view, nil
}
func (s *phoneTestService) AddPhone(_ context.Context, name string) (nodeservice.PhoneInvitation, error) {
	s.pending = "phone-c"
	return nodeservice.PhoneInvitation{PhoneID: "phone-c", Bundle: base64.RawURLEncoding.EncodeToString([]byte(`{"type":"direct-invitation"}`))}, nil
}
func (s *phoneTestService) CancelPhoneInvitation(_ context.Context, id string) error {
	if id != "phone-c" {
		return errors.New("Unknown phone.")
	}
	s.canceled = true
	s.pending = ""
	return nil
}
func (s *phoneTestService) RenamePhone(_ context.Context, id, name string) error {
	if _, ok := s.names[id]; !ok {
		return errors.New("Unknown phone.")
	}
	s.names[id] = name
	return nil
}
func (s *phoneTestService) RevokePhone(_ context.Context, id string) error {
	if _, ok := s.names[id]; !ok {
		return errors.New("Unknown phone.")
	}
	s.removed = id
	return nil
}
func (s *phoneTestService) PhoneProxy(_ context.Context, id, kind string) (string, error) {
	if _, ok := s.names[id]; !ok {
		return "", errors.New("Unknown phone.")
	}
	return id + ":" + kind + ":private-secret", nil
}
func (s *phoneTestService) ExportPhoneEndpointUpdate(_ context.Context, id string) (string, error) {
	if _, ok := s.names[id]; !ok {
		return "", errors.New("Unknown phone.")
	}
	return base64.RawURLEncoding.EncodeToString([]byte(`{"type":"direct-endpoint-update","phone":"` + id + `"}`)), nil
}
func (s *phoneTestService) RetryPhoneProxy(_ context.Context, id string) error {
	if _, ok := s.names[id]; !ok {
		return errors.New("Unknown phone.")
	}
	s.retried = id
	return nil
}
func newPhoneTestService() *phoneTestService {
	return &phoneTestService{names: map[string]string{"phone-a": "One", "phone-b": "Two"}}
}

func TestPhoneIPCStatusHasNoSecretsAndLegacyShapeIsUnchanged(t *testing.T) {
	s := newPhoneTestService()
	r := directExchange(t, s, `{"method":"phones"}`)
	raw, _ := json.Marshal(r)
	if r.Phones == nil || len(r.Phones.Phones) != 2 || strings.Contains(string(raw), "private-secret") || r.Invitation != nil || r.Value != "" {
		t.Fatalf("unsafe phone status: %s", raw)
	}
	old := directExchange(t, s, `{"method":"status"}`)
	if old.Status == nil || old.Phones != nil || old.Invitation != nil {
		t.Fatalf("legacy response changed: %#v", old)
	}
}

func TestPhoneIPCRejectsInvalidPayloadsWithoutReturningSecrets(t *testing.T) {
	s := newPhoneTestService()
	for _, request := range []string{
		`{"method":"phone-proxy","phoneId":"missing","kind":"http"}`,
		`{"method":"phone-proxy","phoneId":"phone-a","kind":"unknown"}`,
		`{"method":"phone-proxy","kind":"http"}`,
		`{"method":"phone-proxy","phoneId":"phone-a","kind":"http","value":"secret"}`,
		`{"method":"rename-phone","phoneId":"phone-a","name":" "}`,
		`{"method":"rename-phone","phoneId":"phone-a","name":"bad\nname"}`,
		`{"method":"rename-phone","phoneId":"phone-a","name":"\nOne"}`,
		`{"method":"add-phone","name":"\n"}`,
		`{"method":"phones","phoneId":"phone-a"}`,
		`{"method":"status","phoneId":"phone-a"}`,
		`{"method":"revoke-phone","phoneId":"phone-a","name":"wrong"}`,
	} {
		r := directExchange(t, s, request)
		if r.Error == "" || r.Value != "" || r.Invitation != nil {
			t.Fatalf("accepted invalid request %s: %#v", request, r)
		}
	}
	if s.names["phone-a"] != "One" || s.removed != "" {
		t.Fatal("invalid request mutated a phone")
	}
}

func TestPhoneIPCAndAppScopeMutationsAndClipboardToExplicitPhone(t *testing.T) {
	s := newPhoneTestService()
	var copied string
	app := New(withFirewall(s, func(context.Context, uint16) error { return nil }), func(value string) error { copied = value; return nil })
	if err := app.CopyPhoneProxy("phone-b", "http"); err != nil || copied != "phone-b:http:private-secret" {
		t.Fatalf("wrong phone proxy: %q %v", copied, err)
	}
	if err := app.CopyPhoneProxy("missing", "http"); err == nil {
		t.Fatal("unknown phone fell back to legacy proxy")
	}
	if err := app.RenamePhone("phone-b", "  Travel  "); err != nil || s.names["phone-b"] != "Travel" {
		t.Fatalf("rename: %v %#v", err, s.names)
	}
	r := directExchange(t, s, `{"method":"phone-proxy","phoneId":"phone-a","kind":"socks"}`)
	if r.Error != "" || r.Value != "phone-a:socks:private-secret" {
		t.Fatalf("wrong IPC phone: %#v", r)
	}
	if err := app.RetryPhoneProxy("phone-b"); err != nil || s.retried != "phone-b" {
		t.Fatalf("retry: %v", err)
	}
	if err := app.RevokePhone("phone-b"); err != nil || s.removed != "phone-b" || s.revoked {
		t.Fatalf("revoke: %v", err)
	}
	view, err := app.AddPhone("")
	if err != nil || view.PhoneID != "phone-c" || view.QRDataURL == "" {
		t.Fatalf("invitation: %#v %v", view, err)
	}
	if err := app.CancelPhoneInvitation("phone-c"); err != nil || !s.canceled {
		t.Fatalf("cancel: %v", err)
	}
}

func TestPhoneAppUnsupportedServiceDoesNotFallbackToLegacy(t *testing.T) {
	s := &invitationService{}
	app := New(s, func(string) error { t.Fatal("legacy secret copied"); return nil })
	if _, err := app.Phones(); err == nil {
		t.Fatal("unsupported phone list succeeded")
	}
	if err := app.CopyPhoneProxy("phone-a", "http"); err == nil {
		t.Fatal("unsupported service used legacy copy")
	}
	if err := app.RevokePhone("phone-a"); err == nil || s.revoked {
		t.Fatal("unsupported service used legacy removal")
	}
}

func TestPhoneInvitationClipboardRejectsWrongIDAndInvalidatesAfterCancel(t *testing.T) {
	s := newPhoneTestService()
	var copied string
	app := New(s, func(value string) error { copied = value; return nil })
	view, err := app.AddPhone("")
	if err != nil {
		t.Fatal(err)
	}
	if err := app.CopyPhoneInvitation("phone-a"); err == nil || copied != "" {
		t.Fatal("wrong phone copied a pending invitation")
	}
	if err := app.CopyPhoneInvitation("phone-c"); err != nil || copied != view.Bundle {
		t.Fatalf("scoped invitation copy: %v", err)
	}
	copied = ""
	if err := app.CopyInvitation(); err == nil || copied != "" {
		t.Fatal("legacy copy implicitly selected a multi-phone invitation")
	}
	if err := app.CancelPhoneInvitation("phone-c"); err != nil {
		t.Fatal(err)
	}
	if err := app.CopyPhoneInvitation("phone-c"); err == nil || copied != "" {
		t.Fatal("canceled invitation copied")
	}
}
func TestPhoneInvitationClipboardRejectsExternalCancellation(t *testing.T) {
	s := newPhoneTestService()
	copied := false
	app := New(s, func(string) error { copied = true; return nil })
	if _, err := app.AddPhone(""); err != nil {
		t.Fatal(err)
	}
	s.pending = ""
	if err := app.CopyPhoneInvitation("phone-c"); err == nil || copied {
		t.Fatal("externally canceled code copied")
	}
}
func TestPhoneNameValidationCountsRunesAndRejectsRawControls(t *testing.T) {
	s := newPhoneTestService()
	app := New(s, nil)
	name := strings.Repeat("📱", 64)
	if err := app.RenamePhone("phone-b", name); err != nil || s.names["phone-b"] != name {
		t.Fatalf("64-rune label rejected: %v", err)
	}
	for _, bad := range []string{strings.Repeat("📱", 65), "\nOne", "One\t", "\x00One", "  "} {
		if err := app.RenamePhone("phone-b", bad); err == nil {
			t.Errorf("invalid label %q accepted", bad)
		}
	}
	if s.names["phone-b"] != name {
		t.Fatal("invalid label mutated the saved name")
	}
}
func TestPhoneIPCRealAuthorityRejectsUnknownIDsAndKeepsInvitationOutOfStatus(t *testing.T) {
	s := nodeservice.NewDirect(nodeservice.NewRepository(securestore.NewMemoryStore()), "windows", "amd64", "test")
	if err := s.Configure(context.Background(), nodeservice.DirectConfiguration{Transport: "direct", Endpoint: "https://client.example:8443", DisplayName: "Client"}); err != nil {
		t.Fatal(err)
	}
	created := directExchange(t, s, `{"method":"add-phone","name":"Travel"}`)
	if created.Error != "" || created.Invitation == nil || created.Invitation.PhoneID == "" {
		t.Fatalf("add: %#v", created)
	}
	secret := created.Invitation.Bundle
	for _, request := range []string{
		`{"method":"phone-proxy","phoneId":"unknown-phone","kind":"http"}`,
		`{"method":"rename-phone","phoneId":"unknown-phone","name":"Wrong"}`,
		`{"method":"revoke-phone","phoneId":"unknown-phone"}`,
		`{"method":"cancel-phone-invitation","phoneId":"unknown-phone"}`,
		`{"method":"export-phone-update","phoneId":"unknown-phone"}`,
		`{"method":"retry-phone-proxy","phoneId":"unknown-phone"}`,
	} {
		r := directExchange(t, s, request)
		if r.Error == "" || r.Value != "" || r.Invitation != nil {
			t.Fatalf("unknown ID accepted: %s %#v", request, r)
		}
	}
	listed := directExchange(t, s, `{"method":"phones"}`)
	raw, _ := json.Marshal(listed)
	if listed.Error != "" || listed.Phones == nil || len(listed.Phones.Phones) != 1 || listed.Phones.Phones[0].Name != "Travel" || listed.Invitation != nil || strings.Contains(string(raw), secret) || strings.Contains(string(raw), "capability") || strings.Contains(string(raw), "password") {
		t.Fatalf("unsafe/mutated authority status: %s", raw)
	}
}
func TestPhoneInvitationCacheSurvivesSiblingRemovalAndFailedConfigureInvalidates(t *testing.T) {
	s := newPhoneTestService()
	var copied string
	app := New(s, func(value string) error { copied = value; return nil })
	view, err := app.AddPhone("")
	if err != nil {
		t.Fatal(err)
	}
	if err := app.RevokePhone("phone-b"); err != nil {
		t.Fatal(err)
	}
	if err := app.CopyPhoneInvitation(view.PhoneID); err != nil || copied != view.Bundle {
		t.Fatalf("sibling removal cleared invitation: %v", err)
	}
	copied = ""
	s.configureErr = errors.New("Endpoint saved; firewall needs attention")
	if err := app.Configure(":8443", "https://changed.example:8443", "Client"); err == nil {
		t.Fatal("configure error lost")
	}
	if err := app.CopyPhoneInvitation(view.PhoneID); err == nil || copied != "" {
		t.Fatal("configuration change retained cached secret")
	}
}
