package relayclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type PairingCode struct {
	Code      string    `json:"code"`
	Role      string    `json:"role"`
	ExpiresAt time.Time `json:"expiresAt"`
}

func IssuePairing(ctx context.Context, identity Identity, role string) (PairingCode, error) {
	if identity.Role != "owner" {
		return PairingCode{}, errors.New("owner identity required")
	}
	if role != "agent" && role != "client" {
		return PairingCode{}, errors.New("pairing role must be agent or client")
	}
	body, _ := json.Marshal(map[string]string{"role": role})
	response, transport, err := identityRequest(ctx, identity, "/v1/pairing-codes", body)
	if transport != nil {
		defer transport.CloseIdleConnections()
	}
	if err != nil {
		return PairingCode{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxControlResponseBytes))
		return PairingCode{}, fmt.Errorf("relay rejected pairing request with HTTP %d", response.StatusCode)
	}
	var wire struct {
		Code      string `json:"code"`
		Role      string `json:"role"`
		ExpiresAt string `json:"expiresAt"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxControlResponseBytes+1))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&wire) != nil || requireJSONEOF(decoder) != nil || wire.Code == "" || wire.Role != role {
		return PairingCode{}, errors.New("relay returned an invalid pairing response")
	}
	expiresAt, err := time.Parse(time.RFC3339, wire.ExpiresAt)
	if err != nil {
		return PairingCode{}, errors.New("relay returned an invalid pairing expiry")
	}
	return PairingCode{Code: wire.Code, Role: wire.Role, ExpiresAt: expiresAt}, nil
}

func Revoke(ctx context.Context, identity Identity, serial string) error {
	if identity.Role != "owner" {
		return errors.New("owner identity required")
	}
	if !validSerial(serial) {
		return errors.New("invalid device serial")
	}
	body, _ := json.Marshal(map[string]string{"serial": strings.ToUpper(serial)})
	response, transport, err := identityRequest(ctx, identity, "/v1/revoke", body)
	if transport != nil {
		defer transport.CloseIdleConnections()
	}
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxControlResponseBytes))
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("relay rejected revocation with HTTP %d", response.StatusCode)
	}
	return nil
}

func identityRequest(ctx context.Context, identity Identity, path string, body []byte) (*http.Response, *http.Transport, error) {
	baseURL, err := validateRelayURL(identity.RelayURL)
	if err != nil {
		return nil, nil, err
	}
	client, transport, err := identityHTTPClient(identity)
	if err != nil {
		return nil, nil, err
	}
	client.Timeout = 15 * time.Second
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL.String()+path, bytes.NewReader(body))
	if err != nil {
		transport.CloseIdleConnections()
		return nil, nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		transport.CloseIdleConnections()
		return nil, nil, fmt.Errorf("relay control request failed: %w", err)
	}
	return response, transport, nil
}

func validSerial(serial string) bool {
	if serial == "" || serial != strings.TrimSpace(serial) || len(serial) > 64 {
		return false
	}
	for _, character := range serial {
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f') || (character >= 'A' && character <= 'F')) {
			return false
		}
	}
	return true
}
