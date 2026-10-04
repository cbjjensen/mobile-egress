package nodeservice

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const directStateKey = "direct-client-state-v2"
const directBundleLimit = 64 << 10
const directSignatureDomain = "MobileEgress-Direct-Endpoint-v2\n"

var errDirectInvalid = errors.New("Invalid direct Client request.")
var errDirectStorage = errors.New("Protected Client state is unavailable. Repair the installation.")

func directStrictJSON(raw []byte, value any) error {
	if !utf8.Valid(raw) {
		return errDirectInvalid
	}
	// encoding/json normally accepts duplicate keys; reject them before decoding.
	scanner := json.NewDecoder(bytes.NewReader(raw))
	var walk func() error
	walk = func() error {
		token, err := scanner.Token()
		if err != nil {
			return err
		}
		delim, isDelim := token.(json.Delim)
		if !isDelim {
			return nil
		}
		if delim != '{' && delim != '[' {
			return errDirectInvalid
		}
		seen := map[string]bool{}
		for scanner.More() {
			if delim == '{' {
				k, err := scanner.Token()
				if err != nil {
					return err
				}
				key, ok := k.(string)
				if !ok || seen[key] {
					return errDirectInvalid
				}
				seen[key] = true
			}
			if err := walk(); err != nil {
				return err
			}
		}
		_, err = scanner.Token()
		return err
	}
	if err := walk(); err != nil {
		return errDirectInvalid
	}
	if _, err := scanner.Token(); err != io.EOF {
		return errDirectInvalid
	}
	if directExactFields(raw, reflect.TypeOf(value)) != nil {
		return errDirectInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return errDirectInvalid
	}
	return nil
}

// encoding/json's case-insensitive field matching is unsuitable for the wire
// contract: each spelling must be exact, including nested persistent records.
func directExactFields(raw []byte, kind reflect.Type) error {
	for kind.Kind() == reflect.Pointer {
		kind = kind.Elem()
	}
	if kind == reflect.TypeOf(time.Time{}) || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	if kind.Kind() == reflect.Struct {
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil {
			return errDirectInvalid
		}
		known := map[string]reflect.Type{}
		for i := 0; i < kind.NumField(); i++ {
			field := kind.Field(i)
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			if name == "-" {
				continue
			}
			if name == "" {
				name = field.Name
			}
			known[name] = field.Type
		}
		for name, value := range fields {
			if name == "transport" && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return errDirectInvalid
			}
			field, ok := known[name]
			if !ok {
				return errDirectInvalid
			}
			if err := directExactFields(value, field); err != nil {
				return err
			}
		}
	} else if (kind.Kind() == reflect.Slice || kind.Kind() == reflect.Array) && kind.Elem().Kind() != reflect.Uint8 {
		var elements []json.RawMessage
		if json.Unmarshal(raw, &elements) != nil {
			return errDirectInvalid
		}
		for _, value := range elements {
			if err := directExactFields(value, kind.Elem()); err != nil {
				return err
			}
		}
	}
	return nil
}

func directID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = b[6]&15 | 64
	b[8] = b[8]&63 | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}
func directRandom() (string, error) {
	b := make([]byte, 32)
	_, err := rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b), err
}
func directSerial() (*big.Int, error) {
	return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
}
func directKeyPEM(key *ecdsa.PrivateKey) (string, error) {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), nil
}
func directCA(state *directState) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	block, rest := pem.Decode([]byte(state.CACertificatePEM))
	if block == nil || len(bytes.TrimSpace(rest)) != 0 {
		return nil, nil, errDirectStorage
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, nil, errDirectStorage
	}
	block, rest = pem.Decode([]byte(state.CAPrivateKeyPEM))
	if block == nil || len(bytes.TrimSpace(rest)) != 0 {
		return nil, nil, errDirectStorage
	}
	private, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, nil, errDirectStorage
	}
	key, ok := private.(*ecdsa.PrivateKey)
	if !ok || key.Curve != elliptic.P256() || !key.PublicKey.Equal(cert.PublicKey) || !cert.IsCA || time.Now().After(cert.NotAfter) {
		return nil, nil, errDirectStorage
	}
	return cert, key, nil
}
func newDirectState() (*directState, error) {
	id, err := directID()
	if err != nil {
		return nil, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := directSerial()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "Mobile Egress Client " + id}, NotBefore: now.Add(-time.Minute), NotAfter: now.AddDate(10, 0, 0), IsCA: true, BasicConstraintsValid: true, MaxPathLenZero: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature | x509.KeyUsageCRLSign}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	private, err := directKeyPEM(key)
	if err != nil {
		return nil, err
	}
	password, err := directRandom()
	if err != nil {
		return nil, err
	}
	return &directState{Version: 2, ClientID: id, CACertificatePEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), CAPrivateKeyPEM: private, Username: "mobile-egress", Password: password}, nil
}
func validateDirectConfiguration(config DirectConfiguration) (DirectConfiguration, error) {
	if config.Transport != "" && config.Transport != "direct" && config.Transport != "hosted" {
		return config, errDirectInvalid
	}
	config.DisplayName = strings.TrimSpace(config.DisplayName)
	if !utf8.ValidString(config.DisplayName) || len(config.DisplayName) == 0 || len(config.DisplayName) > 80 || strings.IndexFunc(config.DisplayName, unicode.IsControl) >= 0 {
		return config, errDirectInvalid
	}
	if config.Transport == "hosted" {
		if config.BindAddress != "" {
			return config, errDirectInvalid
		}
		origin, err := directHTTPSOrigin(config.Endpoint)
		if err != nil {
			return config, err
		}
		config.Endpoint = origin.String()
		return config, nil
	}
	if config.BindAddress == "" {
		config.BindAddress = ":8443"
	}
	host, port, err := net.SplitHostPort(config.BindAddress)
	if err != nil {
		return config, errDirectInvalid
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 || (host != "" && net.ParseIP(host) == nil) {
		return config, errDirectInvalid
	}
	origin, err := directHTTPSOrigin(config.Endpoint)
	if err != nil {
		return config, err
	}
	config.Endpoint = origin.String()
	return config, nil
}

// DecodeDirectConfiguration keeps the protected IPC mode schema identical to
// persisted configuration, rejecting duplicate, null and case-folded fields.
func DecodeDirectConfiguration(raw string) (DirectConfiguration, error) {
	var c DirectConfiguration
	if directStrictJSON([]byte(raw), &c) != nil {
		return c, errDirectInvalid
	}
	return validateDirectConfiguration(c)
}

// Mobile URL implementations normalize numeric ports, DNS case and IP text.
// Emit one spelling for newly configured origins and use it for comparisons.
func directHTTPSOrigin(endpoint string) (*url.URL, error) {
	origin, err := url.Parse(endpoint)
	if err != nil || origin.Scheme != "https" || origin.Host == "" || origin.User != nil || origin.RawQuery != "" || origin.ForceQuery || origin.Fragment != "" || origin.RawPath != "" || (origin.Path != "" && origin.Path != "/") || strings.ContainsAny(origin.Host, "\\%\r\n\t ") {
		return nil, errDirectInvalid
	}
	port := 443
	if p := origin.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return nil, errDirectInvalid
		}
		port = n
	}
	host := origin.Hostname()
	if host == "" || len(host) > 253 {
		return nil, errDirectInvalid
	}
	if ip := net.ParseIP(host); ip != nil {
		host = ip.String()
	} else {
		host = strings.ToLower(host)
	}
	if port != 443 {
		origin.Host = net.JoinHostPort(host, strconv.Itoa(port))
	} else if strings.Contains(host, ":") {
		origin.Host = "[" + host + "]"
	} else {
		origin.Host = host
	}
	origin.Path = ""
	return origin, nil
}
func directServerCertificate(state *directState, endpoint string) error {
	ca, key, err := directCA(state)
	if err != nil {
		return err
	}
	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := directSerial()
	if err != nil {
		return err
	}
	now := time.Now()
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: state.ClientID}, NotBefore: now.Add(-time.Minute), NotAfter: now.AddDate(0, 3, 0), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	origins := []string{endpoint}
	if state.AcknowledgedEndpoint != "" {
		origins = append(origins, state.AcknowledgedEndpoint)
	}
	if state.AcknowledgedEndpoint == "" && state.Invitation != nil {
		origins = append(origins, state.Invitation.Endpoint)
	}
	if state.Configuration != nil {
		origins = append(origins, state.Configuration.Endpoint)
	}
	seen := map[string]bool{}
	for _, raw := range origins {
		u, err := url.Parse(raw)
		if err != nil {
			return err
		}
		host := u.Hostname()
		if seen[host] {
			continue
		}
		seen[host] = true
		if ip := net.ParseIP(host); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else {
			template.DNSNames = append(template.DNSNames, host)
		}
	}
	if template.NotAfter.After(ca.NotAfter) {
		template.NotAfter = ca.NotAfter
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, &serverKey.PublicKey, key)
	if err != nil {
		return err
	}
	private, err := directKeyPEM(serverKey)
	if err != nil {
		return err
	}
	state.ServerCertificatePEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})) + state.CACertificatePEM
	state.ServerPrivateKeyPEM = private
	return nil
}
func directCSR(raw string) (*x509.CertificateRequest, []byte, error) {
	block, rest := pem.Decode([]byte(raw))
	if block == nil || block.Type != "CERTIFICATE REQUEST" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, nil, errDirectInvalid
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil || csr.CheckSignature() != nil {
		return nil, nil, errDirectInvalid
	}
	key, ok := csr.PublicKey.(*ecdsa.PublicKey)
	if !ok || key.Curve != elliptic.P256() {
		return nil, nil, errDirectInvalid
	}
	der, err := x509.MarshalPKIXPublicKey(key)
	return csr, der, err
}
func directIssue(state *directState, pairingID string, csr *x509.CertificateRequest) (directIdentityResponse, error) {
	ca, key, err := directCA(state)
	if err != nil {
		return directIdentityResponse{}, err
	}
	serial, err := directSerial()
	if err != nil {
		return directIdentityResponse{}, err
	}
	now := time.Now()
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: pairingID, OrganizationalUnit: []string{"agent"}}, NotBefore: now.Add(-time.Minute), NotAfter: now.AddDate(0, 0, 30), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	if template.NotAfter.After(ca.NotAfter) {
		template.NotAfter = ca.NotAfter
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, csr.PublicKey, key)
	if err != nil {
		return directIdentityResponse{}, err
	}
	return directIdentityResponse{CertificatePEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})) + state.CACertificatePEM, CACertificatePEM: state.CACertificatePEM, Serial: strings.ToUpper(serial.Text(16)), Role: "agent", ClientID: state.ClientID, PairingID: pairingID, Generation: state.Generation}, nil
}
func directTLS(state *directState) (*tls.Config, error) {
	certificate, err := tls.X509KeyPair([]byte(state.ServerCertificatePEM), []byte(state.ServerPrivateKeyPEM))
	if err != nil {
		return nil, errDirectStorage
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(state.CACertificatePEM)) {
		return nil, errDirectStorage
	}
	return &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate}, ClientCAs: roots, ClientAuth: tls.VerifyClientCertIfGiven, NextProtos: []string{"http/1.1"}}, nil
}

func directServerNeedsRenewal(state *directState) bool {
	block, _ := pem.Decode([]byte(state.ServerCertificatePEM))
	if block == nil {
		return true
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	return err != nil || time.Now().Before(certificate.NotBefore) || time.Until(certificate.NotAfter) < 7*24*time.Hour
}
func directEndpointBundle(state *directState) (string, error) {
	if state.Configuration == nil || state.Pairing == nil || state.Pairing.Revoked {
		return "", errors.New("Pair a phone before exporting an update.")
	}
	payload, err := json.Marshal(struct {
		ClientID   string `json:"clientId"`
		PairingID  string `json:"pairingId"`
		Generation uint64 `json:"generation"`
		Endpoint   string `json:"endpoint"`
		Transport  string `json:"transport,omitempty"`
	}{state.ClientID, state.Pairing.ID, state.Generation, state.Configuration.Endpoint, wireTransport(state.Configuration.Transport)})
	if err != nil {
		return "", err
	}
	_, key, err := directCA(state)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(append([]byte(directSignatureDomain), payload...))
	signature, err := ecdsa.SignASN1(rand.Reader, key, digest[:])
	if err != nil {
		return "", err
	}
	wrapper, err := json.Marshal(struct {
		Version   int    `json:"version"`
		Type      string `json:"type"`
		Payload   string `json:"payload"`
		Signature string `json:"signature"`
	}{2, "mobile-egress-direct-endpoint-update", base64.RawURLEncoding.EncodeToString(payload), base64.RawURLEncoding.EncodeToString(signature)})
	return base64.RawURLEncoding.EncodeToString(wrapper), err
}
