package protocol

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestParseEnvelopeAcceptsVersionOneMessages(t *testing.T) {
	t.Parallel()

	payload := base64.RawURLEncoding.EncodeToString([]byte("hello"))
	for _, raw := range []string{
		`{"version":1,"type":"open","streamId":"stream-1","payload":""}`,
		`{"version":1,"type":"opened","streamId":"stream-1","payload":""}`,
		`{"version":1,"type":"rejected","streamId":"stream-1","payload":""}`,
		`{"version":1,"type":"data","streamId":"stream-1","payload":"` + payload + `"}`,
		`{"version":1,"type":"close","streamId":"stream-1","payload":""}`,
		`{"version":1,"type":"ping","streamId":"","payload":""}`,
		`{"version":1,"type":"pong","streamId":"","payload":""}`,
	} {
		raw := raw
		t.Run(raw, func(t *testing.T) {
			t.Parallel()

			envelope, err := ParseEnvelope([]byte(raw))
			if err != nil {
				t.Fatalf("ParseEnvelope() returned an error: %v", err)
			}
			if envelope.Version != Version1 {
				t.Fatalf("ParseEnvelope() returned version %d, want %d", envelope.Version, Version1)
			}
		})
	}
}

func TestParseEnvelopeRejectsInvalidEnvelopes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
	}{
		{name: "unknown version", raw: `{"version":2,"type":"ping","streamId":"","payload":""}`},
		{name: "unknown type", raw: `{"version":1,"type":"unknown","streamId":"stream-1","payload":""}`},
		{name: "missing stream ID", raw: `{"version":1,"type":"data","streamId":"","payload":""}`},
		{name: "stream ID on ping", raw: `{"version":1,"type":"ping","streamId":"stream-1","payload":""}`},
		{name: "invalid payload", raw: `{"version":1,"type":"data","streamId":"stream-1","payload":"not+/base64"}`},
		{name: "malformed JSON", raw: `{`},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if _, err := ParseEnvelope([]byte(test.raw)); err == nil {
				t.Fatal("ParseEnvelope() accepted an invalid envelope")
			}
		})
	}
}

func TestParseEnvelopeRejectsPayloadOverOneMiB(t *testing.T) {
	t.Parallel()

	payload := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("a", MaxDecodedPayloadBytes+1)))
	raw := `{"version":1,"type":"data","streamId":"stream-1","payload":"` + payload + `"}`

	if _, err := ParseEnvelope([]byte(raw)); err == nil {
		t.Fatal("ParseEnvelope() accepted a payload larger than one MiB")
	}
}

func TestParseEnvelopeAcceptsDataAtThirtyTwoKiB(t *testing.T) {
	t.Parallel()

	payload := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("a", 32<<10)))
	raw := `{"version":1,"type":"data","streamId":"stream-1","payload":"` + payload + `"}`

	if _, err := ParseEnvelope([]byte(raw)); err != nil {
		t.Fatalf("ParseEnvelope() rejected a 32 KiB data payload: %v", err)
	}
}

func TestParseEnvelopeRejectsDataOverThirtyTwoKiB(t *testing.T) {
	t.Parallel()

	payload := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("a", 32<<10+1)))
	raw := `{"version":1,"type":"data","streamId":"stream-1","payload":"` + payload + `"}`

	if _, err := ParseEnvelope([]byte(raw)); err == nil {
		t.Fatal("ParseEnvelope() accepted a data payload larger than 32 KiB")
	}
}

func TestParseEnvelopePreservesLargerNonDataPayloadLimit(t *testing.T) {
	t.Parallel()

	payload := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("a", 32<<10+1)))
	raw := `{"version":1,"type":"open","streamId":"stream-1","payload":"` + payload + `"}`

	if _, err := ParseEnvelope([]byte(raw)); err != nil {
		t.Fatalf("ParseEnvelope() applied the data limit to a non-data payload: %v", err)
	}
}

func TestParseEnvelopeRejectsConcatenatedJSONValues(t *testing.T) {
	t.Parallel()

	raw := `{"version":1,"type":"ping","streamId":"","payload":""}null`
	if _, err := ParseEnvelope([]byte(raw)); err == nil {
		t.Fatal("ParseEnvelope() accepted concatenated JSON values")
	}
}

func TestParseEnvelopeRejectsAlternateCasedFieldNames(t *testing.T) {
	t.Parallel()

	raw := `{"Version":1,"type":"ping","streamId":"","payload":""}`
	if _, err := ParseEnvelope([]byte(raw)); err == nil {
		t.Fatal("ParseEnvelope() accepted an alternate-cased field name")
	}
}

func TestParseEnvelopeRejectsDuplicateRequiredFields(t *testing.T) {
	t.Parallel()

	raw := `{"version":2,"version":1,"type":"ping","streamId":"","payload":""}`
	if _, err := ParseEnvelope([]byte(raw)); err == nil {
		t.Fatal("ParseEnvelope() accepted duplicate required fields")
	}
}
