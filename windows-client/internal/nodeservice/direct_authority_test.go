package nodeservice

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"mobile-egress/windows-client/internal/securestore"
)

var directAuthorityCases = []struct {
	name, endpoint, canonical, authority string
}{
	{"expanded IPv6", "https://[2001:0db8:0000:0000:0000:0000:0000:0001]:8443", "https://[2001:db8::1]:8443", "[2001:db8::1]:8443"},
	{"padded port", "https://client.example:08443", "https://client.example:8443", "client.example:8443"},
	{"default port", "https://client.example:00443", "https://client.example", "client.example"},
	{"DNS case", "https://CLIENT.Example:8443", "https://client.example:8443", "client.example:8443"},
	{"IPv6 default port", "https://[2001:0db8:0:0:0:0:0:1]:443", "https://[2001:db8::1]", "[2001:db8::1]"},
}

func directAuthorityRequest(t *testing.T, m *Direct, method, path, authority string, body any, identity directIdentityResponse) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(method, "https://client.example:8443"+path, bytes.NewReader(raw))
	r.Host = authority
	r.Header.Set("X-Mobile-Egress-Protocol", "direct/1")
	block, _ := pem.Decode([]byte(identity.CertificatePEM))
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	r.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{certificate}, VerifiedChains: [][]*x509.Certificate{{certificate}}}
	w := httptest.NewRecorder()
	m.handler().ServeHTTP(w, r)
	return w
}

func directEndpointFromUpdate(t *testing.T, bundle string) string {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(bundle)
	if err != nil {
		t.Fatal(err)
	}
	var wrapper struct{ Payload string }
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		t.Fatal(err)
	}
	payload, err := base64.RawURLEncoding.DecodeString(wrapper.Payload)
	if err != nil {
		t.Fatal(err)
	}
	var update struct{ Endpoint string }
	if err := json.Unmarshal(payload, &update); err != nil {
		t.Fatal(err)
	}
	return update.Endpoint
}

func TestDirectCanonicalOriginsReachInvitationAndSignedUpdate(t *testing.T) {
	for _, tc := range directAuthorityCases {
		t.Run(tc.name, func(t *testing.T) {
			m, _, csr := directTestConfigured(t)
			if err := m.Configure(context.Background(), DirectConfiguration{Endpoint: tc.endpoint, DisplayName: "Client"}); err != nil {
				t.Fatal(err)
			}
			bundle, err := m.IssueInvitation(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := base64.RawURLEncoding.DecodeString(bundle)
			var invitation directInvitation
			if err := json.Unmarshal(raw, &invitation); err != nil {
				t.Fatal(err)
			}
			if invitation.Endpoint != tc.canonical {
				t.Errorf("invitation endpoint = %q; want mobile-compatible %q", invitation.Endpoint, tc.canonical)
			}
			identity := directTestEnroll(t, m, invitation, csr)
			update, err := m.ExportEndpointUpdate(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if endpoint := directEndpointFromUpdate(t, update); endpoint != tc.canonical {
				t.Errorf("signed endpoint = %q; want %q", endpoint, tc.canonical)
			}
			ack := map[string]any{"clientId": identity.ClientID, "pairingId": identity.PairingID, "generation": identity.Generation}
			if w := directAuthorityRequest(t, m, http.MethodPost, "/v2/direct/ack", tc.authority, ack, identity); w.Code != http.StatusOK {
				t.Errorf("canonical phone ACK = %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestDirectCanonicalOriginsAcceptEquivalentPhoneAuthority(t *testing.T) {
	for _, tc := range directAuthorityCases {
		t.Run(tc.name, func(t *testing.T) {
			m, _, csr := directTestConfigured(t)
			if err := m.Configure(context.Background(), DirectConfiguration{Endpoint: tc.canonical, DisplayName: "Client"}); err != nil {
				t.Fatal(err)
			}
			bundle, err := m.IssueInvitation(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := base64.RawURLEncoding.DecodeString(bundle)
			var invitation directInvitation
			if err := json.Unmarshal(raw, &invitation); err != nil {
				t.Fatal(err)
			}
			identity := directTestEnroll(t, m, invitation, csr)
			origin, _ := url.Parse(tc.endpoint)
			ack := map[string]any{"clientId": identity.ClientID, "pairingId": identity.PairingID, "generation": identity.Generation}
			if w := directAuthorityRequest(t, m, http.MethodPost, "/v2/direct/ack", origin.Host, ack, identity); w.Code != http.StatusOK {
				t.Fatalf("equivalent phone authority rejected: %d %s", w.Code, w.Body.String())
			}
			if err := m.Configure(context.Background(), DirectConfiguration{Endpoint: tc.endpoint, DisplayName: "Client"}); err != nil || m.state.Generation != identity.Generation {
				t.Fatal("equivalent reconfiguration changed the endpoint generation", err)
			}
		})
	}
}

func TestDirectPersistedNoncanonicalOriginsRecoverAndAcknowledge(t *testing.T) {
	for _, tc := range directAuthorityCases {
		for _, update := range []bool{false, true} {
			name := tc.name + "/pairing"
			if update {
				name = tc.name + "/endpoint update"
			}
			t.Run(name, func(t *testing.T) {
				m, invitation, csr := directTestConfigured(t)
				identity := directTestEnroll(t, m, invitation, csr)
				if update {
					directTestAck(t, m, identity)
				}
				// Recreate an origin accepted and saved by the earlier implementation.
				next := m.cloneLocked()
				next.Configuration.Endpoint = tc.endpoint
				if update {
					next.Generation++
				} else {
					next.Invitation.Endpoint = tc.endpoint
				}
				if err := directServerCertificate(next, tc.endpoint); err != nil {
					t.Fatal(err)
				}
				if err := m.saveLocked(context.Background(), next); err != nil {
					t.Fatal(err)
				}
				restarted := NewDirect(m.repository, "windows", "amd64", "2.0.0")
				bundle, err := restarted.ExportEndpointUpdate(context.Background())
				if err != nil {
					t.Fatal("stored equivalent origin became unreadable:", err)
				}
				if got := directEndpointFromUpdate(t, bundle); got != tc.endpoint {
					t.Errorf("changed the signed endpoint at an existing generation: got %q; want %q", got, tc.endpoint)
				}
				if restarted.state.Generation != next.Generation || restarted.state.Pairing.ID != next.Pairing.ID || restarted.state.CACertificatePEM != next.CACertificatePEM || restarted.state.Invitation.Capability != next.Invitation.Capability {
					t.Fatal("normalization changed generation, pairing, trust or invitation")
				}
				ack := map[string]any{"clientId": identity.ClientID, "pairingId": identity.PairingID, "generation": next.Generation}
				for _, wrong := range []string{"other.example:8443", "[2001:db8::2]:8443", strings.ReplaceAll(tc.authority, ":8443", ":9443")} {
					if wrong == tc.authority {
						wrong += ":9443"
					}
					if w := directAuthorityRequest(t, restarted, http.MethodPost, "/v2/direct/ack", wrong, ack, identity); w.Code != http.StatusConflict {
						t.Fatalf("wrong authority %q admitted: %d", wrong, w.Code)
					}
				}
				if w := directAuthorityRequest(t, restarted, http.MethodPost, "/v2/direct/ack", tc.authority, ack, identity); w.Code != http.StatusOK {
					t.Fatalf("equivalent authority rejected: %d %s", w.Code, w.Body.String())
				}
				if restarted.state.AcknowledgedEndpoint != tc.canonical || restarted.Status().UpdatePending || !restarted.Status().Paired {
					t.Fatal("ACK did not durably complete pairing/update with the canonical origin")
				}
				if err := restarted.Configure(context.Background(), *next.Configuration); err != nil {
					t.Fatal(err)
				}
				bundle, err = restarted.ExportEndpointUpdate(context.Background())
				if err != nil || restarted.state.Generation != next.Generation+1 || directEndpointFromUpdate(t, bundle) != tc.canonical {
					t.Fatal("reconfiguration did not issue a canonical endpoint at a new generation", err)
				}
			})
		}
	}
}

type directReadUnavailableStore struct{ securestore.Store }

func (s directReadUnavailableStore) Get(context.Context, string) ([]byte, error) {
	return nil, errors.New("protected storage temporarily unavailable")
}

func TestDirectProtectedStateFailureDoesNotClaimAuthenticationRejection(t *testing.T) {
	m, invitation, csr := directTestConfigured(t)
	identity := directTestEnroll(t, m, invitation, csr)
	directTestAck(t, m, identity)
	unavailable := NewDirect(NewRepository(directReadUnavailableStore{m.repository.store}), "windows", "amd64", "2.0.0")
	denied := NewDirect(m.repository, "windows", "amd64", "2.0.0")
	denied.denied.Store(true)
	revoked := NewDirect(m.repository, "windows", "amd64", "2.0.0")
	if err := denied.ensureLocked(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := revoked.Revoke(context.Background()); err != nil {
		t.Fatal(err)
	}
	if w := directAuthorityRequest(t, denied, http.MethodPost, "/v2/direct/enroll", "client.example:8443", directEnrollBody(invitation, csr), identity); w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "storage_unavailable") {
		t.Errorf("failed revocation claimed the invitation was definitively rejected: %d %s", w.Code, w.Body.String())
	}
	for _, route := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPost, "/v2/direct/ack", map[string]any{"clientId": identity.ClientID, "pairingId": identity.PairingID, "generation": identity.Generation}},
		{http.MethodGet, "/v2/direct/config", nil},
		{http.MethodPost, "/v2/direct/renew", map[string]any{"clientId": identity.ClientID, "pairingId": identity.PairingID, "csrPem": csr}},
		{http.MethodGet, "/v2/direct/session?transport=2", nil},
	} {
		t.Run(route.path, func(t *testing.T) {
			for _, failure := range []struct {
				manager *Direct
				status  int
				code    string
			}{
				{unavailable, http.StatusServiceUnavailable, "storage_unavailable"},
				{denied, http.StatusServiceUnavailable, "storage_unavailable"},
				{revoked, http.StatusUnauthorized, "unauthorized"},
			} {
				w := directAuthorityRequest(t, failure.manager, route.method, route.path, "client.example:8443", route.body, identity)
				var response map[string]string
				if w.Code != failure.status || json.Unmarshal(w.Body.Bytes(), &response) != nil || response["error"] != failure.code {
					t.Errorf("admission response = %d %s; want %d %s", w.Code, w.Body.String(), failure.status, failure.code)
				}
			}
		})
	}
}
