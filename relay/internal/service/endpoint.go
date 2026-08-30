package service

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
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

	rollback, finalize, err := stageRotatedEndpointFiles(stateDir, certificatePEM, privateKeyPEM)
	if err != nil {
		return RotateEndpointResult{}, err
	}
	state, err := openStore(filepath.Join(stateDir, databaseFilename))
	if err == nil {
		err = state.setRelayURL(ctx, origin)
		closeErr := state.Close()
		if err == nil && closeErr != nil {
			err = closeErr
		}
	}
	if err != nil {
		rollback()
		return RotateEndpointResult{}, fmt.Errorf("persist rotated endpoint: %w", err)
	}
	if err := finalize(); err != nil {
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

func stageRotatedEndpointFiles(stateDir string, certificatePEM, privateKeyPEM []byte) (func(), func() error, error) {
	certificatePath := filepath.Join(stateDir, relayCertFilename)
	keyPath := filepath.Join(stateDir, relayKeyFilename)
	temporaryCertificate, err := writeTemporaryStateFile(stateDir, ".relay-cert-", certificatePEM, 0o644)
	if err != nil {
		return nil, nil, err
	}
	temporaryKey, err := writeTemporaryStateFile(stateDir, ".relay-key-", privateKeyPEM, 0o600)
	if err != nil {
		_ = os.Remove(temporaryCertificate)
		return nil, nil, err
	}
	cleanupTemporary := func() {
		_ = os.Remove(temporaryCertificate)
		_ = os.Remove(temporaryKey)
	}
	if _, err := tls.LoadX509KeyPair(temporaryCertificate, temporaryKey); err != nil {
		cleanupTemporary()
		return nil, nil, fmt.Errorf("validate rotated relay identity: %w", err)
	}

	certificateBackup, err := unusedTemporaryPath(stateDir, ".relay-cert-backup-")
	if err != nil {
		cleanupTemporary()
		return nil, nil, err
	}
	keyBackup, err := unusedTemporaryPath(stateDir, ".relay-key-backup-")
	if err != nil {
		cleanupTemporary()
		return nil, nil, err
	}
	rollback := func() {
		_ = os.Remove(certificatePath)
		_ = os.Remove(keyPath)
		_ = os.Rename(certificateBackup, certificatePath)
		_ = os.Rename(keyBackup, keyPath)
		cleanupTemporary()
	}
	if err := os.Rename(certificatePath, certificateBackup); err != nil {
		cleanupTemporary()
		return nil, nil, fmt.Errorf("stage relay certificate rotation: %w", err)
	}
	if err := os.Rename(keyPath, keyBackup); err != nil {
		_ = os.Rename(certificateBackup, certificatePath)
		cleanupTemporary()
		return nil, nil, fmt.Errorf("stage relay key rotation: %w", err)
	}
	if err := os.Rename(temporaryCertificate, certificatePath); err != nil {
		rollback()
		return nil, nil, fmt.Errorf("commit relay certificate rotation: %w", err)
	}
	if err := os.Rename(temporaryKey, keyPath); err != nil {
		rollback()
		return nil, nil, fmt.Errorf("commit relay key rotation: %w", err)
	}
	finalize := func() error {
		if err := os.Remove(certificateBackup); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove relay certificate backup: %w", err)
		}
		if err := os.Remove(keyBackup); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove relay key backup: %w", err)
		}
		return nil
	}
	return rollback, finalize, nil
}

func writeTemporaryStateFile(directory, pattern string, contents []byte, mode os.FileMode) (string, error) {
	file, err := os.CreateTemp(directory, pattern)
	if err != nil {
		return "", fmt.Errorf("create temporary relay state: %w", err)
	}
	path := file.Name()
	defer func() {
		if file != nil {
			_ = file.Close()
		}
	}()
	if err := file.Chmod(mode); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("protect temporary relay state: %w", err)
	}
	if _, err := file.Write(contents); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("write temporary relay state: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("flush temporary relay state: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("close temporary relay state: %w", err)
	}
	file = nil
	return path, nil
}

func unusedTemporaryPath(directory, pattern string) (string, error) {
	file, err := os.CreateTemp(directory, pattern)
	if err != nil {
		return "", err
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		return "", err
	}
	if err := os.Remove(path); err != nil {
		return "", err
	}
	return path, nil
}
