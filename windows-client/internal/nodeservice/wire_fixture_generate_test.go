package nodeservice

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"
)

// This opt-in generator emits only disposable public test material. No private
// key or production capability is written to the shared interoperability fixture.
func TestGenerateDirectWireFixture(t *testing.T) {
	path := os.Getenv("MOBILE_EGRESS_WIRE_FIXTURE")
	if path == "" {
		t.Skip("fixture generation is opt-in")
	}
	m, invitation, csr := directTestConfigured(t)
	w := directTestRequest(m, "/v2/direct/enroll", directEnrollBody(invitation, csr), nil)
	if w.Code != 201 {
		t.Fatal(w.Code)
	}
	var identity directIdentityResponse
	if err := json.Unmarshal(w.Body.Bytes(), &identity); err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{"https://second.example:9443", "https://third.example:10443"} {
		if err := m.Configure(context.Background(), DirectConfiguration{Endpoint: endpoint, DisplayName: "Client"}); err != nil {
			t.Fatal(err)
		}
	}
	update, err := m.ExportEndpointUpdate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	rawInvitation, _ := json.Marshal(invitation)
	rawUpdate, _ := base64.RawURLEncoding.DecodeString(update)
	var wrapper struct {
		Payload string `json:"payload"`
	}
	_ = json.Unmarshal(rawUpdate, &wrapper)
	rawPayload, _ := base64.RawURLEncoding.DecodeString(wrapper.Payload)
	var retainedUpdates []map[string]any
	for _, endpoint := range []string{
		"https://client.example:08443",
		"https://CLIENT.Example:00443",
		"https://[2001:0db8:0000:0000:0000:0000:0000:0001]:08443",
	} {
		// Recreate already-persisted direct state, then export through the same
		// public manager method used for QR/file recovery. Configure intentionally
		// canonicalizes new input, so it cannot construct this regression fixture.
		next := m.cloneLocked()
		next.Configuration.Endpoint = endpoint
		if err := directServerCertificate(next, endpoint); err != nil {
			t.Fatal(err)
		}
		if err := m.saveLocked(context.Background(), next); err != nil {
			t.Fatal(err)
		}
		restarted := NewDirect(m.repository, "windows", "amd64", "2.0.0")
		retained, err := restarted.ExportEndpointUpdate(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if got := directEndpointFromUpdate(t, retained); got != endpoint {
			t.Fatalf("retained signed spelling changed: %q", got)
		}
		canonical, err := directHTTPSOrigin(endpoint)
		if err != nil {
			t.Fatal(err)
		}
		retainedUpdates = append(retainedUpdates, map[string]any{
			"endpoint": endpoint, "canonicalEndpoint": canonical.String(),
			"generation": next.Generation, "update": retained,
		})
	}
	// Export a hosted transition through the same signing boundary. No gateway
	// activation credentials are needed or written into this public fixture.
	next := m.cloneLocked()
	next.Configuration.Transport = "hosted"
	next.Configuration.BindAddress = ""
	next.Configuration.Endpoint = "https://r-fixture.gateway.example"
	next.Generation++
	if err := directServerCertificate(next, next.Configuration.Endpoint); err != nil {
		t.Fatal(err)
	}
	if err := m.saveLocked(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	hostedUpdate, err := m.ExportEndpointUpdate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	fixture := map[string]any{
		"description": "Disposable Go-generated direct/1 interoperability fixture. Public certificates and an unusable test invitation only; private authority and phone keys discarded.",
		"clientId":    invitation.ClientID, "pairingId": identity.PairingID,
		"caCertificatePem": invitation.CACertificatePEM, "csrPem": csr,
		"invitation":          base64.RawURLEncoding.EncodeToString(rawInvitation),
		"invitationExpiresAt": invitation.ExpiresAt, "identity": identity,
		"update": update, "updatePayload": json.RawMessage(rawPayload),
		"retainedUpdates": retainedUpdates,
		"hostedUpdate":    hostedUpdate,
	}
	raw, err := json.MarshalIndent(fixture, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}
