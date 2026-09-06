package tunnelwire

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
	Version1                   = 1
	MaxDecodedPayloadBytes     = 1 << 20
	MaxDecodedDataPayloadBytes = MaxDataBytes
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

// Envelope holds a tunnel message in its received representation.
type Envelope struct {
	Version  int         `json:"version"`
	Type     MessageType `json:"type"`
	StreamID string      `json:"streamId"`
	Payload  string      `json:"payload"`
	Data     []byte      `json:"-"`
}

// ParseEnvelope validates a legacy JSON envelope or negotiated binary data frame.
// The caller must enforce session negotiation before accepting binary frames.
func ParseEnvelope(raw []byte) (Envelope, error) {
	if IsData(raw) {
		id, data, err := ParseData(raw)
		if err != nil {
			return Envelope{}, err
		}
		return Envelope{Version: Version1, Type: TypeData, StreamID: id, Data: data}, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return Envelope{}, fmt.Errorf("%w: malformed JSON", ErrInvalidEnvelope)
	}

	var envelope Envelope
	seen := make(map[string]bool, 4)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return Envelope{}, fmt.Errorf("%w: malformed JSON", ErrInvalidEnvelope)
		}
		key, ok := token.(string)
		if !ok || !isEnvelopeKey(key) || seen[key] {
			return Envelope{}, fmt.Errorf("%w: invalid envelope field", ErrInvalidEnvelope)
		}
		seen[key] = true

		if err := decodeEnvelopeField(decoder, key, &envelope); err != nil {
			return Envelope{}, err
		}
	}

	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') {
		return Envelope{}, fmt.Errorf("%w: malformed JSON", ErrInvalidEnvelope)
	}
	if _, err := decoder.Token(); err != io.EOF {
		return Envelope{}, fmt.Errorf("%w: trailing JSON", ErrInvalidEnvelope)
	}
	if len(seen) != 4 {
		return Envelope{}, fmt.Errorf("%w: required field missing", ErrInvalidEnvelope)
	}
	if err := envelope.Validate(); err != nil {
		return Envelope{}, err
	}
	return envelope, nil
}

// Validate confirms that an already-decoded envelope satisfies the v1 wire
// constraints.
func (envelope Envelope) Validate() error {
	if envelope.Data != nil {
		if envelope.Version != Version1 || envelope.Type != TypeData || envelope.Payload != "" {
			return ErrInvalidEnvelope
		}
		_, err := EncodeData(envelope.StreamID, envelope.Data)
		return err
	}
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

	payloadLimit := MaxDecodedPayloadBytes
	if envelope.Type == TypeData {
		payloadLimit = MaxDecodedDataPayloadBytes
	}
	if _, err := decodePayload(envelope.Payload, payloadLimit); err != nil {
		return err
	}
	return nil
}

// DecodePayload returns raw data or decodes the legacy base64url payload.
func (envelope Envelope) DecodePayload() ([]byte, error) {
	if envelope.Data != nil {
		return envelope.Data, nil
	}
	return decodePayload(envelope.Payload, MaxDecodedPayloadBytes)
}

// RetainedPayloadBytes accounts for the representation kept in the mailbox.
func (envelope Envelope) RetainedPayloadBytes() int {
	return len(envelope.Payload) + len(envelope.Data)
}

// PayloadBytes is called only after ParseEnvelope has validated the payload.
func (envelope Envelope) PayloadBytes() int {
	if envelope.Data != nil {
		return len(envelope.Data)
	}
	// The legacy Go decoder accepts CR/LF; they consume retained memory, but
	// contribute no decoded traffic bytes.
	return base64.RawURLEncoding.DecodedLen(len(envelope.Payload) - strings.Count(envelope.Payload, "\r") - strings.Count(envelope.Payload, "\n"))
}

// MarshalForPeer translates only when peers use different data representations.
func (envelope Envelope) MarshalForPeer(binaryData bool) ([]byte, error) {
	if envelope.Version != Version1 || !isValidMessageType(envelope.Type) {
		return nil, ErrInvalidEnvelope
	}
	if envelope.Type == TypeData && binaryData && ValidStreamID(envelope.StreamID) {
		data, err := envelope.DecodePayload()
		if err != nil {
			return nil, err
		}
		return EncodeData(envelope.StreamID, data)
	}
	if envelope.Data != nil {
		envelope.Payload = base64.RawURLEncoding.EncodeToString(envelope.Data)
		envelope.Data = nil
	}
	return json.Marshal(envelope)
}

func decodePayload(value string, maximumBytes int) ([]byte, error) {
	if base64.RawURLEncoding.DecodedLen(len(value)) > maximumBytes {
		return nil, fmt.Errorf("%w: payload too large", ErrInvalidEnvelope)
	}
	payload, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid base64url payload", ErrInvalidEnvelope)
	}
	if len(payload) > maximumBytes {
		return nil, fmt.Errorf("%w: payload too large", ErrInvalidEnvelope)
	}
	return payload, nil
}
func decodeEnvelopeField(decoder *json.Decoder, key string, envelope *Envelope) error {
	switch key {
	case "version":
		var value *int
		if err := decoder.Decode(&value); err != nil || value == nil {
			return fmt.Errorf("%w: invalid version", ErrInvalidEnvelope)
		}
		envelope.Version = *value
	case "type":
		var value *MessageType
		if err := decoder.Decode(&value); err != nil || value == nil {
			return fmt.Errorf("%w: invalid message type", ErrInvalidEnvelope)
		}
		envelope.Type = *value
	case "streamId":
		var value *string
		if err := decoder.Decode(&value); err != nil || value == nil {
			return fmt.Errorf("%w: invalid stream ID", ErrInvalidEnvelope)
		}
		envelope.StreamID = *value
	case "payload":
		var value *string
		if err := decoder.Decode(&value); err != nil || value == nil {
			return fmt.Errorf("%w: invalid payload", ErrInvalidEnvelope)
		}
		envelope.Payload = *value
	}
	return nil
}

func isEnvelopeKey(key string) bool {
	return key == "version" || key == "type" || key == "streamId" || key == "payload"
}

func isValidMessageType(messageType MessageType) bool {
	switch messageType {
	case TypeOpen, TypeOpened, TypeRejected, TypeData, TypeClose, TypePing, TypePong:
		return true
	default:
		return false
	}
}
