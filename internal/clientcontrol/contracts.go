// Package clientcontrol defines the standalone Client control protocol. It
// carries public enrollment material and encrypted configuration, never keys.
package clientcontrol

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"strings"
	"time"
	"unicode"

	"mobile-egress/pairing"
)

const Version = 1
const InvitationType = "client-enrollment"
const EndpointUpdateType = "client-endpoint-update"
const InvitationLifetime = 10 * time.Minute
const MaxMessageBytes = 2 << 20

type Invitation struct {
	Version          int       `json:"version"`
	Type             string    `json:"type"`
	ID               string    `json:"id"`
	NodeID           string    `json:"nodeId"`
	DisplayName      string    `json:"displayName"`
	RelayURL         string    `json:"relayUrl"`
	CACertificatePEM string    `json:"caCertificatePem"`
	Capability       string    `json:"capability"`
	ExpiresAt        time.Time `json:"expiresAt"`
}

type Bootstrap struct {
	CSRPEM                 string `json:"csrPem"`
	ConfigurationPublicKey string `json:"configurationPublicKey"`
	Platform               string `json:"platform"`
	Architecture           string `json:"architecture"`
	ServiceVersion         string `json:"serviceVersion"`
}

type ConfigurationDelivery struct {
	Generation uint64          `json:"generation"`
	Envelope   json.RawMessage `json:"envelope"`
}

type Enrollment struct {
	ID                string                 `json:"id"`
	NodeID            string                 `json:"nodeId"`
	DisplayName       string                 `json:"displayName"`
	State             string                 `json:"state"`
	Bootstrap         *Bootstrap             `json:"bootstrap,omitempty"`
	ClientSerial      string                 `json:"clientSerial,omitempty"`
	CertificatePEM    string                 `json:"certificatePem,omitempty"`
	CACertificatePEM  string                 `json:"caCertificatePem,omitempty"`
	AppliedGeneration uint64                 `json:"appliedGeneration"`
	ServiceVersion    string                 `json:"serviceVersion,omitempty"`
	ExpiresAt         time.Time              `json:"expiresAt"`
	Configuration     *ConfigurationDelivery `json:"configuration,omitempty"`
}

type Status struct {
	EnrollmentID      string    `json:"enrollmentId,omitempty"`
	NodeID            string    `json:"nodeId,omitempty"`
	ClientSerial      string    `json:"clientSerial"`
	Connected         bool      `json:"connected"`
	AppliedGeneration uint64    `json:"appliedGeneration"`
	ServiceVersion    string    `json:"serviceVersion,omitempty"`
	Platform          string    `json:"platform,omitempty"`
	Architecture      string    `json:"architecture,omitempty"`
	LastSeen          time.Time `json:"lastSeen,omitempty"`
}

type EndpointUpdate struct {
	Version      int             `json:"version"`
	Type         string          `json:"type"`
	NodeID       string          `json:"nodeId"`
	EnrollmentID string          `json:"enrollmentId"`
	Envelope     json.RawMessage `json:"envelope"`
}

func (value Invitation) Validate() error {
	if value.Version != Version || value.Type != InvitationType || !ValidID(value.ID) || !ValidID(value.NodeID) || !ValidText(value.DisplayName, 128) || len(value.CACertificatePEM) > 16384 || value.ExpiresAt.IsZero() {
		return errors.New("invalid Client invitation")
	}
	if _, err := pairing.RelayOrigin(value.RelayURL); err != nil {
		return errors.New("invalid invitation relay origin")
	}
	if _, err := pairing.CACertificate(value.CACertificatePEM); err != nil {
		return errors.New("invalid invitation relay CA")
	}
	capability, err := base64.RawURLEncoding.DecodeString(value.Capability)
	if err != nil || len(capability) != 32 {
		return errors.New("invalid invitation capability")
	}
	return nil
}

func (value Bootstrap) Validate() error {
	if !((value.Platform == "windows" && value.Architecture == "amd64") || (value.Platform == "macos" && value.Architecture == "arm64")) || !ValidText(value.ServiceVersion, 64) {
		return errors.New("unsupported Client platform or version")
	}
	if len(value.CSRPEM) > 16384 {
		return errors.New("invalid Client certificate request")
	}
	block, rest := pem.Decode([]byte(value.CSRPEM))
	if block == nil || block.Type != "CERTIFICATE REQUEST" || len(bytes.TrimSpace(rest)) != 0 {
		return errors.New("invalid Client certificate request")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil || csr.CheckSignature() != nil {
		return errors.New("invalid Client certificate request")
	}
	if !validPublicKey(value.ConfigurationPublicKey) {
		return errors.New("invalid Client configuration public key")
	}
	return nil
}

func ValidID(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func ValidText(value string, maximum int) bool {
	if value == "" || len(value) > maximum || strings.TrimSpace(value) != value {
		return false
	}
	for _, c := range value {
		if unicode.IsControl(c) {
			return false
		}
	}
	return true
}

func validPublicKey(value string) bool {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(raw) != 32 {
		return false
	}
	key, err := ecdh.X25519().NewPublicKey(raw)
	if err != nil {
		return false
	}
	private, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return false
	}
	_, err = private.ECDH(key)
	return err == nil
}

// ValidateEnvelope bounds and validates the public envelope framing without
// decrypting it. Configuration confidentiality remains end to end.
func ValidateEnvelope(raw json.RawMessage) error {
	var envelope struct {
		Version            int    `json:"version"`
		EphemeralPublicKey string `json:"ephemeralPublicKey"`
		Nonce              string `json:"nonce"`
		Ciphertext         string `json:"ciphertext"`
	}
	if err := DecodeJSON(raw, &envelope); err != nil || envelope.Version != 1 || !validPublicKey(envelope.EphemeralPublicKey) {
		return errors.New("invalid sealed configuration")
	}
	nonce, err := base64.RawURLEncoding.DecodeString(envelope.Nonce)
	if err != nil || len(nonce) != 12 {
		return errors.New("invalid sealed configuration nonce")
	}
	ciphertext, err := base64.RawURLEncoding.DecodeString(envelope.Ciphertext)
	if err != nil || len(ciphertext) < 17 || len(ciphertext) > (1<<20)+64 {
		return errors.New("invalid sealed configuration ciphertext")
	}
	return nil
}

func DecodeJSON(raw []byte, destination any) error {
	if len(raw) == 0 || len(raw) > MaxMessageBytes {
		return errors.New("control message missing or too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return errors.New("invalid control message")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("trailing control message data")
	}
	return nil
}

func EncodeInvitation(value Invitation) (string, error) {
	if err := value.Validate(); err != nil {
		return "", err
	}
	return encode(value)
}
func DecodeInvitation(value string) (Invitation, error) {
	var result Invitation
	if err := decode(value, &result); err != nil {
		return result, err
	}
	return result, result.Validate()
}
func Encode(value Invitation) (string, error) { return EncodeInvitation(value) }
func Decode(value string) (Invitation, error) { return DecodeInvitation(value) }
func (value EndpointUpdate) Validate() error {
	if value.Version != Version || value.Type != EndpointUpdateType || !ValidID(value.NodeID) || !ValidID(value.EnrollmentID) {
		return errors.New("invalid Client endpoint update")
	}
	return ValidateEnvelope(value.Envelope)
}
func EncodeEndpointUpdate(value EndpointUpdate) (string, error) {
	if err := value.Validate(); err != nil {
		return "", err
	}
	return encode(value)
}
func DecodeEndpointUpdate(value string) (EndpointUpdate, error) {
	var result EndpointUpdate
	if err := decode(value, &result); err != nil {
		return result, err
	}
	return result, result.Validate()
}
func encode(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	if len(raw) > MaxMessageBytes {
		return "", errors.New("control message too large")
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
func decode(value string, destination any) error {
	value = strings.TrimSpace(value)
	if len(value) > base64.RawURLEncoding.EncodedLen(MaxMessageBytes) {
		return errors.New("control message too large")
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return errors.New("invalid control message encoding")
	}
	return DecodeJSON(raw, destination)
}
