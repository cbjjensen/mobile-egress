package relayclient

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"mobile-egress/internal/tunnelwire"
)

type wireEnvelope = tunnelwire.Envelope

func parseWireEnvelope(raw []byte) (wireEnvelope, error) {
	return tunnelwire.ParseEnvelope(raw)
}

func decodeWirePayload(value string) ([]byte, error) {
	return (wireEnvelope{Payload: value}).DecodePayload()
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
