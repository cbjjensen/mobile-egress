package nodeservice

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"net"
	"net/url"
	"strconv"
	"strings"

	"mobile-egress/internal/hostedgateway"
)

type hostedState struct {
	DeviceID        string `json:"deviceId"`
	DeviceToken     string `json:"deviceToken"`
	BrokerEndpoint  string `json:"brokerEndpoint"`
	GatewayHostname string `json:"gatewayHostname"`
	GatewayPort     int    `json:"gatewayPort"`
}

func effectiveTransport(mode string) string {
	if mode == "hosted" {
		return mode
	}
	return "direct"
}
func wireTransport(mode string) string {
	if mode == "hosted" {
		return mode
	}
	return ""
}

// The route hostname is the inner phone TLS origin; the broker is the outer
// system-trusted attachment. Neither endpoint carries account credentials.
func (m *Direct) configureHostedLocked(ctx context.Context, name string) error {
	if m.state.Hosted == nil {
		return errors.New("Activate Inevitable first.")
	}
	h := m.state.Hosted
	endpoint := "https://" + net.JoinHostPort(h.GatewayHostname, strconv.Itoa(h.GatewayPort))
	config, err := validateDirectConfiguration(DirectConfiguration{Transport: "hosted", Endpoint: endpoint, DisplayName: name})
	if err != nil {
		return err
	}
	next := m.cloneLocked()
	next.Activation = nil
	changed := next.Configuration == nil || next.Configuration.Endpoint != config.Endpoint || effectiveTransport(next.Configuration.Transport) != "hosted"
	if changed {
		if next.Generation >= 1<<63-1 {
			return errors.New("Endpoint generation exhausted.")
		}
		next.Generation++
		next.Invitation = nil
		if err := directServerCertificate(next, config.Endpoint); err != nil {
			return errDirectStorage
		}
	}
	next.Configuration = &config
	tc, err := directTLS(next)
	if err != nil {
		return err
	}
	var replacement net.Listener
	previousEpoch := m.gatewayEpoch
	if m.runCtx != nil {
		replacement, err = m.hostedListenerLocked(next)
		if err != nil {
			return err
		}
	}
	if err = m.saveLocked(ctx, next); err != nil {
		m.gatewayEpoch = previousEpoch
		if replacement != nil {
			replacement.Close()
		}
		return err
	}
	m.notifyEndpointUpdateLocked()
	m.tlsConfig.Store(tc)
	if replacement != nil {
		m.replaceListenerLocked(replacement)
	}
	m.refreshLocked()
	return nil
}
func (m *Direct) hostedListenerLocked(state *directState) (net.Listener, error) {
	if state.Hosted == nil {
		return nil, errors.New("Hosted activation is unavailable.")
	}
	broker, err := directHTTPSOrigin(state.Hosted.BrokerEndpoint)
	if err != nil {
		return nil, err
	}
	port := broker.Port()
	if port == "" {
		port = "443"
	}
	generation := state.Generation
	m.gatewayEpoch++
	epoch := m.gatewayEpoch
	connector, err := hostedgateway.New(hostedgateway.Config{Address: net.JoinHostPort(broker.Hostname(), port), ServerName: broker.Hostname(), DeviceID: state.Hosted.DeviceID, ClientID: state.ClientID, Token: state.Hosted.DeviceToken, OnStatus: func(s hostedgateway.Status) {
		m.recordGatewayStatus(generation, epoch, s)
	}})
	if err != nil {
		return nil, errors.New("Hosted gateway configuration is invalid.")
	}
	return connector, nil
}

func (m *Direct) recordGatewayStatus(generation, epoch uint64, s hostedgateway.Status) {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if m.gatewayEpoch != epoch || m.state == nil || m.state.Generation != generation || m.state.Configuration == nil || m.state.Configuration.Transport != "hosted" {
		return
	}
	switch s.State {
	case "connected", "connecting", "disconnected":
		m.gatewayState = s.State
	default:
		m.gatewayState = "disconnected"
	}
	if s.Reason == "gateway activation rejected" {
		m.gatewayState = "authorization_rejected"
	}
	m.refreshLocked()
}

func validateBroker(raw string) error {
	u, err := directHTTPSOrigin(raw)
	if err != nil {
		return err
	}
	if u.Port() != "" && u.Port() != "443" {
		return errDirectInvalid
	}
	if net.ParseIP(u.Hostname()) != nil || len(u.Hostname()) < 3 {
		return errDirectInvalid
	}
	return nil
}
func validActivationURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || (u.Hostname() != "inevitableproxies.com" && u.Hostname() != "www.inevitableproxies.com") || (u.Port() != "" && u.Port() != "443") || u.Fragment != "" || u.Path != "/mobile-egress/approve" || u.RawPath != "" {
		return false
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(q) != 1 || len(q["requestId"]) != 1 {
		return false
	}
	_, err = uuid.Parse(q.Get("requestId"))
	return err == nil
}

// ValidActivationURL validates the fixed approved browser endpoint for the GUI.
func ValidActivationURL(raw string) bool { return validActivationURL(raw) }

func validateSavedHosted(h *hostedState) error {
	if _, err := uuid.Parse(h.DeviceID); err != nil {
		return errDirectStorage
	}
	if !strings.HasPrefix(h.DeviceToken, "med1.") || !validProof(strings.TrimPrefix(h.DeviceToken, "med1.")) || validateBroker(h.BrokerEndpoint) != nil || h.GatewayPort != 443 {
		return errDirectStorage
	}
	u, err := directHTTPSOrigin("https://" + h.GatewayHostname)
	if err != nil || u.Host != h.GatewayHostname || net.ParseIP(u.Hostname()) != nil {
		return errDirectStorage
	}
	return nil
}
func validateSavedActivation(a *activationState) error {
	if _, err := uuid.Parse(a.RequestID); err != nil {
		return errDirectStorage
	}
	if a.Status != "pending" && a.Status != "authorized" && a.Status != "expired" && a.Status != "denied" {
		return errDirectStorage
	}
	if a.PollIntervalSeconds < 5 || a.PollIntervalSeconds > 60 || !validActivationURL(a.VerificationURI) {
		return errDirectStorage
	}
	if a.Status == "pending" && (!validProof(a.PollSecret) || !validProof(a.CodeVerifier)) {
		return errDirectStorage
	}
	return nil
}

// The established authenticated tunnel can carry the signed update even after
// its old listener is closed. The tunnel negotiates support; QR remains recovery.
func (m *Direct) notifyEndpointUpdateLocked() {
	if m.state.Pairing == nil || m.state.Pairing.Revoked || m.state.AcknowledgedGeneration >= m.state.Generation {
		return
	}
	bundle, err := directEndpointBundle(m.state)
	if err != nil {
		return
	}
	if sender, ok := m.opener.current().(interface{ SendEndpointUpdate(string) error }); ok {
		_ = sender.SendEndpointUpdate(bundle)
	}
}
