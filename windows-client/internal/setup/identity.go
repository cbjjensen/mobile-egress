package setup

import (
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

var (
	embeddedCertificateBase64      string
	embeddedCertificateFingerprint string
)

type Identity struct {
	DER         []byte
	Thumbprint  string
	Fingerprint string
}

func EmbeddedIdentity() (Identity, error) {
	if embeddedCertificateBase64 == "" || len(embeddedCertificateBase64) > 32<<10 {
		return Identity{}, errors.New("embedded publisher certificate is unavailable")
	}
	der, err := base64.StdEncoding.DecodeString(embeddedCertificateBase64)
	if err != nil {
		return Identity{}, errors.New("embedded publisher certificate is invalid")
	}
	return LoadIdentity(der, embeddedCertificateFingerprint)
}

func LoadIdentity(der []byte, expectedFingerprint string) (Identity, error) {
	if len(der) == 0 || len(der) > 16<<10 {
		return Identity{}, errors.New("publisher certificate is invalid")
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil || !bytes.Equal(certificate.Raw, der) {
		return Identity{}, errors.New("publisher certificate is invalid")
	}
	sha256Sum := sha256.Sum256(der)
	fingerprint := colonFingerprint(sha256Sum[:])
	if expectedFingerprint != fingerprint {
		return Identity{}, fmt.Errorf("embedded publisher certificate fingerprint mismatch")
	}
	sha1Sum := sha1.Sum(der)
	return Identity{
		DER:         append([]byte(nil), der...),
		Thumbprint:  strings.ToUpper(hex.EncodeToString(sha1Sum[:])),
		Fingerprint: fingerprint,
	}, nil
}

func colonFingerprint(sum []byte) string {
	encoded := strings.ToUpper(hex.EncodeToString(sum))
	parts := make([]string, 0, len(encoded)/2)
	for index := 0; index < len(encoded); index += 2 {
		parts = append(parts, encoded[index:index+2])
	}
	return strings.Join(parts, ":")
}
