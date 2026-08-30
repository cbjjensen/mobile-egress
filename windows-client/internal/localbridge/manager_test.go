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
	"math/big"
	"testing"
	"time"

	"mobile-egress/windows-client/internal/relayclient"
	"mobile-egress/windows-client/internal/tailscale"
)

func TestSetupGeneratesOwnerLocallyAndUsesDirectCSRBootstrap(t *testing.T) {
	t.Parallel()

	bridge := &fakeTailscaleBridge{status: tailscale.Status{
		Online: true, FunnelReady: true, FQDN: "bridge.tail123.ts.net", PublicURL: "https://bridge.tail123.ts.net:8443",
	}}
	helper := &fakeElevatedHelper{t: t}
	sink := &fakeOwnerSink{}
	manager := NewManager(bridge, helper, sink)
	status, err := manager.Setup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.PublicURL != bridge.status.PublicURL || status.OwnerSerial == "" || !status.Ready {
		t.Fatalf("Setup() status = %#v", status)
	}
	if helper.request.PublicName != bridge.status.FQDN || helper.request.PublicURL != bridge.status.PublicURL || helper.request.OwnerCSRPEM == "" {
		t.Fatalf("elevated helper request = %#v", helper.request)
	}
	if stringsContainsPrivateMaterial(helper.request.OwnerCSRPEM) {
		t.Fatal("elevated helper request contained private material")
	}
	if sink.identity.Role != "owner" || sink.identity.RelayURL != bridge.status.PublicURL || sink.identity.DialAddress != "127.0.0.1:8443" || sink.identity.PrivateKeyPEM == "" {
		t.Fatalf("stored Owner identity = %#v", sink.identity)
	}
	if sink.saveCalls != 1 || sink.updateCalls != 0 {
		t.Fatalf("Owner sink calls = save %d/update %d, want initial SaveOwnerIdentity only", sink.saveCalls, sink.updateCalls)
	}
	assertIdentityCertificateMatchesPrivateKey(t, sink.identity)
}

func TestRotateEndpointRetainsOwnerKeysAndUsesNewFunnelName(t *testing.T) {
	t.Parallel()

	bridge := &fakeTailscaleBridge{status: tailscale.Status{
		Online: true, FunnelReady: true, FQDN: "new.tail123.ts.net", PublicURL: "https://new.tail123.ts.net:8443",
	}}
	helper := &fakeElevatedHelper{t: t}
	sink := &fakeOwnerSink{}
	manager := NewManager(bridge, helper, sink)
	before := relayclient.Identity{
		RelayURL: "https://old.tail123.ts.net:8443", DialAddress: "127.0.0.1:8443", Role: "owner", Serial: "2A",
		PrivateKeyPEM: "private", CertificatePEM: "certificate", CACertificatePEM: "ca",
	}
	status, after, err := manager.Rotate(context.Background(), before)
	if err != nil {
		t.Fatal(err)
	}
	if helper.rotation.PublicName != bridge.status.FQDN || helper.rotation.PublicURL != bridge.status.PublicURL {
		t.Fatalf("rotation request = %#v", helper.rotation)
	}
	want := before
	want.RelayURL = bridge.status.PublicURL
	if after != want || sink.identity != want || !status.Ready {
		t.Fatalf("rotated identity/status = %#v / %#v", after, status)
	}
	if sink.saveCalls != 0 || sink.updateCalls != 1 {
		t.Fatalf("Owner sink calls = save %d/update %d, want endpoint UpdateOwnerIdentity only", sink.saveCalls, sink.updateCalls)
	}
}

func TestRepairReinstallsTheSignedRelayWithoutChangingIdentity(t *testing.T) {
	t.Parallel()

	helper := &fakeElevatedHelper{t: t}
	bridge := &fakeTailscaleBridge{status: tailscale.Status{Online: true, FunnelReady: true}}
	manager := NewManager(bridge, helper, &fakeOwnerSink{})
	if err := manager.Repair(context.Background()); err != nil {
		t.Fatal(err)
	}
	if helper.repairs != 1 {
		t.Fatalf("repair calls = %d, want 1", helper.repairs)
	}
	if bridge.enableCalls != 1 {
		t.Fatalf("Tailscale Funnel repair calls = %d, want 1", bridge.enableCalls)
	}
}

type fakeTailscaleBridge struct {
	status      tailscale.Status
	enableCalls int
}

func (bridge *fakeTailscaleBridge) Enable(context.Context) (tailscale.Status, error) {
	bridge.enableCalls++
	return bridge.status, nil
}

type fakeOwnerSink struct {
	identity    relayclient.Identity
	saveCalls   int
	updateCalls int
}

func (sink *fakeOwnerSink) SaveOwnerIdentity(_ context.Context, identity relayclient.Identity) error {
	sink.saveCalls++
	sink.identity = identity
	return nil
}

func (sink *fakeOwnerSink) UpdateOwnerIdentity(_ context.Context, identity relayclient.Identity) error {
	sink.updateCalls++
	sink.identity = identity
	return nil
}

type fakeElevatedHelper struct {
	t        *testing.T
	request  SetupRequest
	rotation RotateRequest
	repairs  int
}

func (helper *fakeElevatedHelper) Repair(context.Context) error {
	helper.repairs++
	return nil
}

func (helper *fakeElevatedHelper) Setup(_ context.Context, request SetupRequest) (OwnerBootstrapResult, error) {
	helper.request = request
	block, _ := pem.Decode([]byte(request.OwnerCSRPEM))
	if block == nil {
		helper.t.Fatal("invalid Owner CSR")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil || csr.CheckSignature() != nil {
		helper.t.Fatal("invalid Owner CSR")
	}
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	now := time.Now()
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "bridge CA"}, IsCA: true, BasicConstraintsValid: true,
		NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caDER, _ := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	ca, _ := x509.ParseCertificate(caDER)
	ownerTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(42), Subject: pkix.Name{CommonName: "owner"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	ownerDER, _ := x509.CreateCertificate(rand.Reader, ownerTemplate, ca, csr.PublicKey, caKey)
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	certificatePEM := append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ownerDER}), caPEM...)
	return OwnerBootstrapResult{
		CertificatePEM: string(certificatePEM), CACertificatePEM: string(caPEM), Serial: "2A", Role: "owner",
	}, nil
}

func (helper *fakeElevatedHelper) Rotate(_ context.Context, request RotateRequest) (EndpointRotationResult, error) {
	helper.rotation = request
	return EndpointRotationResult{PublicURL: request.PublicURL, Serial: "99"}, nil
}

func assertIdentityCertificateMatchesPrivateKey(t *testing.T, identity relayclient.Identity) {
	t.Helper()
	keyBlock, _ := pem.Decode([]byte(identity.PrivateKeyPEM))
	certificateBlock, _ := pem.Decode([]byte(identity.CertificatePEM))
	if keyBlock == nil || certificateBlock == nil {
		t.Fatal("identity PEM is incomplete")
	}
	key, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(certificateBlock.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	left, _ := x509.MarshalPKIXPublicKey(key.(crypto.Signer).Public())
	right, _ := x509.MarshalPKIXPublicKey(certificate.PublicKey)
	if !bytes.Equal(left, right) {
		t.Fatal("Owner certificate does not match generated private key")
	}
}

func stringsContainsPrivateMaterial(value string) bool {
	return bytes.Contains([]byte(value), []byte("PRIVATE KEY"))
}
