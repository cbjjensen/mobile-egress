package service

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mobile-egress/pairing"
)

type RotateEndpointOptions struct {
	StateDir   string
	PublicName string
	PublicURL  string
}

type RotateEndpointResult struct {
	PublicURL string `json:"publicUrl"`
	Serial    string `json:"serial"`
}

func RotateEndpoint(ctx context.Context, options RotateEndpointOptions) (RotateEndpointResult, error) {
	origin, err := validateRelayOrigin(options.PublicURL, options.PublicName)
	if err != nil {
		return RotateEndpointResult{}, err
	}
	stateDir := filepath.Clean(strings.TrimSpace(options.StateDir))
	if stateDir == "." || options.StateDir == "" {
		return RotateEndpointResult{}, errors.New("state directory is required")
	}
	caCertPEM, err := os.ReadFile(filepath.Join(stateDir, caCertFilename))
	if err != nil {
		return RotateEndpointResult{}, fmt.Errorf("read relay CA certificate: %w", err)
	}
	caKeyPEM, err := os.ReadFile(filepath.Join(stateDir, caKeyFilename))
	if err != nil {
		return RotateEndpointResult{}, fmt.Errorf("read relay CA private key: %w", err)
	}
	caCert, caKey, err := parseCertificateAuthorityState(caCertPEM, caKeyPEM)
	if err != nil {
		return RotateEndpointResult{}, err
	}
	certificatePEM, privateKeyPEM, serial, err := generateRelayCertificateState(options.PublicName, caCert, caKey, time.Now().UTC())
	if err != nil {
		return RotateEndpointResult{}, err
	}
	database, err := openStore(filepath.Join(stateDir, databaseFilename))
	if err != nil {
		return RotateEndpointResult{}, err
	}
	defer database.Close()
	oldURL, err := database.relayURL(ctx)
	if err != nil {
		return RotateEndpointResult{}, err
	}
	oldOrigin, err := pairing.RelayOrigin(oldURL)
	if err != nil || oldURL != oldOrigin.String() || oldOrigin.Hostname() == "" {
		return RotateEndpointResult{}, ErrAdminStateIncompatible
	}
	oldCertificatePEM, err := readRegularAdminStateFile(filepath.Join(stateDir, relayCertFilename))
	if err != nil {
		return RotateEndpointResult{}, err
	}
	oldPrivateKeyPEM, err := readRegularAdminStateFile(filepath.Join(stateDir, relayKeyFilename))
	if err != nil {
		return RotateEndpointResult{}, err
	}
	if _, err := validateRelayEndpointIdentity(oldCertificatePEM, oldPrivateKeyPEM, caCert, oldOrigin.Hostname(), ""); err != nil {
		return RotateEndpointResult{}, err
	}
	requestBytes := make([]byte, 16)
	if _, err := rand.Read(requestBytes); err != nil {
		return RotateEndpointResult{}, fmt.Errorf("generate relay endpoint staging ID: %w", err)
	}
	requestID := hex.EncodeToString(requestBytes)
	stage := &adminEndpointStage{
		stateDir:          stateDir,
		requestID:         requestID,
		paths:             adminEndpointArtifactPaths(stateDir, requestID),
		caCertificate:     caCert,
		caCertificatePEM:  append([]byte(nil), caCertPEM...),
		caPrivateKeyPEM:   append([]byte(nil), caKeyPEM...),
		oldHostname:       oldOrigin.Hostname(),
		newHostname:       options.PublicName,
		newURL:            origin,
		serial:            serial,
		oldCertificatePEM: append([]byte(nil), oldCertificatePEM...),
		oldPrivateKeyPEM:  append([]byte(nil), oldPrivateKeyPEM...),
		newCertificatePEM: append([]byte(nil), certificatePEM...),
		newPrivateKeyPEM:  append([]byte(nil), privateKeyPEM...),
	}
	if err := stage.apply(ctx); err != nil {
		_ = stage.rollback()
		return RotateEndpointResult{}, err
	}
	if err := database.setRelayURL(ctx, origin); err != nil {
		rollbackErr := stage.rollback()
		return RotateEndpointResult{}, fmt.Errorf("persist rotated endpoint: %w", errors.Join(err, rollbackErr))
	}
	if err := stage.finalize(); err != nil {
		return RotateEndpointResult{}, err
	}
	return RotateEndpointResult{PublicURL: origin, Serial: serial}, nil
}

func generateRelayCertificateState(publicName string, caCert *x509.Certificate, caKey crypto.Signer, now time.Time) ([]byte, []byte, string, error) {
	relayKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, "", fmt.Errorf("generate relay key: %w", err)
	}
	relaySerial, err := randomSerial()
	if err != nil {
		return nil, nil, "", err
	}
	relayTemplate := &x509.Certificate{
		SerialNumber: relaySerial,
		Subject:      pkix.Name{CommonName: "Mobile Egress Relay"},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.AddDate(2, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if address := net.ParseIP(publicName); address != nil {
		relayTemplate.IPAddresses = []net.IP{address}
	} else {
		relayTemplate.DNSNames = []string{publicName}
	}
	relayDER, err := x509.CreateCertificate(rand.Reader, relayTemplate, caCert, &relayKey.PublicKey, caKey)
	if err != nil {
		return nil, nil, "", fmt.Errorf("create relay certificate: %w", err)
	}
	relayKeyDER, err := x509.MarshalPKCS8PrivateKey(relayKey)
	if err != nil {
		return nil, nil, "", fmt.Errorf("encode relay key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: relayDER}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: relayKeyDER}),
		strings.ToUpper(relaySerial.Text(16)), nil
}
