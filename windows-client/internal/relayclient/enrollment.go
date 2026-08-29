// Package relayclient implements the mobile-egress relay enrollment, control,
// and v1 tunnel protocols for the Windows client.
package relayclient

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const maxControlResponseBytes = 256 << 10

type Identity struct {
	RelayURL         string `json:"relayUrl"`
	Role             string `json:"role"`
	Serial           string `json:"serial"`
	PrivateKeyPEM    string `json:"privateKeyPem"`
	CertificatePEM   string `json:"certificatePem"`
	CACertificatePEM string `json:"caCertificatePem"`
}

type enrollResponse struct {
	CertificatePEM   string `json:"certificatePem"`
	CACertificatePEM string `json:"caCertificatePem"`
	Serial           string `json:"serial"`
	Role             string `json:"role"`
}

func Enroll(ctx context.Context, relayURL, capability, role string) (Identity, error) {
	baseURL, err := validateRelayURL(relayURL)
	if err != nil {
		return Identity{}, err
	}
	if strings.TrimSpace(capability) == "" {
		return Identity{}, errors.New("pairing capability is required")
	}
	if role != "owner" && role != "client" {
		return Identity{}, errors.New("Windows enrollment role must be owner or client")
	}

	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return Identity{}, fmt.Errorf("generate enrollment key: %w", err)
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: "mobile-egress-windows"},
	}, privateKey)
	if err != nil {
		return Identity{}, fmt.Errorf("create enrollment request: %w", err)
	}
	requestBody, err := json.Marshal(map[string]string{
		"code": capability, "role": role,
		"csrPem": string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})),
	})
	if err != nil {
		return Identity{}, err
	}

	var peerMu sync.Mutex
	var peerCertificates []*x509.Certificate
	transport := &http.Transport{TLSClientConfig: &tls.Config{
		MinVersion: tls.VersionTLS13,
		// Enrollment bootstraps trust from the returned relay CA. The observed
		// server certificate is validated against that CA before state is saved.
		InsecureSkipVerify: true, //nolint:gosec
		VerifyConnection: func(state tls.ConnectionState) error {
			peerMu.Lock()
			peerCertificates = append([]*x509.Certificate(nil), state.PeerCertificates...)
			peerMu.Unlock()
			if len(state.PeerCertificates) == 0 {
				return errors.New("relay did not present a server certificate")
			}
			return nil
		},
	}}
	defer transport.CloseIdleConnections()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL.String()+"/v1/enroll", bytes.NewReader(requestBody))
	if err != nil {
		return Identity{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Transport: transport, Timeout: 30 * time.Second}).Do(request)
	if err != nil {
		return Identity{}, fmt.Errorf("enrollment request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxControlResponseBytes))
		return Identity{}, fmt.Errorf("enrollment rejected with HTTP %d", response.StatusCode)
	}
	var result enrollResponse
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxControlResponseBytes+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return Identity{}, errors.New("relay returned an invalid enrollment response")
	}
	if err := requireJSONEOF(decoder); err != nil {
		return Identity{}, errors.New("relay returned an invalid enrollment response")
	}

	peerMu.Lock()
	observedPeer := append([]*x509.Certificate(nil), peerCertificates...)
	peerMu.Unlock()
	if err := validateEnrollmentResult(baseURL, role, privateKey, result, observedPeer); err != nil {
		return Identity{}, err
	}
	privateKeyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return Identity{}, err
	}
	return Identity{
		RelayURL: baseURL.String(), Role: result.Role, Serial: result.Serial,
		PrivateKeyPEM:  string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKeyDER})),
		CertificatePEM: result.CertificatePEM, CACertificatePEM: result.CACertificatePEM,
	}, nil
}

func validateEnrollmentResult(baseURL *url.URL, requestedRole string, privateKey *ecdsa.PrivateKey, result enrollResponse, observedPeer []*x509.Certificate) error {
	if result.Role != requestedRole || result.Serial == "" {
		return errors.New("relay returned an identity with the wrong role or serial")
	}
	ca, err := parseSingleCertificate(result.CACertificatePEM)
	if err != nil || !ca.IsCA || !ca.BasicConstraintsValid || ca.KeyUsage&x509.KeyUsageCertSign == 0 {
		return errors.New("relay returned an invalid CA certificate")
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	if len(observedPeer) == 0 {
		return errors.New("relay server certificate was not observed")
	}
	serverIntermediates := x509.NewCertPool()
	for _, certificate := range observedPeer[1:] {
		serverIntermediates.AddCert(certificate)
	}
	if _, err := observedPeer[0].Verify(x509.VerifyOptions{
		Roots: roots, Intermediates: serverIntermediates, DNSName: baseURL.Hostname(),
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err != nil {
		return errors.New("relay server certificate does not verify against returned CA")
	}

	chain, err := parseCertificateChain(result.CertificatePEM)
	if err != nil || len(chain) == 0 {
		return errors.New("relay returned an invalid client certificate chain")
	}
	intermediates := x509.NewCertPool()
	for _, certificate := range chain[1:] {
		if !certificate.Equal(ca) {
			intermediates.AddCert(certificate)
		}
	}
	if _, err := chain[0].Verify(x509.VerifyOptions{
		Roots: roots, Intermediates: intermediates,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}); err != nil {
		return errors.New("client certificate does not verify against returned CA")
	}
	publicKey, ok := chain[0].PublicKey.(*ecdsa.PublicKey)
	if !ok || !publicKey.Equal(&privateKey.PublicKey) {
		return errors.New("client certificate does not match generated private key")
	}
	wantSerial := strings.ToUpper(chain[0].SerialNumber.Text(16))
	if strings.ToUpper(result.Serial) != wantSerial {
		return errors.New("client certificate serial does not match enrollment response")
	}
	return nil
}

func validateRelayURL(raw string) (*url.URL, error) {
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

func parseSingleCertificate(value string) (*x509.Certificate, error) {
	block, rest := pem.Decode([]byte(value))
	if block == nil || block.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, errors.New("invalid certificate PEM")
	}
	return x509.ParseCertificate(block.Bytes)
}

func parseCertificateChain(value string) ([]*x509.Certificate, error) {
	rest := []byte(value)
	certificates := make([]*x509.Certificate, 0, 2)
	for len(bytes.TrimSpace(rest)) > 0 {
		block, remaining := pem.Decode(rest)
		if block == nil || block.Type != "CERTIFICATE" {
			return nil, errors.New("invalid certificate chain PEM")
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, err
		}
		certificates = append(certificates, certificate)
		rest = remaining
	}
	return certificates, nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}
