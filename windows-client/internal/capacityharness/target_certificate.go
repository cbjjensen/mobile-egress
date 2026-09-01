//go:build capacityharness

package capacityharness

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"
)

const (
	maximumTargetCertificateBytes = 512 << 10
	maximumTargetPrivateKeyBytes  = 128 << 10
)

func LoadTargetTLSConfig(secrets TargetSecrets, roots *x509.CertPool, now time.Time) (*tls.Config, error) {
	if len(secrets.Token) != tokenBytes || !validPublicHostname(secrets.Hostname) ||
		secrets.CertificateFile == secrets.PrivateKeyFile || now.IsZero() {
		return nil, CategorizedError{Category: FailureInput}
	}
	certificatePEM, err := readProtectedFile(secrets.CertificateFile, maximumTargetCertificateBytes)
	if err != nil {
		return nil, CategorizedError{Category: FailureTLS, cause: err}
	}
	defer clear(certificatePEM)
	privateKeyPEM, err := readProtectedFile(secrets.PrivateKeyFile, maximumTargetPrivateKeyBytes)
	if err != nil {
		return nil, CategorizedError{Category: FailureTLS, cause: err}
	}
	defer clear(privateKeyPEM)
	certificate, err := tls.X509KeyPair(certificatePEM, privateKeyPEM)
	if err != nil || len(certificate.Certificate) == 0 {
		return nil, CategorizedError{Category: FailureTLS}
	}
	leaf, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		return nil, CategorizedError{Category: FailureTLS}
	}
	trustedRoots := roots
	if trustedRoots == nil {
		trustedRoots, err = x509.SystemCertPool()
		if err != nil || trustedRoots == nil {
			return nil, CategorizedError{Category: FailureTLS}
		}
	}
	intermediates := x509.NewCertPool()
	for _, raw := range certificate.Certificate[1:] {
		candidate, parseErr := x509.ParseCertificate(raw)
		if parseErr != nil {
			return nil, CategorizedError{Category: FailureTLS}
		}
		intermediates.AddCert(candidate)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{
		DNSName: secrets.Hostname, Roots: trustedRoots, Intermediates: intermediates,
		CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err != nil {
		return nil, CategorizedError{Category: FailureTLS}
	}
	certificate.Leaf = leaf
	return &tls.Config{
		Certificates: []tls.Certificate{certificate},
		MinVersion:   tls.VersionTLS13, MaxVersion: tls.VersionTLS13,
	}, nil
}

func readProtectedFile(path string, maximum int64) ([]byte, error) {
	cleaned, valid := protectedInputPath(path)
	if !valid || maximum <= 0 {
		return nil, errors.New("protected capacity input is invalid")
	}
	info, err := os.Lstat(cleaned)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("protected capacity input is invalid")
	}
	file, err := os.Open(filepath.Clean(cleaned))
	if err != nil {
		return nil, errors.New("protected capacity input is invalid")
	}
	defer file.Close()
	value, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil || len(value) == 0 || int64(len(value)) > maximum {
		clear(value)
		return nil, errors.New("protected capacity input is invalid")
	}
	return value, nil
}
