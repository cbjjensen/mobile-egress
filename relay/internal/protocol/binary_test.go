package protocol

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseBinaryDataLiteral(t *testing.T) {
	raw := []byte{2, 4, 0, 3, 'a', '_', '1', 0, 255, 128}
	envelope, err := ParseEnvelope(raw)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := envelope.DecodePayload()
	if err != nil || envelope.Type != TypeData || envelope.StreamID != "a_1" || !bytes.Equal(payload, []byte{0, 255, 128}) {
		t.Fatalf("unexpected binary envelope: %+v, %v", envelope, err)
	}
}

func TestNegotiatedWriterPreservesLegacyStreamIDs(t *testing.T) {
	for _, id := range []string{"legacy:1", strings.Repeat("a", 129)} {
		legacy := Envelope{Version: 1, Type: TypeData, StreamID: id, Payload: "aGVsbG8"}
		raw, err := legacy.MarshalForPeer(true)
		if err != nil {
			t.Fatal(err)
		}
		if len(raw) == 0 || raw[0] != '{' {
			t.Fatal("unrepresentable legacy ID was not kept in JSON")
		}
		got, err := ParseEnvelope(raw)
		if err != nil || got.StreamID != id {
			t.Fatalf("legacy ID changed: %v", err)
		}
	}
}

func TestLegacyPayloadMetricIgnoresAcceptedLineBreaks(t *testing.T) {
	envelope, err := ParseEnvelope([]byte(`{"version":1,"type":"data","streamId":"s","payload":"aGVs\nbG8"}`))
	if err != nil {
		t.Fatal(err)
	}
	if envelope.PayloadBytes() != 5 || envelope.RetainedPayloadBytes() != 8 {
		t.Fatalf("wrong decoded/retained byte counts: %d/%d", envelope.PayloadBytes(), envelope.RetainedPayloadBytes())
	}
}

func TestRejectMalformedBinaryData(t *testing.T) {
	for _, raw := range [][]byte{
		{2}, {2, 4, 0, 0}, {2, 4, 0, 2, 'a'}, {2, 3, 0, 1, 'a'}, {2, 4, 0, 1, ' '},
		append([]byte{2, 4, 0, 1, 'a'}, make([]byte, 32769)...),
		append([]byte{2, 4, 0, 129}, bytes.Repeat([]byte{'a'}, 129)...),
	} {
		if _, err := ParseEnvelope(raw); err == nil {
			t.Fatalf("accepted invalid binary frame of %d bytes", len(raw))
		}
	}
}

func TestBinaryBoundaryAndLegacyTranslation(t *testing.T) {
	for _, size := range []int{0, 32768} {
		payload := bytes.Repeat([]byte{255}, size)
		original := Envelope{Version: 1, Type: TypeData, StreamID: "s", Data: payload}
		binary, err := original.MarshalForPeer(true)
		if err != nil {
			t.Fatal(err)
		}
		if len(binary) != size+5 {
			t.Fatalf("binary wire size %d, want %d", len(binary), size+5)
		}
		parsed, err := ParseEnvelope(binary)
		if err != nil {
			t.Fatal(err)
		}
		if parsed.Data == nil || parsed.RetainedPayloadBytes() != size || parsed.PayloadBytes() != size {
			t.Fatal("binary payload accounting mismatch")
		}
		legacy, err := parsed.MarshalForPeer(false)
		if err != nil {
			t.Fatal(err)
		}
		if len(legacy) == 0 || legacy[0] != '{' {
			t.Fatal("legacy translation is not JSON")
		}
		roundtrip, err := ParseEnvelope(legacy)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := roundtrip.DecodePayload()
		if err != nil || !bytes.Equal(decoded, payload) {
			t.Fatal("translation corrupted payload")
		}
	}
}
