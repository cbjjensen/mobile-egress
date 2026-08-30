package sealedconfig

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestSealOpenRoundTripAndStableFingerprint(t *testing.T) {
	t.Parallel()

	privateKey, publicKey, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	plaintext := []byte(`{"relayUrl":"https://relay.example.ts.net:8443","secret":"not logged"}`)
	envelope, err := Seal(publicKey, plaintext)
	if err != nil {
		t.Fatal(err)
	}
	if envelope.Version != Version || envelope.EphemeralPublicKey == "" || envelope.Nonce == "" || envelope.Ciphertext == "" {
		t.Fatalf("sealed envelope is incomplete: %#v", envelope)
	}
	opened, err := Open(privateKey, envelope)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(opened, plaintext) {
		t.Fatalf("opened plaintext = %q, want %q", opened, plaintext)
	}
	first, err := envelope.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Envelope
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	second, err := decoded.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	if first == "" || first != second {
		t.Fatalf("fingerprints = %q/%q", first, second)
	}
}

func TestOpenRejectsTamperedMalformedWrongKeyAndUnsupportedVersion(t *testing.T) {
	t.Parallel()

	privateKey, publicKey, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	wrongPrivateKey, _, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := Seal(publicKey, []byte("secret configuration"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(wrongPrivateKey, envelope); err == nil {
		t.Fatal("Open() accepted an envelope for a different node key")
	}

	tests := map[string]func(Envelope) Envelope{
		"version":       func(value Envelope) Envelope { value.Version++; return value },
		"ephemeral key": func(value Envelope) Envelope { value.EphemeralPublicKey = strings.Repeat("A", 43); return value },
		"nonce":         func(value Envelope) Envelope { value.Nonce = strings.Repeat("A", 16); return value },
		"ciphertext": func(value Envelope) Envelope {
			replacement := byte('A')
			if value.Ciphertext[0] == replacement {
				replacement = 'B'
			}
			value.Ciphertext = string(replacement) + value.Ciphertext[1:]
			return value
		},
		"base64": func(value Envelope) Envelope { value.Ciphertext = "***"; return value },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Open(privateKey, mutate(envelope)); err == nil {
				t.Fatalf("Open() accepted tampered %s", name)
			}
		})
	}
}

func TestSealRejectsMalformedRecipientKey(t *testing.T) {
	t.Parallel()

	for _, publicKey := range []string{"", "***", "AA"} {
		if _, err := Seal(publicKey, []byte("config")); err == nil {
			t.Fatalf("Seal() accepted public key %q", publicKey)
		}
	}
}
