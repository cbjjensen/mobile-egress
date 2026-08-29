package relayclient

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
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
	transport := &http.Transport{TLSClientConfig: tlsConfig}
	return &http.Client{Transport: transport}, transport, nil
}
