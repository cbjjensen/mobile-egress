package service

import (
	"bytes"
	"context"
	"crypto"
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

	"mobile-egress/pairing"
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
	PublicURL  string
}

type BootstrapOwnerOptions struct {
	StateDir   string
	PublicName string
	PublicURL  string
	CSRPEM     string
}

func Initialize(ctx context.Context, options InitOptions) (string, error) {
	if options.PublicURL == "" && strings.TrimSpace(options.PublicName) != "" {
		options.PublicURL = "https://" + net.JoinHostPort(strings.TrimSpace(options.PublicName), "8443")
	}
	capability, capabilityHash, err := newCapability()
	if err != nil {
		return "", err
	}
	err = initializeState(ctx, options, func(database *store, _ *x509.Certificate, _ crypto.Signer, _ []byte) error {
		now := time.Now().UTC()
		if err := database.insertCapability(ctx, capabilityHash, enrollment.RoleOwner, now, now.Add(capabilityLifetime)); err != nil {
			return err
		}
		return database.setRelayURL(ctx, options.PublicURL)
	})
	if err != nil {
		return "", err
	}
	return capability, nil
}

func BootstrapOwner(ctx context.Context, options BootstrapOwnerOptions) (EnrollmentResult, error) {
	origin, err := validateRelayOrigin(options.PublicURL, options.PublicName)
	if err != nil {
		return EnrollmentResult{}, err
	}
	publicKey, err := parseDevicePublicKey(options.CSRPEM, "")
	if err != nil {
		return EnrollmentResult{}, fmt.Errorf("parse Owner certificate request: %w", err)
	}

	var result EnrollmentResult
	err = initializeState(ctx, InitOptions{
		StateDir: options.StateDir, PublicName: options.PublicName, PublicURL: origin,
	}, func(database *store, caCert *x509.Certificate, caKey crypto.Signer, caCertPEM []byte) error {
		now := time.Now().UTC()
		serialNumber, err := randomSerial()
		if err != nil {
			return err
		}
		serial := strings.ToUpper(serialNumber.Text(16))
		certificatePEM, err := signDeviceCertificate(caCert, caKey, publicKey, enrollment.RoleOwner, serialNumber, serial, now)
		if err != nil {
			return fmt.Errorf("issue Owner certificate: %w", err)
		}
		if err := database.createIdentity(ctx, serial, enrollment.RoleOwner, now); err != nil {
			return err
		}
		if err := database.setRelayURL(ctx, origin); err != nil {
			return err
		}
		result = EnrollmentResult{
			CertificatePEM: string(certificatePEM) + string(caCertPEM), CACertificatePEM: string(caCertPEM),
			Serial: serial, Role: enrollment.RoleOwner,
		}
		return nil
	})
	if err != nil {
		return EnrollmentResult{}, err
	}
	return result, nil
}

func initializeState(
	ctx context.Context,
	options InitOptions,
	configure func(*store, *x509.Certificate, crypto.Signer, []byte) error,
) error {
	stateDir, err := validateInitOptions(options)
	if err != nil {
		return err
	}
	if options.PublicURL != "" {
		if _, err := validateRelayOrigin(options.PublicURL, options.PublicName); err != nil {
			return err
		}
	}
	if _, err := os.Stat(stateDir); err == nil {
		return errors.New("state directory already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect state directory: %w", err)
	}

	parent := filepath.Dir(stateDir)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("create state parent directory: %w", err)
	}
	temporary, err := os.MkdirTemp(parent, ".relay-init-")
	if err != nil {
		return fmt.Errorf("create temporary state directory: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(temporary)
		}
	}()

	caCertPEM, caKeyPEM, relayCertPEM, relayKeyPEM, err := generateCertificateState(options.PublicName, time.Now().UTC())
	if err != nil {
		return err
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
			return fmt.Errorf("write initialized certificate state: %w", err)
		}
	}

	caCert, caKey, err := parseCertificateAuthorityState(caCertPEM, caKeyPEM)
	if err != nil {
		return err
	}
	database, err := createStore(filepath.Join(temporary, databaseFilename))
	if err != nil {
		return err
	}
	if err := configure(database, caCert, caKey, caCertPEM); err != nil {
		_ = database.Close()
		return err
	}
	if err := database.Close(); err != nil {
		return fmt.Errorf("close initialized SQLite state: %w", err)
	}
	if err := os.Rename(temporary, stateDir); err != nil {
		return fmt.Errorf("commit initialized state: %w", err)
	}
	committed = true
	return nil
}

func validateRelayOrigin(rawURL, publicName string) (string, error) {
	origin, err := pairing.RelayOrigin(rawURL)
	if err != nil || origin.Hostname() != strings.TrimSpace(publicName) {
		return "", errors.New("public URL must be an HTTPS origin for public name")
	}
	return origin.String(), nil
}

func parseCertificateAuthorityState(certificatePEM, privateKeyPEM []byte) (*x509.Certificate, crypto.Signer, error) {
	certificateBlock, rest := pem.Decode(certificatePEM)
	if certificateBlock == nil || certificateBlock.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, nil, errors.New("invalid generated CA certificate")
	}
	certificate, err := x509.ParseCertificate(certificateBlock.Bytes)
	if err != nil {
		return nil, nil, errors.New("invalid generated CA certificate")
	}
	keyBlock, rest := pem.Decode(privateKeyPEM)
	if keyBlock == nil || keyBlock.Type != "PRIVATE KEY" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, nil, errors.New("invalid generated CA private key")
	}
	parsedKey, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, nil, errors.New("invalid generated CA private key")
	}
	key, ok := parsedKey.(crypto.Signer)
	if !ok || !key.Public().(interface{ Equal(crypto.PublicKey) bool }).Equal(certificate.PublicKey) {
		return nil, nil, errors.New("generated CA certificate and private key do not match")
	}
	return certificate, key, nil
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
