package relayclient

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
)

const maxPayloadBytes = 1 << 20

type wireEnvelope struct {
	Version  int    `json:"version"`
	Type     string `json:"type"`
	StreamID string `json:"streamId"`
	Payload  string `json:"payload"`
}

func parseWireEnvelope(raw []byte) (wireEnvelope, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var envelope wireEnvelope
	if err := decoder.Decode(&envelope); err != nil || requireJSONEOF(decoder) != nil {
		return wireEnvelope{}, errors.New("invalid relay envelope")
	}
	if envelope.Version != 1 || !validWireType(envelope.Type) {
		return wireEnvelope{}, errors.New("invalid relay envelope")
	}
	keepalive := envelope.Type == "ping" || envelope.Type == "pong"
	if keepalive != (envelope.StreamID == "") {
		return wireEnvelope{}, errors.New("invalid relay envelope stream ID")
	}
	if _, err := decodeWirePayload(envelope.Payload); err != nil {
		return wireEnvelope{}, err
	}
	return envelope, nil
}

func decodeWirePayload(value string) ([]byte, error) {
	if base64.RawURLEncoding.DecodedLen(len(value)) > maxPayloadBytes {
		return nil, errors.New("relay payload too large")
	}
	payload, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(payload) > maxPayloadBytes {
		return nil, errors.New("invalid relay payload")
	}
	return payload, nil
}

func validWireType(value string) bool {
	switch value {
	case "open", "opened", "rejected", "data", "close", "ping", "pong":
		return true
	default:
		return false
	}
}

func marshalWireEnvelope(envelope wireEnvelope) ([]byte, error) {
	if envelope.Version != 1 || !validWireType(envelope.Type) {
		return nil, errors.New("invalid relay envelope")
	}
	return json.Marshal(envelope)
}

func decodeStrictJSON(payload []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}
