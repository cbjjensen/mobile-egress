package service

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mobile-egress/relay/internal/enrollment"
)

const (
	caCertFilename     = "ca.crt"
	caKeyFilename      = "ca.key"
	relayCertFilename  = "relay.crt"
	relayKeyFilename   = "relay.key"
	databaseFilename   = "state.db"
	capabilityLifetime = 10 * time.Minute
)

type InitOptions struct {
	StateDir   string
	PublicName string
}

func Initialize(ctx context.Context, options InitOptions) (string, error) {
	stateDir, err := validateInitOptions(options)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(stateDir); err == nil {
		return "", errors.New("state directory already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect state directory: %w", err)
	}

	parent := filepath.Dir(stateDir)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return "", fmt.Errorf("create state parent directory: %w", err)
	}
	temporary, err := os.MkdirTemp(parent, ".relay-init-")
	if err != nil {
		return "", fmt.Errorf("create temporary state directory: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(temporary)
		}
	}()

	caCertPEM, caKeyPEM, relayCertPEM, relayKeyPEM, err := generateCertificateState(options.PublicName, time.Now().UTC())
	if err != nil {
		return "", err
	}
	files := []struct {
		name string
		data []byte
		mode os.FileMode
	}{
		{name: caCertFilename, data: caCertPEM, mode: 0o644},
		{name: caKeyFilename, data: caKeyPEM, mode: 0o600},
		{name: relayCertFilename, data: relayCertPEM, mode: 0o644},
		{name: relayKeyFilename, data: relayKeyPEM, mode: 0o600},
	}
	for _, file := range files {
		if err := os.WriteFile(filepath.Join(temporary, file.name), file.data, file.mode); err != nil {
			return "", fmt.Errorf("write initialized certificate state: %w", err)
		}
	}

	database, err := createStore(filepath.Join(temporary, databaseFilename))
	if err != nil {
		return "", err
	}
	capability, capabilityHash, err := newCapability()
	if err == nil {
		now := time.Now().UTC()
		err = database.insertCapability(ctx, capabilityHash, enrollment.RoleOwner, now, now.Add(capabilityLifetime))
	}
	closeErr := database.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", fmt.Errorf("close initialized SQLite state: %w", closeErr)
	}

	if err := os.Rename(temporary, stateDir); err != nil {
		return "", fmt.Errorf("commit initialized state: %w", err)
	}
	committed = true
	return capability, nil
}

func CACertificatePEM(stateDir string) ([]byte, error) {
	value, err := os.ReadFile(filepath.Join(filepath.Clean(stateDir), caCertFilename))
	if err != nil {
		return nil, fmt.Errorf("read relay CA certificate: %w", err)
	}
	return value, nil
}

func validateInitOptions(options InitOptions) (string, error) {
	stateDir := filepath.Clean(strings.TrimSpace(options.StateDir))
	if options.StateDir == "" || stateDir == "." {
		return "", errors.New("state directory is required")
	}
	publicName := strings.TrimSpace(options.PublicName)
	if publicName == "" {
		return "", errors.New("public name is required")
	}
	if net.ParseIP(publicName) == nil {
		if len(publicName) > 253 || strings.ContainsAny(publicName, "/: \\\t\r\n") {
			return "", errors.New("public name must be a DNS name or IP address")
		}
		for _, label := range strings.Split(publicName, ".") {
			if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return "", errors.New("public name must be a DNS name or IP address")
			}
			for _, character := range label {
				if !((character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '-') {
					return "", errors.New("public name must be a DNS name or IP address")
				}
			}
		}
	}
	return stateDir, nil
}

func generateCertificateState(publicName string, now time.Time) ([]byte, []byte, []byte, []byte, error) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("generate CA key: %w", err)
	}
	caSerial, err := randomSerial()
	if err != nil {
		return nil, nil, nil, nil, err
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          caSerial,
		Subject:               pkix.Name{CommonName: "Mobile Egress Private CA"},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("create CA certificate: %w", err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("parse generated CA certificate: %w", err)
	}

	relayKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("generate relay key: %w", err)
	}
	relaySerial, err := randomSerial()
	if err != nil {
		return nil, nil, nil, nil, err
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
		return nil, nil, nil, nil, fmt.Errorf("create relay certificate: %w", err)
	}

	caKeyDER, err := x509.MarshalPKCS8PrivateKey(caKey)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("encode CA key: %w", err)
	}
	relayKeyDER, err := x509.MarshalPKCS8PrivateKey(relayKey)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("encode relay key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: caKeyDER}),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: relayDER}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: relayKeyDER}), nil
}

func randomSerial() (*big.Int, error) {
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return nil, fmt.Errorf("generate certificate serial: %w", err)
	}
	if serial.Sign() == 0 {
		return big.NewInt(1), nil
	}
	return serial, nil
}

func newCapability() (string, [sha256.Size]byte, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", [sha256.Size]byte{}, fmt.Errorf("generate enrollment capability: %w", err)
	}
	capability := base64.RawURLEncoding.EncodeToString(raw)
	return capability, sha256.Sum256([]byte(capability)), nil
}
