package relayclient

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"time"
)

func (identity Identity) tlsConfig() (*tls.Config, error) {
	if identity.Role != "client" && identity.Role != "owner" {
		return nil, errors.New("invalid Windows identity role")
	}
	certificate, err := tls.X509KeyPair([]byte(identity.CertificatePEM), []byte(identity.PrivateKeyPEM))
	if err != nil {
		return nil, errors.New("stored client certificate or private key is invalid")
	}
	ca, err := parseSingleCertificate(identity.CACertificatePEM)
	if err != nil || !ca.IsCA {
		return nil, errors.New("stored relay CA is invalid")
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	return &tls.Config{
		Certificates: []tls.Certificate{certificate}, RootCAs: roots,
		MinVersion: tls.VersionTLS13,
	}, nil
}

func identityHTTPClient(identity Identity) (*http.Client, *http.Transport, error) {
	tlsConfig, err := identity.tlsConfig()
	if err != nil {
		return nil, nil, err
	}
	baseURL, err := validateRelayURL(identity.RelayURL)
	if err != nil {
		return nil, nil, err
	}
	tlsConfig.ServerName = baseURL.Hostname()
	transport := &http.Transport{TLSClientConfig: tlsConfig}
	if identity.DialAddress != "" {
		host, port, err := net.SplitHostPort(identity.DialAddress)
		if err != nil || host != "127.0.0.1" || port != "8443" {
			return nil, nil, errors.New("stored relay dial override is invalid")
		}
		dialer := &net.Dialer{Timeout: 10 * time.Second}
		transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "tcp4", identity.DialAddress)
		}
	}
	return &http.Client{Transport: transport}, transport, nil
}
