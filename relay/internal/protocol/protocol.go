// Package protocol validates relay tunnel envelopes.
package protocol

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	Version1               = 1
	MaxDecodedPayloadBytes = 1 << 20
)

var (
	ErrInvalidEnvelope = errors.New("invalid protocol envelope")
)

// MessageType is a finite v1 protocol message type.
type MessageType string

const (
	TypeOpen     MessageType = "open"
	TypeOpened   MessageType = "opened"
	TypeRejected MessageType = "rejected"
	TypeData     MessageType = "data"
	TypeClose    MessageType = "close"
	TypePing     MessageType = "ping"
	TypePong     MessageType = "pong"
)

// Envelope is the decoded form of a v1 tunnel protocol message.
type Envelope struct {
	Version  int         `json:"version"`
	Type     MessageType `json:"type"`
	StreamID string      `json:"streamId"`
	Payload  string      `json:"payload"`
}

type rawEnvelope struct {
	Version  *int         `json:"version"`
	Type     *MessageType `json:"type"`
	StreamID *string      `json:"streamId"`
	Payload  *string      `json:"payload"`
}

// ParseEnvelope decodes and validates a v1 JSON envelope.
func ParseEnvelope(raw []byte) (Envelope, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()

	var decoded rawEnvelope
	if err := decoder.Decode(&decoded); err != nil {
		return Envelope{}, fmt.Errorf("%w: malformed JSON", ErrInvalidEnvelope)
	}
	if err := ensureEOF(decoder); err != nil {
		return Envelope{}, fmt.Errorf("%w: trailing JSON", ErrInvalidEnvelope)
	}
	if decoded.Version == nil || decoded.Type == nil || decoded.StreamID == nil || decoded.Payload == nil {
		return Envelope{}, fmt.Errorf("%w: required field missing", ErrInvalidEnvelope)
	}

	envelope := Envelope{
		Version:  *decoded.Version,
		Type:     *decoded.Type,
		StreamID: *decoded.StreamID,
		Payload:  *decoded.Payload,
	}
	if err := envelope.Validate(); err != nil {
		return Envelope{}, err
	}
	return envelope, nil
}

// Validate confirms that an already-decoded envelope satisfies the v1 wire
// constraints.
func (envelope Envelope) Validate() error {
	if envelope.Version != Version1 {
		return fmt.Errorf("%w: unsupported version", ErrInvalidEnvelope)
	}
	if !isValidMessageType(envelope.Type) {
		return fmt.Errorf("%w: unsupported message type", ErrInvalidEnvelope)
	}

	isKeepalive := envelope.Type == TypePing || envelope.Type == TypePong
	if isKeepalive && envelope.StreamID != "" {
		return fmt.Errorf("%w: keepalive stream ID must be empty", ErrInvalidEnvelope)
	}
	if !isKeepalive && strings.TrimSpace(envelope.StreamID) == "" {
		return fmt.Errorf("%w: stream ID is required", ErrInvalidEnvelope)
	}

	if _, err := envelope.DecodePayload(); err != nil {
		return err
	}
	return nil
}

// DecodePayload returns Payload after base64url decoding and enforces the
// decoded one MiB payload limit.
func (envelope Envelope) DecodePayload() ([]byte, error) {
	if base64.RawURLEncoding.DecodedLen(len(envelope.Payload)) > MaxDecodedPayloadBytes {
		return nil, fmt.Errorf("%w: payload too large", ErrInvalidEnvelope)
	}
	payload, err := base64.RawURLEncoding.DecodeString(envelope.Payload)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid base64url payload", ErrInvalidEnvelope)
	}
	if len(payload) > MaxDecodedPayloadBytes {
		return nil, fmt.Errorf("%w: payload too large", ErrInvalidEnvelope)
	}
	return payload, nil
}

func ensureEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if err == io.EOF {
		return nil
	}
	return err
}

func isValidMessageType(messageType MessageType) bool {
	switch messageType {
	case TypeOpen, TypeOpened, TypeRejected, TypeData, TypeClose, TypePing, TypePong:
		return true
	default:
		return false
	}
}
