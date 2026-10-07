package nodeservice

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"mobile-egress/windows-client/internal/httpconnect"
	"mobile-egress/windows-client/internal/proxyendpoint"
	"mobile-egress/windows-client/internal/socks"
)

const maxPhones = 10

// All runtime-map and session metadata access is serialized by opMu. Each
// opener independently synchronizes stream traffic and closes replaced sessions.
type phoneRuntime struct {
	updatePending     *phoneEndpointNotification
	updateSending     bool
	opener            switchingTunnel
	activeSerial      string
	sessionGeneration uint64
	sessionTransport  string
	denied            bool
	socks             *socks.Server
	http              *httpconnect.Server
	proxyError        string
}

func (m *Direct) runtimeLocked(id string) *phoneRuntime {
	if m.phones == nil {
		m.phones = make(map[string]*phoneRuntime)
	}
	if m.phones[id] == nil {
		m.phones[id] = &phoneRuntime{}
	}
	return m.phones[id]
}
func phoneByID(s *directState, id string) *directPhone {
	for _, p := range s.Phones {
		if p.ID == id {
			return p
		}
	}
	return nil
}
func pendingPhone(s *directState) *directPhone {
	for _, p := range s.Phones {
		if p.Pairing == nil || !p.Pairing.Acknowledged {
			return p
		}
	}
	return nil
}
func phoneName(name string, slot int, defaultEmpty bool) (string, error) {
	// Controls are rejected before trimming so newline-wrapped labels are invalid.
	if !utf8.ValidString(name) || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return "", errDirectInvalid
	}
	name = strings.TrimSpace(name)
	if name == "" && defaultEmpty {
		name = fmt.Sprintf("Phone %d", slot+1)
	}
	if name == "" || utf8.RuneCountInString(name) > 64 {
		return "", errDirectInvalid
	}
	return name, nil
}
func migrateDirectPhones(s *directState) error {
	if s.Version == 3 {
		return validateDirectPhones(s)
	}
	if s.Version != 2 || len(s.Phones) != 0 {
		return errDirectStorage
	}
	if s.Pairing != nil && s.Pairing.Revoked {
		s.Username = ""
		s.Password = ""
		s.Invitation = nil
	}
	if (s.Pairing != nil && !s.Pairing.Revoked) || s.Invitation != nil {
		id, err := directID()
		if err != nil {
			return errDirectStorage
		}
		s.Phones = []*directPhone{{InvitationGeneration: s.Generation, ID: id, Name: "Phone 1", Slot: 0, Username: s.Username, Password: s.Password, Invitation: s.Invitation, Pairing: s.Pairing, AcknowledgedGeneration: s.AcknowledgedGeneration, AcknowledgedEndpoint: s.AcknowledgedEndpoint}}
		if s.Pairing != nil && s.Pairing.Revoked {
			s.Phones[0].Pairing = nil
		}
		if s.Pairing != nil {
			s.Phones[0].InvitationGeneration = s.Pairing.Identity.Generation
		}
		s.Username = ""
		s.Password = ""
	}
	s.Version = 3
	s.Pairing = nil
	s.Invitation = nil
	s.AcknowledgedGeneration = 0
	s.AcknowledgedEndpoint = ""
	return validateDirectPhones(s)
}
func validateDirectPhones(s *directState) error {
	if (len(s.Phones) > 0 && s.Configuration == nil) || len(s.Phones) > maxPhones || s.Pairing != nil || s.Invitation != nil || s.AcknowledgedGeneration != 0 || s.AcknowledgedEndpoint != "" {
		return errDirectStorage
	}
	ids := map[string]bool{}
	slots := map[int]bool{}
	serials := map[string]bool{}
	pending := 0
	for _, p := range s.Phones {
		if p == nil {
			return errDirectStorage
		}
		if _, e := uuid.Parse(p.ID); e != nil {
			return errDirectStorage
		}
		name, e := phoneName(p.Name, p.Slot, false)
		if e != nil || name != p.Name || ids[p.ID] || slots[p.Slot] || p.Slot < 0 || p.Slot >= maxPhones || p.Username == "" || p.Password == "" || p.AcknowledgedGeneration > s.Generation {
			return errDirectStorage
		}
		ids[p.ID] = true
		slots[p.Slot] = true
		if p.Pairing == nil || !p.Pairing.Acknowledged {
			pending++
			if p.Invitation == nil {
				return errDirectStorage
			}
		}
		if p.Pairing != nil {
			if p.Pairing.Revoked || p.Pairing.ID == "" || p.Pairing.Identity.Serial == "" || len(p.Pairing.PublicKey) == 0 {
				return errDirectStorage
			}
			for _, serial := range []string{p.Pairing.Identity.Serial, p.Pairing.PreviousSerial} {
				if serial != "" {
					if serials[serial] {
						return errDirectStorage
					}
					serials[serial] = true
				}
			}
		}
		if p.Invitation != nil && (p.Invitation.ClientID != s.ClientID || p.Invitation.Capability == "" || p.Invitation.InvitationID == "") {
			return errDirectStorage
		}
	}
	if pending > 1 {
		return errDirectStorage
	}
	return nil
}
func (m *Direct) expirePhoneLocked(ctx context.Context) error {
	p := pendingPhone(m.state)
	if p == nil || p.Pairing != nil || time.Now().Before(p.Invitation.ExpiresAt) {
		return nil
	}
	return m.removePhoneLocked(ctx, p.ID)
}
func (m *Direct) phoneStatusLocked(p *directPhone) PhoneStatus {
	rt := m.runtimeLocked(p.ID)
	s := PhoneStatus{ID: p.ID, Name: p.Name, Slot: p.Slot, SOCKSAddress: fmtPhoneAddress(p.Slot, false), HTTPAddress: fmtPhoneAddress(p.Slot, true), Phase: "awaiting_phone", Message: "Scan the invitation on your phone.", ProxyRunning: rt.socks != nil && rt.http != nil}
	if p.Invitation != nil && (p.Pairing == nil || !p.Pairing.Acknowledged) {
		expiry := p.Invitation.ExpiresAt
		s.InvitationExpiresAt = &expiry
	}
	if p.Pairing != nil {
		s.Paired = p.Pairing.Acknowledged
		s.UpdatePending = p.AcknowledgedGeneration < m.state.Generation
		s.Phase = "acknowledging"
		s.Message = "Waiting for the phone to confirm pairing."
		if s.Paired {
			s.Phase = "awaiting_phone"
			s.Message = "Waiting for the paired phone to connect."
		}
		tunnel := rt.opener.current()
		s.Connected = m.status.Running && s.Paired && !s.UpdatePending && !rt.denied && rt.sessionGeneration == m.state.Generation && rt.sessionTransport == effectiveTransport(m.state.Configuration.Transport) && tunnel != nil && tunnel.Healthy()
		if s.UpdatePending && s.Paired {
			s.Phase = "update_pending"
			s.Message = "Phone endpoint update is pending."
		}
		if s.Connected {
			s.Phase = "ready"
			s.Message = "Phone connected. Copy its proxy to use it."
		}
	}
	if rt.proxyError != "" {
		s.Phase = "proxy_error"
		s.Message = rt.proxyError
	}
	if rt.denied {
		s.Connected = false
		s.Phase = "error"
		s.Message = "Removal could not be saved. This phone is disabled until removal is retried successfully; restarting may restore its saved access."
	}
	return s
}
func (m *Direct) Phones(ctx context.Context) (PhonesStatus, error) {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	result := PhonesStatus{Phones: []PhoneStatus{}, MaxPhones: maxPhones}
	if err := m.ensureLocked(ctx); err != nil {
		return result, err
	}
	// Failed expiration is scoped to its reservation. Preserve sibling status
	// and expose the target runtime uncertainty; mutations still return errors.
	_ = m.expirePhoneLocked(ctx)
	for _, p := range m.state.Phones {
		result.Phones = append(result.Phones, m.phoneStatusLocked(p))
	}
	sort.Slice(result.Phones, func(i, j int) bool { return result.Phones[i].Slot < result.Phones[j].Slot })
	if p := pendingPhone(m.state); p != nil {
		result.PendingPhoneID = p.ID
	}
	return result, nil
}
func (m *Direct) AddPhone(ctx context.Context, name string) (PhoneInvitation, error) {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if err := m.ensureLocked(ctx); err != nil {
		return PhoneInvitation{}, err
	}
	return m.addPhoneLocked(ctx, name)
}
func (m *Direct) addPhoneLocked(ctx context.Context, name string) (PhoneInvitation, error) {
	if m.state.Configuration == nil {
		return PhoneInvitation{}, errors.New("Configure this Client first.")
	}
	if _, err := phoneName(name, 0, true); err != nil {
		return PhoneInvitation{}, err
	}
	if err := m.expirePhoneLocked(ctx); err != nil {
		return PhoneInvitation{}, err
	}
	p := pendingPhone(m.state)
	if p == nil {
		if len(m.state.Phones) >= maxPhones {
			return PhoneInvitation{}, errors.New("This Client already has ten phones.")
		}
		next := m.cloneLocked()
		slot := 0
		for ; slot < maxPhones; slot++ {
			used := false
			for _, p := range next.Phones {
				used = used || p.Slot == slot
			}
			if !used {
				break
			}
		}
		label, _ := phoneName(name, slot, true)
		id, err := directID()
		if err != nil {
			return PhoneInvitation{}, errDirectStorage
		}
		invitationID, err := directID()
		if err != nil {
			return PhoneInvitation{}, errDirectStorage
		}
		capability, err := directRandom()
		if err != nil {
			return PhoneInvitation{}, errDirectStorage
		}
		password, err := directRandom()
		if err != nil {
			return PhoneInvitation{}, errDirectStorage
		}
		username, err := directRandom()
		if err != nil {
			return PhoneInvitation{}, errDirectStorage
		}
		if next.Username != "" && next.Password != "" {
			username = next.Username
			password = next.Password
			next.Username = ""
			next.Password = ""
		}
		p = &directPhone{InvitationGeneration: next.Generation, ID: id, Name: label, Slot: slot, Username: username, Password: password, Invitation: &directInvitation{Version: 2, Type: "mobile-egress-direct-invitation", ClientID: next.ClientID, DisplayName: next.Configuration.DisplayName, Endpoint: next.Configuration.Endpoint, CACertificatePEM: next.CACertificatePEM, InvitationID: invitationID, Capability: capability, ExpiresAt: time.Now().UTC().Add(10 * time.Minute).Truncate(time.Second), Role: "agent", Transport: wireTransport(next.Configuration.Transport)}}
		next.Phones = append(next.Phones, p)
		if err := m.saveLocked(ctx, next); err != nil {
			return PhoneInvitation{}, err
		}
		if m.runCtx != nil {
			m.startPhoneProxyLocked(p)
		}
	}
	if m.runtimeLocked(p.ID).denied {
		return PhoneInvitation{}, errDirectStorage
	}
	raw, err := json.Marshal(p.Invitation)
	m.refreshLocked()
	return PhoneInvitation{PhoneID: p.ID, Bundle: base64.RawURLEncoding.EncodeToString(raw)}, err
}
func (m *Direct) RenamePhone(ctx context.Context, id, name string) error {
	name, err := phoneName(name, 0, false)
	if err != nil {
		return err
	}
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if err := m.ensureLocked(ctx); err != nil {
		return err
	}
	next := m.cloneLocked()
	p := phoneByID(next, id)
	if p == nil {
		return errDirectInvalid
	}
	p.Name = name
	if err := m.saveLocked(ctx, next); err != nil {
		return err
	}
	m.refreshLocked()
	return nil
}
func (m *Direct) CancelPhoneInvitation(ctx context.Context, id string) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if err := m.ensureLocked(ctx); err != nil {
		return err
	}
	p := phoneByID(m.state, id)
	if p == nil || p.Pairing != nil && p.Pairing.Acknowledged {
		return errDirectInvalid
	}
	return m.removePhoneLocked(ctx, id)
}
func (m *Direct) RevokePhone(ctx context.Context, id string) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if err := m.ensureLocked(ctx); err != nil {
		return err
	}
	return m.removePhoneLocked(ctx, id)
}
func (m *Direct) removePhoneLocked(ctx context.Context, id string) error {
	if phoneByID(m.state, id) == nil {
		return errDirectInvalid
	}
	next := m.cloneLocked()
	for i, p := range next.Phones {
		if p.ID == id {
			next.Phones = append(next.Phones[:i], next.Phones[i+1:]...)
			break
		}
	}
	err := m.saveLocked(ctx, next)
	rt := m.runtimeLocked(id)
	rt.denied = err != nil
	m.stopPhoneProxyLocked(rt)
	if err == nil {
		delete(m.phones, id)
	}
	m.refreshLocked()
	return err
}
func (m *Direct) PhoneProxy(ctx context.Context, id, kind string) (string, error) {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if err := m.ensureLocked(ctx); err != nil {
		return "", err
	}
	p := phoneByID(m.state, id)
	if p == nil {
		return "", errDirectInvalid
	}
	if m.runtimeLocked(id).denied {
		return "", errDirectStorage
	}
	return phoneProxy(p, kind)
}
func phoneProxy(p *directPhone, kind string) (string, error) {
	if kind == "http" {
		return fmt.Sprintf("%s:%s:%s", fmtPhoneAddress(p.Slot, true), p.Username, p.Password), nil
	}
	if kind == "socks" {
		u := url.URL{Scheme: "socks5", Host: fmtPhoneAddress(p.Slot, false), User: url.UserPassword(p.Username, p.Password)}
		return u.String(), nil
	}
	return "", errors.New("Unknown proxy format.")
}
func (m *Direct) ExportPhoneEndpointUpdate(ctx context.Context, id string) (string, error) {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if err := m.ensureLocked(ctx); err != nil {
		return "", err
	}
	p := phoneByID(m.state, id)
	if p == nil {
		return "", errDirectInvalid
	}
	if m.runtimeLocked(id).denied {
		return "", errDirectStorage
	}
	return directPhoneEndpointBundle(m.state, p)
}
func (m *Direct) RetryPhoneProxy(ctx context.Context, id string) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if err := m.ensureLocked(ctx); err != nil {
		return err
	}
	p := phoneByID(m.state, id)
	if p == nil {
		return errDirectInvalid
	}
	rt := m.runtimeLocked(id)
	if rt.denied {
		return errDirectStorage
	}
	if m.runCtx == nil {
		return errors.New("Client service is not running.")
	}
	m.startPhoneProxyLocked(p)
	if rt.proxyError != "" {
		return errors.New(rt.proxyError)
	}
	return nil
}
func (m *Direct) startPhoneProxyLocked(p *directPhone) {
	rt := m.runtimeLocked(p.ID)
	if rt.denied || rt.socks != nil && rt.http != nil {
		return
	}
	rt.proxyError = ""
	sp, hp, err := newPhoneProxyPair(p, &rt.opener, uint16(1080+2*p.Slot), uint16(1081+2*p.Slot))
	if err != nil {
		rt.proxyError = err.Error()
		return
	}
	rt.socks = sp
	rt.http = hp
}

// The same constructor serves production's stable ports and isolated ephemeral
// listeners in integration tests. A partial bind is always rolled back.
func newPhoneProxyPair(p *directPhone, opener *switchingTunnel, socksPort, httpPort uint16) (*socks.Server, *httpconnect.Server, error) {
	sp := socks.NewServer(socks.Config{Username: p.Username, Password: p.Password, Opener: opener})
	hp := httpconnect.NewServer(httpconnect.Config{Username: p.Username, Password: p.Password, Opener: opener})
	if err := sp.Start(socksPort); err != nil {
		return nil, nil, fmt.Errorf("Proxy port %d is occupied. Release the port and retry this phone's proxies.", socksPort)
	}
	if err := hp.Start(httpPort); err != nil {
		sp.Stop()
		return nil, nil, fmt.Errorf("Proxy port %d is occupied. Release the port and retry this phone's proxies.", httpPort)
	}
	return sp, hp, nil
}

func (m *Direct) stopPhoneProxyLocked(rt *phoneRuntime) {
	rt.updatePending = nil
	rt.opener.swap(nil)
	if rt.http != nil {
		rt.http.Stop()
		rt.http = nil
	}
	if rt.socks != nil {
		rt.socks.Stop()
		rt.socks = nil
	}
	rt.activeSerial = ""
	rt.sessionGeneration = 0
	rt.sessionTransport = ""
}
func (m *Direct) solePhoneLocked() (*directPhone, error) {
	if len(m.state.Phones) > 1 {
		return nil, errors.New("Select a phone for this action.")
	}
	if len(m.state.Phones) == 0 {
		return nil, nil
	}
	return m.state.Phones[0], nil
}

func fmtPhoneAddress(slot int, http bool) string {
	port := 1080 + 2*slot
	if http {
		port++
	}
	return proxyendpoint.Address(uint16(port))
}
