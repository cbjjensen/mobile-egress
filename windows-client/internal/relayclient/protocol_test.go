package relayclient

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestParseWireEnvelopeAcceptsDataAtThirtyTwoKiB(t *testing.T) {
	raw, err := json.Marshal(wireEnvelope{
		Version: 1, Type: "data", StreamID: "stream-1",
		Payload: base64.RawURLEncoding.EncodeToString(make([]byte, 32<<10)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseWireEnvelope(raw); err != nil {
		t.Fatalf("parseWireEnvelope() rejected a 32 KiB data payload: %v", err)
	}
}

func TestParseWireEnvelopeRejectsDataOverThirtyTwoKiB(t *testing.T) {
	raw, err := json.Marshal(wireEnvelope{
		Version: 1, Type: "data", StreamID: "stream-1",
		Payload: base64.RawURLEncoding.EncodeToString(make([]byte, 32<<10+1)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseWireEnvelope(raw); err == nil {
		t.Fatal("parseWireEnvelope() accepted a data payload larger than 32 KiB")
	}
}

func TestParseWireEnvelopePreservesLargerNonDataPayloadLimit(t *testing.T) {
	raw, err := json.Marshal(wireEnvelope{
		Version: 1, Type: "rejected", StreamID: "stream-1",
		Payload: base64.RawURLEncoding.EncodeToString(make([]byte, 32<<10+1)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseWireEnvelope(raw); err != nil {
		t.Fatalf("parseWireEnvelope() applied the data limit to a non-data payload: %v", err)
	}
}

func TestParseWireEnvelopeRejectsAmbiguousFields(t *testing.T) {
	for name, raw := range map[string]string{
		"duplicate":      `{"version":2,"version":1,"type":"ping","streamId":"","payload":""}`,
		"missing":        `{"version":1,"type":"ping","streamId":""}`,
		"null":           `{"version":1,"type":"ping","streamId":"","payload":null}`,
		"alternate case": `{"Version":1,"type":"ping","streamId":"","payload":""}`,
		"blank stream":   `{"version":1,"type":"data","streamId":" ","payload":""}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseWireEnvelope([]byte(raw)); err == nil {
				t.Fatal("accepted an envelope outside the shared wire contract")
			}
		})
	}
}
