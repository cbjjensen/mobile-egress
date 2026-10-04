package nodeservice

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

const activationAPIOrigin = "https://api.inevitableproxies.com"

var errActivation = errors.New("Inevitable activation is unavailable. Retry activation or check your connection.")

type activationState struct {
	RequestID           string    `json:"requestId"`
	PollSecret          string    `json:"pollSecret"`
	CodeVerifier        string    `json:"codeVerifier"`
	VerificationURI     string    `json:"verificationUri"`
	ExpiresAt           time.Time `json:"expiresAt"`
	PollIntervalSeconds int       `json:"pollIntervalSeconds"`
	DisplayName         string    `json:"displayName"`
	Status              string    `json:"status"`
}

// ActivationView is the only activation data crossing the local IPC boundary.
type ActivationView struct {
	State           string     `json:"state"`
	VerificationURI string     `json:"verificationUri,omitempty"`
	ExpiresAt       *time.Time `json:"expiresAt,omitempty"`
}

func activationView(a *activationState) ActivationView {
	if a == nil {
		return ActivationView{State: "inactive"}
	}
	v := ActivationView{State: a.Status}
	if a.Status == "pending" {
		expiry := a.ExpiresAt
		v.VerificationURI = a.VerificationURI
		v.ExpiresAt = &expiry
	}
	return v
}

func (m *Direct) activationRequest(ctx context.Context, path string, body any, result any) error {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	raw, err := json.Marshal(body)
	if err != nil {
		return errActivation
	}
	origin := m.activationOrigin
	if origin == "" {
		origin = activationAPIOrigin
	}
	if _, err := directHTTPSOrigin(origin); err != nil {
		return errActivation
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, origin+path, bytes.NewReader(raw))
	if err != nil {
		return errActivation
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: 8 * time.Second}
	if m.activationClient != nil {
		*client = *m.activationClient
		client.Timeout = 8 * time.Second
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(request)
	if err != nil {
		return errActivation
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusCreated {
		return errActivation
	}
	raw, err = io.ReadAll(io.LimitReader(response.Body, (16<<10)+1))
	if err != nil || len(raw) > 16<<10 {
		return errActivation
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if directStrictJSON(raw, &envelope) != nil || len(envelope.Data) == 0 || directStrictJSON(envelope.Data, result) != nil {
		return errActivation
	}
	return nil
}

func (m *Direct) StartHostedActivation(ctx context.Context, name string) (ActivationView, error) {
	name = strings.TrimSpace(name)
	if _, err := validateDirectConfiguration(DirectConfiguration{Transport: "hosted", Endpoint: "https://route.example", DisplayName: name}); err != nil {
		return ActivationView{}, err
	}
	if err := m.lockActivation(ctx); err != nil {
		return ActivationView{}, errActivation
	}
	defer m.opMu.Unlock()
	if err := m.ensureLocked(ctx); err != nil {
		return ActivationView{}, err
	}
	if m.state.Activation != nil && m.state.Activation.Status == "pending" && time.Now().Before(m.state.Activation.ExpiresAt) {
		m.startActivationPollingLocked()
		return activationView(m.state.Activation), nil
	}
	verifier, err := directRandom()
	if err != nil {
		return ActivationView{}, errActivation
	}
	digest := sha256.Sum256([]byte(verifier))
	var response struct {
		RequestID           string    `json:"requestId"`
		PollSecret          string    `json:"pollSecret"`
		VerificationURI     string    `json:"verificationUri"`
		ExpiresAt           time.Time `json:"expiresAt"`
		PollIntervalSeconds int       `json:"pollIntervalSeconds"`
	}
	body := struct {
		ClientID            string `json:"clientId"`
		DeviceName          string `json:"deviceName"`
		CodeChallenge       string `json:"codeChallenge"`
		CodeChallengeMethod string `json:"codeChallengeMethod"`
	}{m.state.ClientID, name, base64.RawURLEncoding.EncodeToString(digest[:]), "S256"}
	if err = m.activationRequest(ctx, "/api/mobile-egress/device-links", body, &response); err != nil {
		return ActivationView{}, err
	}
	if _, err = uuid.Parse(response.RequestID); err != nil {
		return ActivationView{}, errActivation
	}
	if !validActivationURL(response.VerificationURI) || !validProof(response.PollSecret) || !time.Now().Before(response.ExpiresAt) || response.ExpiresAt.After(time.Now().Add(24*time.Hour)) || response.PollIntervalSeconds < 5 || response.PollIntervalSeconds > 60 {
		return ActivationView{}, errActivation
	}
	verification, _ := url.Parse(response.VerificationURI)
	if verification.Query().Get("requestId") != response.RequestID {
		return ActivationView{}, errActivation
	}
	next := m.cloneLocked()
	next.Activation = &activationState{RequestID: response.RequestID, PollSecret: response.PollSecret, CodeVerifier: verifier, VerificationURI: response.VerificationURI, ExpiresAt: response.ExpiresAt, PollIntervalSeconds: response.PollIntervalSeconds, DisplayName: name, Status: "pending"}
	if err = m.saveLocked(ctx, next); err != nil {
		return ActivationView{}, err
	}
	m.refreshLocked()
	m.startActivationPollingLocked()
	return activationView(next.Activation), nil
}
func validProof(raw string) bool {
	b, err := base64.RawURLEncoding.DecodeString(raw)
	return err == nil && len(b) == 32 && base64.RawURLEncoding.EncodeToString(b) == raw
}

func (m *Direct) ResumeHostedActivation(ctx context.Context) (ActivationView, error) {
	if err := m.lockActivation(ctx); err != nil {
		return ActivationView{}, errActivation
	}
	defer m.opMu.Unlock()
	if err := m.ensureLocked(ctx); err != nil {
		return ActivationView{}, err
	}
	if m.state.Activation != nil && m.state.Activation.Status == "pending" {
		m.startActivationPollingLocked()
	}
	if m.state.Activation != nil && m.state.Activation.Status == "authorized" {
		if err := m.configureHostedLocked(ctx, m.state.Activation.DisplayName); err != nil {
			return ActivationView{}, err
		}
	}
	if m.state.Hosted != nil && m.state.Activation == nil {
		return ActivationView{State: "authorized"}, nil
	}
	return activationView(m.state.Activation), nil
}

func (m *Direct) StopHostedActivation() {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.stopActivationPollingLocked()
}
func (m *Direct) stopActivationPollingLocked() {
	if m.activationCancel != nil {
		m.activationCancel()
		m.activationCancel = nil
	}
}

func (m *Direct) startActivationPollingLocked() {
	if m.activationCancel != nil || m.state.Activation == nil || m.state.Activation.Status != "pending" {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.activationCancel = cancel
	id := m.state.Activation.RequestID
	interval := time.Duration(m.state.Activation.PollIntervalSeconds) * time.Second
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = m.pollActivation(ctx, id)
				m.opMu.Lock()
				terminal := m.state.Activation == nil || m.state.Activation.RequestID != id || m.state.Activation.Status != "pending"
				m.opMu.Unlock()
				if terminal {
					return
				}
			}
		}
	}()
}

func (m *Direct) pollActivation(ctx context.Context, id string) error {
	if err := m.lockActivation(ctx); err != nil {
		return errActivation
	}
	defer m.opMu.Unlock()
	a := m.state.Activation
	if a == nil || a.RequestID != id || a.Status != "pending" || ctx.Err() != nil {
		return nil
	}
	if !time.Now().Before(a.ExpiresAt) {
		next := m.cloneLocked()
		next.Activation.Status = "expired"
		next.Activation.CodeVerifier = ""
		next.Activation.PollSecret = ""
		err := m.saveLocked(ctx, next)
		m.stopActivationPollingLocked()
		m.refreshLocked()
		return err
	}
	var result struct {
		Status          string `json:"status"`
		DeviceID        string `json:"deviceId,omitempty"`
		DeviceToken     string `json:"deviceToken,omitempty"`
		ClientID        string `json:"clientId,omitempty"`
		RouteID         string `json:"routeId,omitempty"`
		BrokerEndpoint  string `json:"brokerEndpoint,omitempty"`
		GatewayHostname string `json:"gatewayHostname,omitempty"`
		GatewayPort     int    `json:"gatewayPort,omitempty"`
	}
	body := struct {
		RequestID    string `json:"requestId"`
		PollSecret   string `json:"pollSecret"`
		CodeVerifier string `json:"codeVerifier"`
	}{a.RequestID, a.PollSecret, a.CodeVerifier}
	if err := m.activationRequest(ctx, "/api/mobile-egress/device-links/poll", body, &result); err != nil {
		return err
	}
	if result.Status != "authorized" && (result.DeviceID != "" || result.DeviceToken != "" || result.ClientID != "" || result.RouteID != "" || result.BrokerEndpoint != "" || result.GatewayHostname != "" || result.GatewayPort != 0) {
		return errActivation
	}
	switch result.Status {
	case "pending":
		return nil
	case "expired", "denied":
		next := m.cloneLocked()
		next.Activation.Status = result.Status
		next.Activation.PollSecret = ""
		next.Activation.CodeVerifier = ""
		err := m.saveLocked(ctx, next)
		m.stopActivationPollingLocked()
		m.refreshLocked()
		return err
	case "authorized":
		if result.ClientID != m.state.ClientID || !strings.HasPrefix(result.DeviceToken, "med1.") || !validProof(strings.TrimPrefix(result.DeviceToken, "med1.")) || validateBroker(result.BrokerEndpoint) != nil || result.GatewayPort != 443 {
			return errActivation
		}
		if _, err := uuid.Parse(result.DeviceID); err != nil {
			return errActivation
		}
		if _, err := uuid.Parse(result.RouteID); err != nil {
			return errActivation
		}
		route, err := directHTTPSOrigin("https://" + result.GatewayHostname)
		if err != nil || route.Host != result.GatewayHostname || strings.Contains(result.GatewayHostname, ":") {
			return errActivation
		}
		next := m.cloneLocked()
		name := a.DisplayName
		next.Hosted = &hostedState{DeviceID: result.DeviceID, DeviceToken: result.DeviceToken, BrokerEndpoint: result.BrokerEndpoint, GatewayHostname: result.GatewayHostname, GatewayPort: result.GatewayPort}
		next.Activation.Status = "authorized"
		next.Activation.CodeVerifier = ""
		next.Activation.PollSecret = ""
		// Save the one-time exchange before applying mode so restart can recover it.
		if err := m.saveLocked(ctx, next); err != nil {
			return err
		}
		m.stopActivationPollingLocked()
		return m.configureHostedLocked(ctx, name)
	default:
		return errActivation
	}
}

func (m *Direct) CancelHostedActivation(ctx context.Context) error {
	if err := m.lockActivation(ctx); err != nil {
		return errActivation
	}
	defer m.opMu.Unlock()
	if err := m.ensureLocked(ctx); err != nil {
		return err
	}
	m.stopActivationPollingLocked()
	a := m.state.Activation
	if a == nil {
		return nil
	}
	if a.Status == "pending" {
		body := struct {
			RequestID    string `json:"requestId"`
			PollSecret   string `json:"pollSecret"`
			CodeVerifier string `json:"codeVerifier"`
		}{a.RequestID, a.PollSecret, a.CodeVerifier}
		var response struct {
			Success bool `json:"success"`
		}
		// Persist cancellation even if the server is unreachable; no local poll may resume.
		_ = m.activationRequest(ctx, "/api/mobile-egress/device-links/cancel", body, &response)
	}
	next := m.cloneLocked()
	next.Activation = nil
	if err := m.saveLocked(context.WithoutCancel(ctx), next); err != nil {
		return err
	}
	m.refreshLocked()
	return nil
}

func (m *Direct) lockActivation(ctx context.Context) error {
	for !m.opMu.TryLock() {
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	if err := ctx.Err(); err != nil {
		m.opMu.Unlock()
		return err
	}
	return nil
}
