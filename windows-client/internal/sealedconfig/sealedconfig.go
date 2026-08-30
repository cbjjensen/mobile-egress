// Package sealedconfig encrypts controller-to-node configuration without
// exposing its contents to Systems Manager command input, output, or logs.
package sealedconfig

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	Version            = 1
	maximumPlaintext   = 1 << 20
	maximumCiphertext  = maximumPlaintext + 64
	keyDerivationLabel = "mobile-egress/node-config/v1"
)

type Envelope struct {
	Version            int    `json:"version"`
	EphemeralPublicKey string `json:"ephemeralPublicKey"`
	Nonce              string `json:"nonce"`
	Ciphertext         string `json:"ciphertext"`
}

func GenerateKey() (privateKey []byte, publicKey string, err error) {
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, "", fmt.Errorf("generate node configuration key: %w", err)
	}
	return append([]byte(nil), key.Bytes()...), base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()), nil
}

func Seal(recipientPublicKey string, plaintext []byte) (Envelope, error) {
	if len(plaintext) == 0 || len(plaintext) > maximumPlaintext {
		return Envelope{}, errors.New("node configuration plaintext is missing or too large")
	}
	recipient, recipientBytes, err := parsePublicKey(recipientPublicKey)
	if err != nil {
		return Envelope{}, err
	}
	ephemeral, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return Envelope{}, fmt.Errorf("generate ephemeral configuration key: %w", err)
	}
	shared, err := ephemeral.ECDH(recipient)
	if err != nil {
		return Envelope{}, errors.New("derive node configuration key")
	}
	defer clear(shared)
	ephemeralBytes := ephemeral.PublicKey().Bytes()
	aead, err := configurationAEAD(shared, ephemeralBytes, recipientBytes)
	if err != nil {
		return Envelope{}, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return Envelope{}, fmt.Errorf("generate node configuration nonce: %w", err)
	}
	aad := associatedData(ephemeralBytes, recipientBytes)
	ciphertext := aead.Seal(nil, nonce, plaintext, aad)
	return Envelope{
		Version: Version, EphemeralPublicKey: base64.RawURLEncoding.EncodeToString(ephemeralBytes),
		Nonce: base64.RawURLEncoding.EncodeToString(nonce), Ciphertext: base64.RawURLEncoding.EncodeToString(ciphertext),
	}, nil
}

func Open(privateKey []byte, envelope Envelope) ([]byte, error) {
	if envelope.Version != Version {
		return nil, errors.New("unsupported node configuration version")
	}
	if len(privateKey) != 32 {
		return nil, errors.New("invalid node configuration private key")
	}
	recipient, err := ecdh.X25519().NewPrivateKey(privateKey)
	if err != nil {
		return nil, errors.New("invalid node configuration private key")
	}
	ephemeral, ephemeralBytes, err := parsePublicKey(envelope.EphemeralPublicKey)
	if err != nil {
		return nil, errors.New("invalid ephemeral configuration key")
	}
	nonce, err := decodeField(envelope.Nonce, 32)
	if err != nil {
		return nil, errors.New("invalid node configuration nonce")
	}
	ciphertext, err := decodeField(envelope.Ciphertext, maximumCiphertext)
	if err != nil || len(ciphertext) < 16 {
		return nil, errors.New("invalid node configuration ciphertext")
	}
	shared, err := recipient.ECDH(ephemeral)
	if err != nil {
		return nil, errors.New("derive node configuration key")
	}
	defer clear(shared)
	recipientBytes := recipient.PublicKey().Bytes()
	aead, err := configurationAEAD(shared, ephemeralBytes, recipientBytes)
	if err != nil {
		return nil, err
	}
	if len(nonce) != aead.NonceSize() {
		return nil, errors.New("invalid node configuration nonce")
	}
	plaintext, err := aead.Open(nil, nonce, ciphertext, associatedData(ephemeralBytes, recipientBytes))
	if err != nil {
		return nil, errors.New("node configuration authentication failed")
	}
	if len(plaintext) == 0 || len(plaintext) > maximumPlaintext {
		clear(plaintext)
		return nil, errors.New("node configuration plaintext is invalid")
	}
	return plaintext, nil
}

func (envelope Envelope) Fingerprint() (string, error) {
	if envelope.Version != Version {
		return "", errors.New("unsupported node configuration version")
	}
	ephemeral, err := decodeField(envelope.EphemeralPublicKey, 32)
	if err != nil || len(ephemeral) != 32 {
		return "", errors.New("invalid ephemeral configuration key")
	}
	nonce, err := decodeField(envelope.Nonce, 32)
	if err != nil || len(nonce) != 12 {
		return "", errors.New("invalid node configuration nonce")
	}
	ciphertext, err := decodeField(envelope.Ciphertext, maximumCiphertext)
	if err != nil || len(ciphertext) < 16 {
		return "", errors.New("invalid node configuration ciphertext")
	}
	hash := sha256.New()
	_, _ = hash.Write(associatedData(ephemeral, nil))
	_, _ = hash.Write(nonce)
	_, _ = hash.Write(ciphertext)
	return base64.RawURLEncoding.EncodeToString(hash.Sum(nil)), nil
}

func configurationAEAD(shared, ephemeralPublic, recipientPublic []byte) (cipher.AEAD, error) {
	key, err := hkdf.Key(sha256.New, shared, nil, keyDerivationLabel+"/"+
		base64.RawURLEncoding.EncodeToString(ephemeralPublic)+"/"+base64.RawURLEncoding.EncodeToString(recipientPublic), 32)
	if err != nil {
		return nil, fmt.Errorf("derive node configuration key: %w", err)
	}
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create node configuration cipher: %w", err)
	}
	return cipher.NewGCM(block)
}

func associatedData(ephemeralPublic, recipientPublic []byte) []byte {
	value := make([]byte, 4, 4+len(keyDerivationLabel)+len(ephemeralPublic)+len(recipientPublic))
	binary.BigEndian.PutUint32(value, Version)
	value = append(value, keyDerivationLabel...)
	value = append(value, ephemeralPublic...)
	value = append(value, recipientPublic...)
	return value
}

func parsePublicKey(encoded string) (*ecdh.PublicKey, []byte, error) {
	raw, err := decodeField(encoded, 32)
	if err != nil || len(raw) != 32 {
		return nil, nil, errors.New("invalid node configuration public key")
	}
	key, err := ecdh.X25519().NewPublicKey(raw)
	if err != nil {
		return nil, nil, errors.New("invalid node configuration public key")
	}
	return key, raw, nil
}

func decodeField(value string, maximum int) ([]byte, error) {
	if value == "" || len(value) > base64.RawURLEncoding.EncodedLen(maximum) {
		return nil, errors.New("sealed configuration field is missing or too large")
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(value)
	if err != nil || len(decoded) > maximum {
		return nil, errors.New("sealed configuration field is invalid")
	}
	return decoded, nil
}
