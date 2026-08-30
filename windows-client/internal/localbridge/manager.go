// Package localbridge coordinates Tailscale Funnel, elevated relay service
// setup, and local-only Owner private-key creation.
package localbridge

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"strings"

	"mobile-egress/pairing"
	"mobile-egress/windows-client/internal/relayclient"
	"mobile-egress/windows-client/internal/tailscale"
)

type SetupRequest struct {
	PublicName  string `json:"publicName"`
	PublicURL   string `json:"publicUrl"`
	OwnerCSRPEM string `json:"ownerCsrPem"`
}

type OwnerBootstrapResult struct {
	CertificatePEM   string `json:"certificatePem"`
	CACertificatePEM string `json:"caCertificatePem"`
	Serial           string `json:"serial"`
	Role             string `json:"role"`
}

type RotateRequest struct {
	PublicName string `json:"publicName"`
	PublicURL  string `json:"publicUrl"`
}

type EndpointRotationResult struct {
	PublicURL string `json:"publicUrl"`
	Serial    string `json:"serial"`
}

type BridgeStatus struct {
	Ready       bool   `json:"ready"`
	PublicURL   string `json:"publicUrl"`
	FQDN        string `json:"fqdn"`
	OwnerSerial string `json:"ownerSerial"`
}

type TailscaleBridge interface {
	Enable(context.Context) (tailscale.Status, error)
}

type ElevatedHelper interface {
	Setup(context.Context, SetupRequest) (OwnerBootstrapResult, error)
	Rotate(context.Context, RotateRequest) (EndpointRotationResult, error)
	Repair(context.Context) error
}

func (manager *Manager) Repair(ctx context.Context) error {
	if manager == nil || manager.helper == nil {
		return errors.New("local bridge repair dependency is required")
	}
	if err := manager.helper.Repair(ctx); err != nil {
		return errors.New("elevated local relay repair failed or was cancelled")
	}
	return nil
}

func (manager *Manager) Rotate(ctx context.Context, identity relayclient.Identity) (BridgeStatus, relayclient.Identity, error) {
	if manager == nil || manager.tailscale == nil || manager.helper == nil || manager.owners == nil {
		return BridgeStatus{}, relayclient.Identity{}, errors.New("local bridge rotation dependencies are required")
	}
	if identity.Role != "owner" || identity.PrivateKeyPEM == "" || identity.CertificatePEM == "" || identity.CACertificatePEM == "" {
		return BridgeStatus{}, relayclient.Identity{}, errors.New("existing local Owner identity is incomplete")
	}
	status, err := manager.tailscale.Enable(ctx)
	if err != nil || !status.Online || status.FQDN == "" || status.PublicURL == "" {
		return BridgeStatus{}, relayclient.Identity{}, errors.New("Tailscale login or Funnel setup failed")
	}
	result, err := manager.helper.Rotate(ctx, RotateRequest{PublicName: status.FQDN, PublicURL: status.PublicURL})
	if err != nil || result.PublicURL != status.PublicURL || result.Serial == "" {
		return BridgeStatus{}, relayclient.Identity{}, errors.New("elevated relay endpoint rotation failed or was cancelled")
	}
	identity.RelayURL = status.PublicURL
	identity.DialAddress = "127.0.0.1:8443"
	if err := manager.owners.SaveOwnerIdentity(ctx, identity); err != nil {
		return BridgeStatus{}, relayclient.Identity{}, errors.New("save rotated local Owner endpoint")
	}
	return BridgeStatus{
		Ready: true, PublicURL: status.PublicURL, FQDN: status.FQDN, OwnerSerial: identity.Serial,
	}, identity, nil
}

type OwnerSink interface {
	SaveOwnerIdentity(context.Context, relayclient.Identity) error
	UpdateOwnerIdentity(context.Context, relayclient.Identity) error
}

type Manager struct {
	tailscale TailscaleBridge
	helper    ElevatedHelper
	owners    OwnerSink
}

func NewManager(tailscaleBridge TailscaleBridge, helper ElevatedHelper, owners OwnerSink) *Manager {
	return &Manager{tailscale: tailscaleBridge, helper: helper, owners: owners}
}

func (manager *Manager) Setup(ctx context.Context) (BridgeStatus, error) {
	if manager == nil || manager.tailscale == nil || manager.helper == nil || manager.owners == nil {
		return BridgeStatus{}, errors.New("local bridge setup dependencies are required")
	}
	tailscaleStatus, err := manager.tailscale.Enable(ctx)
	if err != nil {
		return BridgeStatus{}, errors.New("Tailscale login or Funnel setup failed")
	}
	if !tailscaleStatus.Online || tailscaleStatus.FQDN == "" || tailscaleStatus.PublicURL == "" {
		return BridgeStatus{}, errors.New("Tailscale did not return an online Funnel endpoint")
	}
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return BridgeStatus{}, errors.New("generate local Owner key")
	}
	requestDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: "Mobile Egress Local Owner"},
	}, privateKey)
	if err != nil {
		return BridgeStatus{}, errors.New("create local Owner certificate request")
	}
	requestPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: requestDER})
	result, err := manager.helper.Setup(ctx, SetupRequest{
		PublicName: tailscaleStatus.FQDN, PublicURL: tailscaleStatus.PublicURL, OwnerCSRPEM: string(requestPEM),
	})
	clear(requestDER)
	clear(requestPEM)
	if err != nil {
		return BridgeStatus{}, errors.New("elevated local relay setup failed or was cancelled")
	}
	if err := validateOwnerBootstrap(privateKey.Public(), result); err != nil {
		return BridgeStatus{}, err
	}
	privateKeyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return BridgeStatus{}, errors.New("encode local Owner key")
	}
	defer clear(privateKeyDER)
	identity := relayclient.Identity{
		RelayURL: tailscaleStatus.PublicURL, DialAddress: "127.0.0.1:8443", Role: "owner", Serial: strings.ToUpper(result.Serial),
		PrivateKeyPEM:  string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKeyDER})),
		CertificatePEM: result.CertificatePEM, CACertificatePEM: result.CACertificatePEM,
	}
	if err := manager.owners.UpdateOwnerIdentity(ctx, identity); err != nil {
		return BridgeStatus{}, errors.New("save encrypted local Owner identity")
	}
	return BridgeStatus{
		Ready: true, PublicURL: tailscaleStatus.PublicURL, FQDN: tailscaleStatus.FQDN, OwnerSerial: identity.Serial,
	}, nil
}

func validateOwnerBootstrap(expectedPublicKey crypto.PublicKey, result OwnerBootstrapResult) error {
	if result.Role != "owner" || result.Serial == "" || result.CertificatePEM == "" || result.CACertificatePEM == "" {
		return errors.New("local relay returned an incomplete Owner identity")
	}
	ca, err := pairing.CACertificate(result.CACertificatePEM)
	if err != nil {
		return errors.New("local relay returned an invalid CA")
	}
	block, _ := pem.Decode([]byte(result.CertificatePEM))
	if block == nil || block.Type != "CERTIFICATE" {
		return errors.New("local relay returned an invalid Owner certificate")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil || strings.ToUpper(certificate.SerialNumber.Text(16)) != strings.ToUpper(result.Serial) {
		return errors.New("local relay returned an invalid Owner certificate")
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	if _, err := certificate.Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		return errors.New("local Owner certificate does not verify under the relay CA")
	}
	actualDER, err := x509.MarshalPKIXPublicKey(certificate.PublicKey)
	if err != nil {
		return errors.New("local Owner certificate has an invalid public key")
	}
	expectedDER, err := x509.MarshalPKIXPublicKey(expectedPublicKey)
	if err != nil || !bytes.Equal(actualDER, expectedDER) {
		return errors.New("local Owner certificate does not match the generated key")
	}
	return nil
}
