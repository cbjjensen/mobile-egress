package nodeservice

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"mobile-egress/windows-client/internal/proxyendpoint"
	"mobile-egress/windows-client/internal/securestore"
)

// Direct owns one locally protected authority and up to ten independently authenticated phone tunnels.
// opMu serializes identity changes and persistence, never stream traffic.
type Direct struct {
	activationCancel context.CancelFunc
	activationClient *http.Client
	activationOrigin string
	gatewayState     string
	gatewayEpoch     uint64
	repository       *Repository
	version          string
	opMu             sync.Mutex
	state            *directState
	runCtx           context.Context
	server           *http.Server
	listener         net.Listener
	phones           map[string]*phoneRuntime
	tlsConfig        atomic.Pointer[tls.Config]
	denied           atomic.Bool
	enrollSlots      chan struct{}
	mu               sync.RWMutex
	status           StandaloneStatus
}

func NewDirect(repository *Repository, platform, architecture, version string) *Direct {
	return &Direct{repository: repository, version: version, enrollSlots: make(chan struct{}, 16), status: StandaloneStatus{Phase: "waiting", Message: "Configure this Client's public endpoint to pair a phone.", Version: version}}
}

func (m *Direct) ensureLocked(ctx context.Context) error {
	if m.state != nil {
		return nil
	}
	if m.repository == nil || m.repository.store == nil {
		return errDirectStorage
	}
	raw, err := m.repository.store.Get(ctx, directStateKey)
	var state *directState
	if errors.Is(err, securestore.ErrNotFound) {
		state, err = newDirectState()
		if err != nil {
			return errDirectStorage
		}
		// Read old local credentials without validating an expired relay certificate.
		// Recovery material remains in its original protected record; it is never dialed.
		m.repository.mu.Lock()
		legacy, legacyErr := m.repository.load(ctx)
		m.repository.mu.Unlock()
		if legacyErr == nil {
			state.MigrationRequired = true
			if legacy.Pairing != nil && len(legacy.Pairing.NodeID) == 36 {
				if _, err := uuid.Parse(legacy.Pairing.NodeID); err == nil {
					state.ClientID = legacy.Pairing.NodeID
				}
			}
			if legacy.Configuration != nil {
				state.Username = legacy.Configuration.SOCKSUsername
				state.Password = legacy.Configuration.SOCKSPassword
			}
		} else if !errors.Is(legacyErr, securestore.ErrNotFound) {
			return errDirectStorage
		}
		if state.Username == "" || state.Password == "" {
			return errDirectStorage
		}
		if err = migrateDirectPhones(state); err != nil {
			return err
		}
		if err = m.saveLocked(ctx, state); err != nil {
			return err
		}
	} else {
		if err != nil || len(raw) > maximumStateBytes {
			return errDirectStorage
		}
		state = &directState{}
		if directStrictJSON(raw, state) != nil || (state.Version != 2 && state.Version != 3) || state.ClientID == "" {
			return errDirectStorage
		}
		migrating := state.Version == 2
		if err = migrateDirectPhones(state); err != nil {
			return err
		}
		if _, _, err = directCA(state); err != nil {
			return errDirectStorage
		}
		var loadedTLS *tls.Config
		if state.Configuration != nil {
			valid, e := validateDirectConfiguration(*state.Configuration)
			if e != nil || valid.BindAddress != state.Configuration.BindAddress || valid.DisplayName != state.Configuration.DisplayName || state.Generation == 0 || state.Generation > math.MaxInt64 {
				return errDirectStorage
			}
			// Older records can use equivalent port/IP spellings. Keep their
			// endpoint payload unchanged at this signed generation; Configure
			// emits canonical spelling as a new generation when requested.
			if directServerNeedsRenewal(state) {
				migrating = true
				if directServerCertificate(state, state.Configuration.Endpoint) != nil {
					return errDirectStorage
				}
			}
			config, e := directTLS(state)
			if e != nil {
				return e
			}
			loadedTLS = config
		}
		if state.Hosted != nil && validateSavedHosted(state.Hosted) != nil {
			return errDirectStorage
		}
		if state.Configuration != nil && state.Configuration.Transport == "hosted" && state.Hosted == nil {
			return errDirectStorage
		}
		if state.Activation != nil && validateSavedActivation(state.Activation) != nil {
			return errDirectStorage
		}
		if migrating {
			if err = m.saveLocked(ctx, state); err != nil {
				return err
			}
		} else {
			m.state = state
		}
		if loadedTLS != nil {
			m.tlsConfig.Store(loadedTLS)
		}
	}
	m.refreshLocked()
	return nil
}

func (m *Direct) cloneLocked() *directState {
	raw, _ := json.Marshal(m.state)
	var next directState
	_ = json.Unmarshal(raw, &next)
	return &next
}
func (m *Direct) saveLocked(ctx context.Context, next *directState) error {
	raw, err := json.Marshal(next)
	if err != nil || len(raw) > maximumStateBytes {
		return errDirectStorage
	}
	if err = m.repository.store.Put(ctx, directStateKey, raw); err != nil {
		return errDirectStorage
	}
	m.state = next
	return nil
}

func (m *Direct) Status() StandaloneStatus {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.mu.RLock()
	status := m.status
	m.mu.RUnlock()
	if m.state != nil {
		for _, p := range m.state.Phones {
			if m.phoneStatusLocked(p).Connected {
				status.Connected = true
			}
		}
	}
	if status.Connected {
		status.Phase = "ready"
		status.Message = "Phone connected. Copy a proxy to use it in your application."
	}
	return status
}
func (m *Direct) setError(message string) {
	m.mu.Lock()
	m.status.Phase = "error"
	m.status.Message = message
	m.mu.Unlock()
}
func (m *Direct) refreshLocked() {
	s := m.state
	if s == nil {
		return
	}
	status := StandaloneStatus{Version: m.version, ClientID: s.ClientID, Generation: s.Generation, SOCKSAddress: proxyendpoint.SOCKSAddress(), HTTPAddress: proxyendpoint.HTTPConnectAddress(), Phase: "waiting", Message: "Configure this Client's public endpoint to pair a phone."}
	status.Transport = "hosted"
	status.Message = "Activate Inevitable to connect this Client, then pair your phone."
	status.ActivationState = "inactive"
	status.GatewayState = m.gatewayState
	if status.GatewayState == "" {
		status.GatewayState = "disconnected"
	}
	if s.Hosted != nil {
		status.ActivationState = "authorized"
	}
	if s.Activation != nil {
		status.ActivationState = s.Activation.Status
	}
	if s.Configuration != nil {
		status.Transport = effectiveTransport(s.Configuration.Transport)
		status.DisplayName = s.Configuration.DisplayName
		status.Endpoint = s.Configuration.Endpoint
		status.BindAddress = s.Configuration.BindAddress
		status.Message = "Ready to pair a phone. Create an invitation in this Client app."
		if status.Transport == "hosted" {
			status.Message = "Hosted connectivity configured. Pair your phone using this Client's invitation."
		}
		if m.listener != nil {
			status.Phase = "listening"
			status.Message = "Listening. Pair your phone to verify cellular reachability."
			if status.Transport == "hosted" {
				status.Message = "Gateway " + status.GatewayState + ". Pair your phone to verify its authenticated cellular connection."
			}
			if pendingPhone(s) != nil {
				status.Phase = "awaiting_phone"
				status.Message = "Awaiting phone. Scan the invitation using the phone's cellular connection."
			}
		}
	}
	if s.MigrationRequired {
		status.Phase = "migration_required"
		status.Message = "This Client needs a reachable public endpoint and fresh phone pairing."
	}
	for _, p := range s.Phones {
		ps := m.phoneStatusLocked(p)
		status.Paired = status.Paired || ps.Paired
		status.UpdatePending = status.UpdatePending || ps.UpdatePending
		if ps.InvitationExpiresAt != nil {
			status.InvitationExpiresAt = ps.InvitationExpiresAt
		}
		if len(s.Phones) == 1 && p.Pairing != nil {
			status.Phase = ps.Phase
			status.Message = ps.Message
		}
	}
	if len(s.Phones) > 1 {
		status.SOCKSAddress = ""
		status.HTTPAddress = ""
	}
	if len(s.Phones) == 1 {
		status.SOCKSAddress = fmtPhoneAddress(s.Phones[0].Slot, false)
		status.HTTPAddress = fmtPhoneAddress(s.Phones[0].Slot, true)
	}
	if status.Transport == "hosted" && status.GatewayState == "authorization_rejected" {
		status.ActivationState = "access_rejected"
		status.Message = "Gateway access was rejected. Check Inevitable Mobile Relay account access or reactivate this Client. Local phone pairing is preserved."
	}
	if m.denied.Load() {
		status.Phase = "error"
		status.Message = "Access is disabled because revocation could not be saved. Repair protected storage."
	}
	m.mu.Lock()
	status.Running = m.status.Running
	m.status = status
	m.mu.Unlock()
}

func (m *Direct) Configure(ctx context.Context, configuration DirectConfiguration) error {
	if configuration.Transport == "hosted" {
		return errors.New("Activate Inevitable to configure hosted connectivity.")
	}
	config, err := validateDirectConfiguration(configuration)
	if err != nil {
		return err
	}
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if err = m.ensureLocked(ctx); err != nil {
		return err
	}
	next := m.cloneLocked()
	next.Activation = nil
	changed := next.Configuration == nil || next.Configuration.Endpoint != config.Endpoint || effectiveTransport(next.Configuration.Transport) != effectiveTransport(config.Transport)
	if changed {
		if next.Generation == math.MaxInt64 {
			return errors.New("Endpoint generation exhausted. Reinstall with a new identity.")
		}
		next.Generation++
		if err = directServerCertificate(next, config.Endpoint); err != nil {
			return errDirectStorage
		}

	}
	if !changed && directServerNeedsRenewal(next) {
		if err = directServerCertificate(next, config.Endpoint); err != nil {
			return errDirectStorage
		}
	}
	next.Configuration = &config
	tlsConfig, err := directTLS(next)
	if err != nil {
		return err
	}
	var replacement net.Listener
	if m.runCtx != nil && (m.listener == nil || m.state.Configuration == nil || m.state.Configuration.BindAddress != config.BindAddress) {
		replacement, err = net.Listen("tcp", config.BindAddress)
		if err != nil {
			return errors.New("The TLS listener address is unavailable. Release the selected port or choose another listener port.")
		}
	}
	if err = m.saveLocked(ctx, next); err != nil {
		if replacement != nil {
			replacement.Close()
		}
		return err
	}
	m.stopActivationPollingLocked()
	m.notifyEndpointUpdateLocked()
	m.tlsConfig.Store(tlsConfig)
	if replacement != nil {
		m.replaceListenerLocked(replacement)
	}
	m.refreshLocked()
	return nil
}
func (m *Direct) IssueInvitation(ctx context.Context) (string, error) {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if err := m.ensureLocked(ctx); err != nil {
		return "", err
	}
	p, err := m.solePhoneLocked()
	if err != nil {
		return "", err
	}
	if p != nil && p.Pairing != nil && p.Pairing.Acknowledged {
		return "", errors.New("Use Add phone to pair another phone.")
	}
	invitation, err := m.addPhoneLocked(ctx, "")
	return invitation.Bundle, err
}
func (m *Direct) CancelInvitation(ctx context.Context) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if err := m.ensureLocked(ctx); err != nil {
		return err
	}
	p, err := m.solePhoneLocked()
	if err != nil {
		return err
	}
	if p == nil {
		return nil
	}
	if p.Pairing != nil && p.Pairing.Acknowledged {
		return nil
	}
	return m.removePhoneLocked(ctx, p.ID)
}
func (m *Direct) ExportEndpointUpdate(ctx context.Context) (string, error) {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if err := m.ensureLocked(ctx); err != nil {
		return "", err
	}
	p, err := m.solePhoneLocked()
	if err != nil {
		return "", err
	}
	if p == nil {
		return "", errors.New("Pair a phone before exporting an update.")
	}
	if m.runtimeLocked(p.ID).denied {
		return "", errDirectStorage
	}
	return directPhoneEndpointBundle(m.state, p)
}
func (m *Direct) Revoke(ctx context.Context) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if err := m.ensureLocked(ctx); err != nil {
		return err
	}
	p, err := m.solePhoneLocked()
	if err != nil {
		return err
	}
	if p == nil {
		return nil
	}
	return m.removePhoneLocked(ctx, p.ID)
}
func (m *Direct) Proxy(ctx context.Context, kind string) (string, error) {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if err := m.ensureLocked(ctx); err != nil {
		return "", err
	}
	if m.state.Configuration == nil {
		return "", errors.New("Configure this Client first.")
	}
	p, err := m.solePhoneLocked()
	if err != nil {
		return "", err
	}
	if p == nil {
		if m.state.Username == "" {
			return "", errors.New("Add a phone first.")
		}
		p = &directPhone{Username: m.state.Username, Password: m.state.Password}
	}
	if m.runtimeLocked(p.ID).denied {
		return "", errDirectStorage
	}
	return phoneProxy(p, kind)
}

func (m *Direct) replaceListenerLocked(listener net.Listener) {
	if m.server != nil {
		_ = m.server.Close()
	}
	m.listener = listener
	config := &tls.Config{MinVersion: tls.VersionTLS13, GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
		current := m.tlsConfig.Load()
		if current == nil {
			return nil, errDirectStorage
		}
		return current, nil
	}}
	server := &http.Server{Handler: m.handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8 << 10, ErrorLog: log.New(io.Discard, "", 0)}
	m.server = server
	if runner, ok := listener.(interface{ Run(context.Context) error }); ok {
		go runner.Run(m.runCtx)
	}
	go func() {
		err := server.Serve(tls.NewListener(newDirectListener(listener, 64), config))
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			m.setError("The TLS listener stopped. Restart or repair the Client service.")
		}
	}()
}
func (m *Direct) Run(ctx context.Context) error {
	m.opMu.Lock()
	if m.runCtx != nil {
		m.opMu.Unlock()
		return errors.New("Client service is already running.")
	}
	if err := m.ensureLocked(ctx); err != nil {
		m.opMu.Unlock()
		m.setError(err.Error())
		return err
	}
	if m.state.Activation != nil && m.state.Activation.Status == "authorized" {
		if err := m.configureHostedLocked(ctx, m.state.Activation.DisplayName); err != nil {
			m.runCtx = nil
			m.opMu.Unlock()
			return err
		}
	}
	m.runCtx = ctx
	m.startActivationPollingLocked()
	if m.state.Configuration != nil {
		if directServerNeedsRenewal(m.state) {
			next := m.cloneLocked()
			if directServerCertificate(next, next.Configuration.Endpoint) != nil || m.saveLocked(ctx, next) != nil {
				m.stopActivationPollingLocked()
				m.runCtx = nil
				m.opMu.Unlock()
				return errDirectStorage
			}
		}
		config, err := directTLS(m.state)
		if err == nil {
			m.tlsConfig.Store(config)
		}
		var listener net.Listener
		if err == nil {
			if m.state.Configuration.Transport == "hosted" {
				listener, err = m.hostedListenerLocked(m.state)
			} else {
				listener, err = net.Listen("tcp", m.state.Configuration.BindAddress)
			}
		}
		if err != nil {
			m.stopActivationPollingLocked()
			m.runCtx = nil
			m.opMu.Unlock()
			m.setError("TLS listener port is unavailable. Release the port or change the listener address.")
			return errors.New("TLS listener is unavailable")
		}
		m.replaceListenerLocked(listener)
	}
	for _, p := range m.state.Phones {
		m.startPhoneProxyLocked(p)
	}
	m.mu.Lock()
	m.status.Running = true
	m.mu.Unlock()
	m.refreshLocked()
	m.opMu.Unlock()
	defer func() {
		m.opMu.Lock()
		m.stopActivationPollingLocked()
		for _, rt := range m.phones {
			m.stopPhoneProxyLocked(rt)
		}
		if m.server != nil {
			m.server.Close()
		}
		m.server = nil
		m.listener = nil
		m.runCtx = nil
		m.mu.Lock()
		m.status.Running = false
		m.mu.Unlock()
		m.opMu.Unlock()
	}()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			m.opMu.Lock()
			_ = m.expirePhoneLocked(ctx)
			m.opMu.Unlock()
			m.refreshServerCertificate(ctx)
		}
	}
}
func (m *Direct) refreshServerCertificate(ctx context.Context) {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if m.state.Configuration == nil {
		return
	}
	current := m.tlsConfig.Load()
	if current == nil {
		return
	}
	cert := current.Certificates[0].Leaf
	if cert == nil || time.Until(cert.NotAfter) < 7*24*time.Hour {
		next := m.cloneLocked()
		if directServerCertificate(next, next.Configuration.Endpoint) != nil {
			return
		}
		config, err := directTLS(next)
		if err != nil {
			return
		}
		if m.saveLocked(ctx, next) == nil {
			m.tlsConfig.Store(config)
		}
	}
}
