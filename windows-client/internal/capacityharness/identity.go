//go:build capacityharness

package capacityharness

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"strings"

	"mobile-egress/windows-client/internal/relayclient"
)

type OwnerLoader interface {
	LoadOwner(context.Context) (relayclient.Identity, error)
}

type Control interface {
	Health(context.Context, relayclient.Identity) (relayclient.RelayHealth, error)
	ProvisionClient(context.Context, relayclient.Identity, string) (relayclient.ProvisionedIdentity, error)
	Revoke(context.Context, relayclient.Identity, string) error
}

type ProductionControl struct{}

func (ProductionControl) Health(ctx context.Context, owner relayclient.Identity) (relayclient.RelayHealth, error) {
	return relayclient.Health(ctx, owner)
}

func (ProductionControl) ProvisionClient(ctx context.Context, owner relayclient.Identity, csr string) (relayclient.ProvisionedIdentity, error) {
	return relayclient.ProvisionClient(ctx, owner, csr)
}

func (ProductionControl) Revoke(ctx context.Context, owner relayclient.Identity, serial string) error {
	return relayclient.Revoke(ctx, owner, serial)
}

type ClientCredential struct {
	RelayURL         string
	DialAddress      string
	Role             string
	Serial           string
	PrivateKeyPEM    []byte
	CertificatePEM   string
	CACertificatePEM string
}

func provisionClientCredential(ctx context.Context, control Control, owner relayclient.Identity) (*ClientCredential, error) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, errors.New("generate capacity Client key")
	}
	requestDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: "mobile-egress-capacity-harness"},
	}, privateKey)
	if err != nil {
		return nil, errors.New("generate capacity Client request")
	}
	requestPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: requestDER})
	privateKeyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		clear(requestDER)
		clear(requestPEM)
		return nil, errors.New("encode capacity Client key")
	}
	privateKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKeyDER})
	clear(privateKeyDER)
	issued, err := control.ProvisionClient(ctx, owner, string(requestPEM))
	clear(requestDER)
	clear(requestPEM)
	if err != nil {
		clear(privateKeyPEM)
		return nil, err
	}
	// ProductionControl delegates to relayclient.ProvisionClient, which has
	// already verified the issued role, certificate/key binding, serial, CA,
	// and relay origin. Do not add a harness-only failure after issuance: that
	// could discard the only serial needed for cleanup.
	return &ClientCredential{
		RelayURL: issued.RelayURL, DialAddress: owner.DialAddress, Role: issued.Role,
		Serial: strings.ToUpper(issued.Serial), PrivateKeyPEM: privateKeyPEM,
		CertificatePEM: issued.CertificatePEM, CACertificatePEM: issued.CACertificatePEM,
	}, nil
}

func validIdentitySerial(serial string) bool {
	if serial == "" || serial != strings.TrimSpace(serial) || len(serial) > 64 {
		return false
	}
	for _, character := range serial {
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f') || (character >= 'A' && character <= 'F')) {
			return false
		}
	}
	return true
}

func (credential *ClientCredential) Zero() {
	if credential == nil {
		return
	}
	clear(credential.PrivateKeyPEM)
	credential.PrivateKeyPEM = nil
	credential.RelayURL = ""
	credential.DialAddress = ""
	credential.Role = ""
	credential.Serial = ""
	credential.CertificatePEM = ""
	credential.CACertificatePEM = ""
}
