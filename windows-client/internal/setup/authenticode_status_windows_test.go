//go:build windows

package setup

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

// This tests the status/identity validator, not certificate parsing or Windows
// cryptography. Native trust is an independent input; no machine trust changes
// or publisher private/public files are needed for these boundary cases.
func TestPreTrustPowerShellUnknownErrorRequiresExactNativeUntrustedRoot(t *testing.T) {
	der := []byte("pinned verifier test identity")
	thumbprint := sha1.Sum(der)
	certificateHash := sha256.Sum256(der)
	identity := Identity{DER: der, Thumbprint: strings.ToUpper(hex.EncodeToString(thumbprint[:]))}
	result := authenticodeResult{
		Status: "UnknownError", Thumbprint: identity.Thumbprint,
		CertificateSHA256: hex.EncodeToString(certificateHash[:]),
		CertificateBase64: base64.StdEncoding.EncodeToString(der), Timestamped: true,
	}
	encode := func(value authenticodeResult) []byte {
		t.Helper()
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	raw := encode(result)
	if err := validatePreTrustAuthenticodeResult(raw, 0x800B0109, identity); err != nil {
		t.Fatalf("PowerShell 5.1 UnknownError with native CERT_E_UNTRUSTEDROOT rejected: %v", err)
	}
	for _, status := range []uint32{0, 0x80096010, 0x800B0100, 0x800B0004, 0x800B0101, 0x800B010A, 0x800B0111, 0x80092013} {
		if err := validatePreTrustAuthenticodeResult(raw, status, identity); err == nil {
			t.Errorf("UnknownError accepted with native status 0x%08X", status)
		}
	}
	for _, status := range []string{"HashMismatch", "NotSigned", "NotSupportedFileFormat", "Incompatible", "Valid", "", "unknownerror"} {
		changed := result
		changed.Status = status
		if err := validatePreTrustAuthenticodeResult(encode(changed), 0x800B0109, identity); err == nil {
			t.Errorf("native untrusted root accepted with incompatible PowerShell status %q", status)
		}
	}
	for _, mutate := range []func(*authenticodeResult){
		func(value *authenticodeResult) { value.Thumbprint = strings.Repeat("0", 40) },
		func(value *authenticodeResult) { value.CertificateSHA256 = strings.Repeat("0", 64) },
		func(value *authenticodeResult) {
			value.CertificateBase64 = base64.StdEncoding.EncodeToString([]byte("other signer"))
		},
		func(value *authenticodeResult) { value.Timestamped = false },
	} {
		changed := result
		mutate(&changed)
		if err := validatePreTrustAuthenticodeResult(encode(changed), 0x800B0109, identity); err == nil {
			t.Error("UnknownError bypassed exact timestamped signer requirements")
		}
	}
	if err := validateAuthenticodeResult(raw, identity); err == nil {
		t.Fatal("UnknownError accepted after trust installation")
	}
}
