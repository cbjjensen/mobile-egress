package nodeservice

import (
	"bytes"
	"context"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"mobile-egress/windows-client/internal/relayclient"
)

func directReply(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func directFailure(w http.ResponseWriter, status int, code string) {
	directReply(w, status, map[string]string{"error": code})
}
func directBody(w http.ResponseWriter, r *http.Request, value any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, directBundleLimit)
	raw, err := io.ReadAll(r.Body)
	if err != nil || directStrictJSON(raw, value) != nil {
		directFailure(w, 400, "invalid_request")
		return false
	}
	return true
}
func (m *Direct) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v2/direct/enroll", m.handleDirectEnroll)
	mux.HandleFunc("POST /v2/direct/ack", m.handleDirectAck)
	mux.HandleFunc("GET /v2/direct/session", m.handleDirectSession)
	mux.HandleFunc("GET /v2/direct/config", m.handleDirectConfig)
	mux.HandleFunc("POST /v2/direct/renew", m.handleDirectRenew)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" && !(r.URL.Path == "/v2/direct/session" && r.URL.RawQuery == "transport=2") {
			directFailure(w, 400, "invalid_request")
			return
		}
		select {
		case m.enrollSlots <- struct{}{}:
			defer func() { <-m.enrollSlots }()
		default:
			directFailure(w, 503, "busy")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		mux.ServeHTTP(w, r.WithContext(ctx))
	})
}
func (m *Direct) handleDirectEnroll(w http.ResponseWriter, r *http.Request) {
	var request struct {
		ClientID     string `json:"clientId"`
		InvitationID string `json:"invitationId"`
		Code         string `json:"code"`
		Role         string `json:"role"`
		CSRPEM       string `json:"csrPem"`
	}
	if !directBody(w, r, &request) {
		return
	}
	csr, publicKey, err := directCSR(request.CSRPEM)
	if err != nil {
		directFailure(w, 400, "invalid_request")
		return
	}
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if m.ensureLocked(r.Context()) != nil {
		directFailure(w, 503, "storage_unavailable")
		return
	}
	if m.denied.Load() {
		directFailure(w, 503, "storage_unavailable")
		return
	}
	state := m.state
	invitation := state.Invitation
	if request.ClientID != state.ClientID || request.Role != "agent" || invitation == nil || request.InvitationID != invitation.InvitationID || subtle.ConstantTimeCompare([]byte(request.Code), []byte(invitation.Capability)) != 1 {
		directFailure(w, 401, "invitation_invalid")
		return
	}
	if state.Pairing != nil {
		if state.Pairing.Revoked || !bytes.Equal(publicKey, state.Pairing.PublicKey) {
			directFailure(w, 409, "invitation_used")
			return
		}
		directReply(w, 201, state.Pairing.Identity)
		return
	}
	if !time.Now().Before(invitation.ExpiresAt) {
		directFailure(w, 410, "invitation_expired")
		return
	}
	next := m.cloneLocked()
	id, err := directID()
	if err != nil {
		directFailure(w, 503, "unavailable")
		return
	}
	identity, err := directIssue(next, id, csr)
	if err != nil {
		directFailure(w, 503, "unavailable")
		return
	}
	next.Pairing = &directPairing{ID: id, PublicKey: publicKey, Identity: identity}
	if m.saveLocked(r.Context(), next) != nil {
		directFailure(w, 503, "storage_unavailable")
		return
	}
	m.refreshLocked()
	directReply(w, 201, identity)
}

var errDirectUnauthorized = errors.New("Direct phone authentication rejected.")

func directAdmissionFailure(w http.ResponseWriter, err error) {
	if errors.Is(err, errDirectUnauthorized) {
		directFailure(w, 401, "unauthorized")
	} else {
		directFailure(w, 503, "storage_unavailable")
	}
}

// Must run under opMu. A valid CA signature never substitutes for durable
// pairing/serial admission; revocation and replacement serialize here.
func (m *Direct) admitLocked(r *http.Request, requireAck bool) (string, error) {
	if err := m.ensureLocked(r.Context()); err != nil {
		return "", err
	}
	if m.denied.Load() {
		return "", errDirectStorage
	}
	if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 || len(r.TLS.PeerCertificates) == 0 {
		return "", errDirectUnauthorized
	}
	pairing := m.state.Pairing
	if pairing == nil || pairing.Revoked || (requireAck && !pairing.Acknowledged) {
		return "", errDirectUnauthorized
	}
	cert := r.TLS.PeerCertificates[0]
	now := time.Now()
	if now.Before(cert.NotBefore) || !now.Before(cert.NotAfter) {
		return "", errDirectUnauthorized
	}
	serial := strings.ToUpper(cert.SerialNumber.Text(16))
	if serial != pairing.Identity.Serial && serial != pairing.PreviousSerial {
		return "", errDirectUnauthorized
	}
	public, err := x509.MarshalPKIXPublicKey(cert.PublicKey)
	if err != nil || !bytes.Equal(public, pairing.PublicKey) {
		return "", errDirectUnauthorized
	}
	return serial, nil
}
func (m *Direct) handleDirectAck(w http.ResponseWriter, r *http.Request) {
	var request struct {
		ClientID   string `json:"clientId"`
		PairingID  string `json:"pairingId"`
		Generation uint64 `json:"generation"`
	}
	if !directBody(w, r, &request) {
		return
	}
	m.opMu.Lock()
	defer m.opMu.Unlock()
	serial, err := m.admitLocked(r, false)
	if err != nil {
		directAdmissionFailure(w, err)
		return
	}
	if request.ClientID != m.state.ClientID || request.PairingID != m.state.Pairing.ID || request.Generation == 0 || request.Generation > m.state.Generation {
		directFailure(w, 409, "configuration_mismatch")
		return
	}
	requestOrigin, err := directHTTPSOrigin("https://" + r.Host)
	if err != nil || strings.ContainsAny(r.Host, "/?#") {
		directFailure(w, 409, "endpoint_mismatch")
		return
	}
	// Advancing to the latest endpoint requires an ACK addressed to that origin.
	if request.Generation > m.state.AcknowledgedGeneration && request.Generation == m.state.Generation {
		origin, err := directHTTPSOrigin(m.state.Configuration.Endpoint)
		if err != nil || requestOrigin.Host != origin.Host {
			directFailure(w, 409, "endpoint_mismatch")
			return
		}
	}
	next := m.cloneLocked()
	next.Pairing.Acknowledged = true
	next.MigrationRequired = false
	var updatedTLS *tls.Config
	if request.Generation > next.AcknowledgedGeneration {
		// The authenticated phone's ACK identifies its reached authority. It
		// must already be covered by our signed server certificate. Persist it
		// separately from desired configuration so repeated changes never remove
		// the last acknowledged hostname before the phone can poll config.
		current := m.tlsConfig.Load()
		if current == nil || current.Certificates[0].Leaf == nil || current.Certificates[0].Leaf.VerifyHostname(requestOrigin.Hostname()) != nil {
			directFailure(w, 409, "endpoint_mismatch")
			return
		}
		next.AcknowledgedGeneration = request.Generation
		next.AcknowledgedEndpoint = requestOrigin.String()
		if directServerCertificate(next, next.Configuration.Endpoint) != nil {
			directFailure(w, 503, "unavailable")
			return
		}
		updatedTLS, err = directTLS(next)
		if err != nil {
			directFailure(w, 503, "unavailable")
			return
		}
	}
	retire := next.Pairing.PreviousSerial != "" && serial == next.Pairing.Identity.Serial
	if retire {
		next.Pairing.PreviousSerial = ""
	}
	if m.saveLocked(r.Context(), next) != nil {
		directFailure(w, 503, "storage_unavailable")
		return
	}
	if updatedTLS != nil {
		m.tlsConfig.Store(updatedTLS)
	}
	if retire && m.activeSerial != serial {
		m.opener.swap(nil)
		m.activeSerial = ""
	}
	m.refreshLocked()
	directReply(w, 200, map[string]string{"status": "paired"})
}
func (m *Direct) handleDirectConfig(w http.ResponseWriter, r *http.Request) {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if _, err := m.admitLocked(r, false); err != nil {
		directAdmissionFailure(w, err)
		return
	}
	update := ""
	var err error
	if m.state.AcknowledgedGeneration < m.state.Generation {
		update, err = directEndpointBundle(m.state)
		if err != nil {
			directFailure(w, 503, "unavailable")
			return
		}
	}
	directReply(w, 200, map[string]string{"update": update})
}
func (m *Direct) handleDirectRenew(w http.ResponseWriter, r *http.Request) {
	var request struct {
		ClientID  string `json:"clientId"`
		PairingID string `json:"pairingId"`
		CSRPEM    string `json:"csrPem"`
	}
	if !directBody(w, r, &request) {
		return
	}
	csr, publicKey, err := directCSR(request.CSRPEM)
	if err != nil {
		directFailure(w, 400, "invalid_request")
		return
	}
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if _, err := m.admitLocked(r, true); err != nil {
		directAdmissionFailure(w, err)
		return
	}
	if request.ClientID != m.state.ClientID || request.PairingID != m.state.Pairing.ID || !bytes.Equal(publicKey, m.state.Pairing.PublicKey) {
		directFailure(w, 409, "identity_mismatch")
		return
	}
	if m.state.Pairing.PreviousSerial != "" {
		// Endpoint acknowledgement can advance while the phone still uses its
		// previous certificate after a lost renewal response. Reuse the exact
		// issued identity, but describe the phone's current acknowledged endpoint
		// generation rather than the generation at certificate issuance.
		identity := m.state.Pairing.Identity
		identity.Generation = m.state.AcknowledgedGeneration
		directReply(w, 201, identity)
		return
	}
	next := m.cloneLocked()
	identity, err := directIssue(next, next.Pairing.ID, csr)
	if err != nil {
		directFailure(w, 503, "unavailable")
		return
	}
	identity.Generation = next.AcknowledgedGeneration
	next.Pairing.PreviousSerial = next.Pairing.Identity.Serial
	next.Pairing.Identity = identity
	if m.saveLocked(r.Context(), next) != nil {
		directFailure(w, 503, "storage_unavailable")
		return
	}
	directReply(w, 201, identity)
}
func (m *Direct) handleDirectSession(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "transport=2" || r.Header.Get("X-Mobile-Egress-Protocol") != "direct/1" {
		directFailure(w, 400, "incompatible_protocol")
		return
	}
	m.opMu.Lock()
	defer m.opMu.Unlock()
	serial, err := m.admitLocked(r, true)
	if err != nil {
		directAdmissionFailure(w, err)
		return
	}
	// Local shutdown cannot race an admitted connection into a stopped manager.
	if m.runCtx == nil || m.runCtx.Err() != nil {
		directFailure(w, 503, "unavailable")
		return
	}
	upgrader := websocket.Upgrader{HandshakeTimeout: 5 * time.Second, CheckOrigin: func(r *http.Request) bool { return r.Header.Get("Origin") == "" }}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	session, err := relayclient.AcceptAgent(conn)
	if err != nil {
		conn.Close()
		return
	}
	m.opener.swap(session)
	m.mu.Lock()
	m.sessionGeneration = m.state.AcknowledgedGeneration
	m.sessionTransport = effectiveTransport(m.state.Configuration.Transport)
	m.mu.Unlock()
	m.notifyEndpointUpdateLocked()
	m.activeSerial = serial
	expiry := r.TLS.PeerCertificates[0].NotAfter
	go func() {
		timer := time.NewTimer(time.Until(expiry))
		defer timer.Stop()
		select {
		case <-session.Done():
			return
		case <-timer.C:
			session.Close()
		}
	}()
}
