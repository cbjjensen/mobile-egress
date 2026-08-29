package pairing

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"
)

func TestBundleRoundTripCarriesPinnedRelayTrust(t *testing.T) {
	t.Parallel()

	_, _, caPEM := testCA(t)
	want := Bundle{
		Version: 1, RelayURL: "https://relay.example:8443",
		CACertificatePEM: string(caPEM), Capability: strings.Repeat("a", 43),
		Role: "owner", ExpiresAt: time.Date(2026, 8, 29, 19, 0, 0, 0, time.UTC),
	}
	encoded, err := Encode(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got.RelayURL != want.RelayURL || got.CACertificatePEM != want.CACertificatePEM || got.Capability != want.Capability || got.Role != want.Role || !got.ExpiresAt.Equal(want.ExpiresAt) {
		t.Fatalf("decoded bundle = %#v, want %#v", got, want)
	}
}

func testCA(t *testing.T) (*x509.Certificate, *ecdsa.PrivateKey, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "pairing-test-ca"},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return certificate, key, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func TestBundleRejectsMissingOrInvalidPinnedTrust(t *testing.T) {
	t.Parallel()

	tests := []Bundle{
		{Version: 1, RelayURL: "https://relay.example:8443", Capability: strings.Repeat("a", 43), Role: "client"},
		{Version: 1, RelayURL: "http://relay.example:8443", CACertificatePEM: "not pem", Capability: strings.Repeat("a", 43), Role: "client"},
	}
	for _, bundle := range tests {
		if _, err := Encode(bundle); err == nil {
			t.Fatalf("Encode(%#v) accepted an insecure bundle", bundle)
		}
	}
}
