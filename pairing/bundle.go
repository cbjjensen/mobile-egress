// Package pairing defines the portable, owner-delivered trust bootstrap used
// by relay and device clients.
package pairing

import (
	"bytes"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/url"
	"strings"
	"time"
)

const (
	Version         = 1
	maxEncodedBytes = 512 << 10
)

type Bundle struct {
	Version          int       `json:"version"`
	RelayURL         string    `json:"relayUrl"`
	CACertificatePEM string    `json:"caCertificatePem"`
	Capability       string    `json:"capability"`
	Role             string    `json:"role"`
	ExpiresAt        time.Time `json:"expiresAt,omitempty"`
}

func Encode(bundle Bundle) (string, error) {
	if err := bundle.Validate(); err != nil {
		return "", err
	}
	raw, err := json.Marshal(bundle)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func Decode(value string) (Bundle, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maxEncodedBytes {
		return Bundle{}, errors.New("pairing bundle is missing or too large")
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return Bundle{}, errors.New("pairing bundle is not valid base64url")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var bundle Bundle
	if err := decoder.Decode(&bundle); err != nil {
		return Bundle{}, errors.New("pairing bundle is not valid JSON")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return Bundle{}, errors.New("pairing bundle has trailing JSON")
	}
	if err := bundle.Validate(); err != nil {
		return Bundle{}, err
	}
	return bundle, nil
}

func (bundle Bundle) Validate() error {
	if bundle.Version != Version {
		return errors.New("unsupported pairing bundle version")
	}
	if _, err := RelayOrigin(bundle.RelayURL); err != nil {
		return err
	}
	if strings.TrimSpace(bundle.Capability) == "" {
		return errors.New("pairing capability is required")
	}
	if bundle.Role != "owner" && bundle.Role != "client" && bundle.Role != "agent" {
		return errors.New("pairing role is invalid")
	}
	if _, err := CACertificate(bundle.CACertificatePEM); err != nil {
		return err
	}
	return nil
}

func RelayOrigin(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("relay URL must be an HTTPS origin")
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return nil, errors.New("relay URL must not contain a path")
	}
	parsed.Path = ""
	return parsed, nil
}

func CACertificate(value string) (*x509.Certificate, error) {
	block, rest := pem.Decode([]byte(value))
	if block == nil || block.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, errors.New("pairing bundle CA is not a single certificate PEM")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil || !certificate.IsCA || !certificate.BasicConstraintsValid || certificate.KeyUsage&x509.KeyUsageCertSign == 0 {
		return nil, errors.New("pairing bundle CA is not a valid certificate authority")
	}
	return certificate, nil
}
