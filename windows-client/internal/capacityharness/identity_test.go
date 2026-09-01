//go:build capacityharness

package capacityharness

import (
	"context"
	"strings"
	"testing"

	"mobile-egress/windows-client/internal/relayclient"
)

func TestProvisionCredentialAddsNoHarnessValidationAfterProductionIssuance(t *testing.T) {
	_, _, caPEM := newHarnessTestCA(t)
	owner := relayclient.Identity{
		RelayURL: "https://relay.example.com", Role: "owner", Serial: "AA",
		PrivateKeyPEM: "owner-key", CertificatePEM: "owner-cert", CACertificatePEM: string(caPEM),
	}
	control := staticProvisionControl{issued: relayclient.ProvisionedIdentity{
		RelayURL: "https://relay.example.com", Role: "client", Serial: "0A",
		CertificatePEM: "production-validated-client-chain",
		// x509/pem validation treats CRLF and LF PEM armor as equivalent. The
		// production ProvisionClient boundary has already validated the CA.
		CACertificatePEM: strings.ReplaceAll(string(caPEM), "\n", "\r\n"),
	}}
	credential, err := provisionClientCredential(context.Background(), control, owner)
	if err != nil {
		t.Fatalf("provisionClientCredential() added a post-issuance failure: %v", err)
	}
	defer credential.Zero()
	if credential.Serial != "0A" || credential.CACertificatePEM != control.issued.CACertificatePEM {
		t.Fatalf("credential did not retain the production-validated issue result")
	}
}

type staticProvisionControl struct {
	issued relayclient.ProvisionedIdentity
}

func (control staticProvisionControl) ProvisionClient(context.Context, relayclient.Identity, string) (relayclient.ProvisionedIdentity, error) {
	return control.issued, nil
}
func (staticProvisionControl) Health(context.Context, relayclient.Identity) (relayclient.RelayHealth, error) {
	return relayclient.RelayHealth{}, nil
}
func (staticProvisionControl) Revoke(context.Context, relayclient.Identity, string) error { return nil }
