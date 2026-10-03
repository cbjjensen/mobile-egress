package relayclient

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"mobile-egress/internal/clientcontrol"
)

func TestStandaloneTunnelReportsRevokedIdentity(t *testing.T) {
	identity, server, _ := newControlFixture(t, "client")
	defer server.Close()
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) })
	if _, err := DialSession(context.Background(), identity); !errors.Is(err, ErrClientUnauthorized) {
		t.Fatalf("revoked tunnel error=%v", err)
	}
}

func TestStandaloneOwnerControlsRequireOwnerAndSanitizeHTTPError(t *testing.T) {
	if _, err := CreateClientInvitation(context.Background(), Identity{Role: "client"}, "paired-one", "PC"); err == nil {
		t.Fatal("client created invitation")
	}
	owner, server, _ := newControlFixture(t, "owner")
	defer server.Close()
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(409)
		_, _ = w.Write([]byte(`{"error":"client_limit","capability":"private-secret"}`))
	})
	_, err := CreateClientInvitation(context.Background(), owner, "paired-one", "PC")
	failure, ok := err.(*ClientControlError)
	if !ok || failure.StatusCode != 409 || failure.Code != "client_limit" || failure.Error() == "private-secret" {
		t.Fatalf("wrong sanitized control error: %v", err)
	}
}

func TestStandaloneApproveValidatesBoundCertificate(t *testing.T) {
	owner, server, _ := newControlFixture(t, "owner")
	defer server.Close()
	_, csr := testControlCSR(t)
	configurationKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	issued, err := ProvisionClient(context.Background(), owner, csr)
	if err != nil {
		t.Fatal(err)
	}
	value := clientcontrol.Enrollment{ID: "enrollment", NodeID: "paired-one", DisplayName: "PC", State: "approved", ExpiresAt: time.Now().Add(time.Minute), Bootstrap: &clientcontrol.Bootstrap{CSRPEM: csr, ConfigurationPublicKey: base64.RawURLEncoding.EncodeToString(configurationKey.PublicKey().Bytes()), Platform: "windows", Architecture: "amd64", ServiceVersion: "1.0.0"}, ClientSerial: issued.Serial, CertificatePEM: issued.CertificatePEM, CACertificatePEM: issued.CACertificatePEM}
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(value)
	})
	if _, err := ApproveClientEnrollment(context.Background(), owner, "enrollment"); err != nil {
		t.Fatalf("valid approval failed: %v", err)
	}
	_, otherCSR := testControlCSR(t)
	value.Bootstrap.CSRPEM = otherCSR
	if _, err := ApproveClientEnrollment(context.Background(), owner, "enrollment"); err == nil {
		t.Fatal("approved mismatched certificate and bootstrap")
	}
}
