//go:build capacityharness

// Package capacityharness implements the developer-only authenticated stream
// capacity acceptance harness. The capacityharness build tag excludes it from
// normal builds and release artifacts.
package capacityharness

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"path/filepath"
	"strings"
)

const (
	tokenBytes             = 32
	maxSecretDocumentBytes = 16 << 10
)

type RunSecrets struct {
	Token      []byte
	TargetHost string
	TargetPort uint16
}

type TargetSecrets struct {
	Token           []byte
	Hostname        string
	CertificateFile string
	PrivateKeyFile  string
}

type runSecretDocument struct {
	Token      string `json:"token"`
	TargetHost string `json:"targetHost"`
	TargetPort uint16 `json:"targetPort"`
}

type targetSecretDocument struct {
	Token           string `json:"token"`
	Hostname        string `json:"hostname"`
	CertificateFile string `json:"certificateFile"`
	PrivateKeyFile  string `json:"privateKeyFile"`
}

func ReadRunSecrets(reader io.Reader) (RunSecrets, error) {
	var document runSecretDocument
	if err := decodeSecretDocument(reader, &document, "token", "targetHost", "targetPort"); err != nil {
		return RunSecrets{}, err
	}
	token, err := decodeToken(document.Token)
	if err != nil || !validPublicHostname(document.TargetHost) || document.TargetPort != 443 {
		clear(token)
		return RunSecrets{}, errors.New("capacity harness secret input is invalid")
	}
	return RunSecrets{Token: token, TargetHost: strings.ToLower(document.TargetHost), TargetPort: document.TargetPort}, nil
}

func ReadTargetSecrets(reader io.Reader) (TargetSecrets, error) {
	var document targetSecretDocument
	if err := decodeSecretDocument(reader, &document, "token", "hostname", "certificateFile", "privateKeyFile"); err != nil {
		return TargetSecrets{}, err
	}
	token, err := decodeToken(document.Token)
	certificateFile, certificateOK := protectedInputPath(document.CertificateFile)
	privateKeyFile, privateKeyOK := protectedInputPath(document.PrivateKeyFile)
	if err != nil || !validPublicHostname(document.Hostname) || !certificateOK || !privateKeyOK || certificateFile == privateKeyFile {
		clear(token)
		return TargetSecrets{}, errors.New("capacity harness secret input is invalid")
	}
	return TargetSecrets{
		Token: token, Hostname: strings.ToLower(document.Hostname),
		CertificateFile: certificateFile, PrivateKeyFile: privateKeyFile,
	}, nil
}

func decodeSecretDocument(reader io.Reader, destination any, allowedFields ...string) error {
	if reader == nil {
		return errors.New("capacity harness secret input is invalid")
	}
	raw, err := io.ReadAll(io.LimitReader(reader, maxSecretDocumentBytes+1))
	if err != nil || len(raw) == 0 || len(raw) > maxSecretDocumentBytes {
		clear(raw)
		return errors.New("capacity harness secret input is invalid")
	}
	defer clear(raw)
	if !hasExactUniqueObjectFields(raw, allowedFields) {
		return errors.New("capacity harness secret input is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(destination) != nil {
		return errors.New("capacity harness secret input is invalid")
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("capacity harness secret input is invalid")
	}
	return nil
}

func hasExactUniqueObjectFields(raw []byte, allowedFields []string) bool {
	allowed := make(map[string]struct{}, len(allowedFields))
	for _, field := range allowedFields {
		allowed[field] = struct{}{}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	opening, ok := token.(json.Delim)
	if err != nil || !ok || opening != '{' {
		return false
	}
	seen := make(map[string]struct{}, len(allowedFields))
	for decoder.More() {
		token, err = decoder.Token()
		field, ok := token.(string)
		if err != nil || !ok {
			return false
		}
		if _, permitted := allowed[field]; !permitted {
			return false
		}
		if _, duplicate := seen[field]; duplicate {
			return false
		}
		seen[field] = struct{}{}
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			clear(value)
			return false
		}
		clear(value)
	}
	token, err = decoder.Token()
	closing, ok := token.(json.Delim)
	return err == nil && ok && closing == '}'
}

func decodeToken(encoded string) ([]byte, error) {
	if encoded == "" || encoded != strings.TrimSpace(encoded) {
		return nil, errors.New("invalid token")
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil || len(decoded) != tokenBytes {
		clear(decoded)
		return nil, errors.New("invalid token")
	}
	allSame := true
	for _, value := range decoded[1:] {
		if value != decoded[0] {
			allSame = false
			break
		}
	}
	if allSame {
		clear(decoded)
		return nil, errors.New("invalid token")
	}
	return decoded, nil
}

func validPublicHostname(host string) bool {
	if host == "" || host != strings.TrimSpace(host) || host != strings.ToLower(host) || len(host) > 253 || net.ParseIP(host) != nil || !strings.Contains(host, ".") {
		return false
	}
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if !((character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '-') {
				return false
			}
		}
	}
	return true
}

func protectedInputPath(value string) (string, bool) {
	if value == "" || value != strings.TrimSpace(value) || strings.ContainsRune(value, '\x00') || !filepath.IsAbs(value) {
		return "", false
	}
	cleaned := filepath.Clean(value)
	if cleaned == filepath.VolumeName(cleaned)+string(filepath.Separator) {
		return "", false
	}
	return cleaned, true
}

func (secrets *RunSecrets) Zero() {
	if secrets != nil {
		clear(secrets.Token)
		secrets.TargetHost = ""
		secrets.TargetPort = 0
	}
}

func (secrets *TargetSecrets) Zero() {
	if secrets != nil {
		clear(secrets.Token)
		secrets.Hostname = ""
		secrets.CertificateFile = ""
		secrets.PrivateKeyFile = ""
	}
}
