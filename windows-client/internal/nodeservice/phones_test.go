package nodeservice

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mobile-egress/windows-client/internal/proxyendpoint"
	"mobile-egress/windows-client/internal/securestore"
	"strings"
	"sync"
	"testing"
	"time"
)

// These exercise durable authority behavior: losing reservation identity,
// admitting an eleventh phone, or selecting a sibling's credentials must fail.
func TestPhonesReservationsLimitsAndIndependentRemoval(t *testing.T) {
	ctx := context.Background()
	m, first, csr := directTestConfigured(t)
	status, err := m.Phones(ctx)
	if err != nil || len(status.Phones) != 1 || status.PendingPhoneID == "" {
		t.Fatalf("reservation missing: %#v %v", status, err)
	}
	firstID := status.PendingPhoneID
	resumed, err := m.AddPhone(ctx, "ignored while resuming")
	if err != nil || resumed.PhoneID != firstID {
		t.Fatal("pending reservation was duplicated", err)
	}
	identity := directTestEnroll(t, m, first, csr)
	directTestAck(t, m, identity)
	original, err := m.PhoneProxy(ctx, firstID, "socks")
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{firstID}
	for n := 1; n < 10; n++ {
		added, err := m.AddPhone(ctx, "")
		if err != nil {
			t.Fatal(n, err)
		}
		ids = append(ids, added.PhoneID)
		raw, _ := base64.RawURLEncoding.DecodeString(added.Bundle)
		var invitation directInvitation
		if json.Unmarshal(raw, &invitation) != nil {
			t.Fatal("bad invitation")
		}
		issued := directTestEnroll(t, m, invitation, csr)
		directTestAck(t, m, issued)
	}
	if _, err := m.AddPhone(ctx, ""); err == nil {
		t.Fatal("eleventh phone admitted")
	}
	status, err = m.Phones(ctx)
	if err != nil || len(status.Phones) != 10 || status.MaxPhones != 10 {
		t.Fatal(status, err)
	}
	for n, p := range status.Phones {
		if p.Name != fmt.Sprintf("Phone %d", n+1) || p.Slot != n || p.SOCKSAddress != fmt.Sprintf("%s:%d", proxyendpoint.Host, 1080+2*n) || p.HTTPAddress != fmt.Sprintf("%s:%d", proxyendpoint.Host, 1081+2*n) {
			t.Fatalf("unstable slot: %#v", p)
		}
	}
	for _, operation := range []func() error{
		func() error { _, e := m.Proxy(ctx, "http"); return e },
		func() error { return m.Revoke(ctx) },
		func() error { _, e := m.ExportEndpointUpdate(ctx); return e },
		func() error { return m.CancelInvitation(ctx) },
	} {
		if operation() == nil {
			t.Fatal("ambiguous legacy operation allowed")
		}
	}
	removedProxy, _ := m.PhoneProxy(ctx, ids[4], "socks")
	if err := m.RevokePhone(ctx, ids[4]); err != nil {
		t.Fatal(err)
	}
	if _, err := m.PhoneProxy(ctx, ids[4], "socks"); err == nil {
		t.Fatal("removed ID accepted")
	}
	again, err := m.AddPhone(ctx, "")
	if err != nil || again.PhoneID == ids[4] {
		t.Fatal("reused identity", err)
	}
	replacement, _ := m.PhoneProxy(ctx, again.PhoneID, "socks")
	if replacement == removedProxy {
		t.Fatal("reused removed credentials")
	}
	kept, _ := m.PhoneProxy(ctx, firstID, "socks")
	if kept != original {
		t.Fatal("sibling credentials changed")
	}
	restarted := NewDirect(m.repository, "windows", "amd64", "test")
	after, err := restarted.Phones(ctx)
	if err != nil || len(after.Phones) != 10 || after.PendingPhoneID != again.PhoneID {
		t.Fatal("restart lost phones", err)
	}
}

type blockedPhoneUpdate struct {
	updateTunnel
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockedPhoneUpdate) SendEndpointUpdate(string) error {
	b.once.Do(func() { close(b.entered) })
	<-b.release
	return nil
}

func TestPhoneSlowUpdateDoesNotBlockSiblingAuthority(t *testing.T) {
	ctx := context.Background()
	m, i, csr := directTestConfigured(t)
	identity := directTestEnroll(t, m, i, csr)
	directTestAck(t, m, identity)
	sibling, err := m.AddPhone(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	blocked := &blockedPhoneUpdate{entered: make(chan struct{}), release: make(chan struct{})}
	m.runtimeLocked(m.state.Phones[0].ID).opener.swap(blocked)
	configured := make(chan error, 1)
	go func() {
		configured <- m.Configure(ctx, DirectConfiguration{Endpoint: "https://new.example", DisplayName: "Client"})
	}()
	select {
	case <-blocked.entered:
	case <-time.After(time.Second):
		t.Fatal("update was not attempted")
	}
	renamed := make(chan error, 1)
	go func() { renamed <- m.RenamePhone(ctx, sibling.PhoneID, "Sibling") }()
	select {
	case err := <-renamed:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(300 * time.Millisecond):
		t.Error("slow phone update blocked sibling authority")
	}
	close(blocked.release)
	if err := <-configured; err != nil {
		t.Fatal(err)
	}
}

func TestPhonesWithoutConfigurationFailClosed(t *testing.T) {
	ctx := context.Background()
	m, i, csr := directTestConfigured(t)
	identity := directTestEnroll(t, m, i, csr)
	directTestAck(t, m, identity)
	bad := m.cloneLocked()
	bad.Configuration = nil
	raw, _ := json.Marshal(bad)
	_ = m.repository.store.Put(ctx, directStateKey, raw)
	broken := NewDirect(m.repository, "windows", "amd64", "test")
	for n := 0; n < 2; n++ {
		if _, err := broken.Phones(ctx); err == nil {
			t.Fatal("phone collection without configuration accepted")
		}
	}
}

// A v2 record is constructed independently of the new collection. Migration
// must retain exact credentials, issued identity and invitation bytes atomically.
func TestPhonesVersion2MigrationAndFailedWriteRestart(t *testing.T) {
	for _, phase := range []string{"invited", "issued", "acknowledged"} {
		t.Run(phase, func(t *testing.T) {
			ctx := context.Background()
			m, invitation, csr := directTestConfigured(t)
			if phase != "invited" {
				identity := directTestEnroll(t, m, invitation, csr)
				if phase == "acknowledged" {
					directTestAck(t, m, identity)
				}
			}
			old := m.cloneLocked()
			phone := old.Phones[0]
			old.Version = 2
			old.Phones = nil
			old.Username = phone.Username
			old.Password = phone.Password
			old.Pairing = phone.Pairing
			old.Invitation = phone.Invitation
			old.AcknowledgedGeneration = phone.AcknowledgedGeneration
			old.AcknowledgedEndpoint = phone.AcknowledgedEndpoint
			original, _ := json.Marshal(old)
			store := &directFailingStore{Store: securestore.NewMemoryStore()}
			if err := store.Put(ctx, directStateKey, original); err != nil {
				t.Fatal(err)
			}
			store.setFail(true)
			failed := NewDirect(NewRepository(store), "windows", "amd64", "test")
			if _, err := failed.Phones(ctx); err == nil {
				t.Fatal("failed migration reported success")
			}
			unchanged, _ := store.Get(ctx, directStateKey)
			if !bytes.Equal(unchanged, original) {
				t.Fatal("failed write altered original")
			}
			store.setFail(false)
			restarted := NewDirect(NewRepository(store), "windows", "amd64", "test")
			status, err := restarted.Phones(ctx)
			if err != nil || len(status.Phones) != 1 {
				t.Fatal(status, err)
			}
			p := restarted.state.Phones[0]
			if restarted.state.Version != 3 || restarted.state.ClientID != old.ClientID || restarted.state.CAPrivateKeyPEM != old.CAPrivateKeyPEM || p.Username != old.Username || p.Password != old.Password || p.Slot != 0 {
				t.Fatal("migration changed identity/credentials")
			}
			want, _ := json.Marshal(old.Pairing)
			got, _ := json.Marshal(p.Pairing)
			if !bytes.Equal(want, got) {
				t.Fatal("issued identity changed")
			}
			want, _ = json.Marshal(old.Invitation)
			got, _ = json.Marshal(p.Invitation)
			if !bytes.Equal(want, got) {
				t.Fatal("pending recovery changed")
			}
			again := NewDirect(restarted.repository, "windows", "amd64", "test")
			againStatus, err := again.Phones(ctx)
			if err != nil || againStatus.Phones[0].ID != p.ID {
				t.Fatal("migrated ID unstable", err)
			}
			saved, _ := store.Get(ctx, directStateKey)
			var oldBinary struct {
				Version int `json:"version"`
			}
			_ = json.Unmarshal(saved, &oldBinary)
			if oldBinary.Version == 2 {
				t.Fatal("old binary would accept new schema")
			}
		})
	}
}

func TestPhonesInvalidMigrationNeverCachesOrOverwritesState(t *testing.T) {
	ctx := context.Background()
	old, _ := newDirectState()
	old.CAPrivateKeyPEM = "invalid key"
	raw, _ := json.Marshal(old)
	store := securestore.NewMemoryStore()
	_ = store.Put(ctx, directStateKey, raw)
	m := NewDirect(NewRepository(store), "windows", "amd64", "test")
	for i := 0; i < 2; i++ {
		if _, err := m.Phones(ctx); err == nil {
			t.Fatal("unreadable state admitted on retry")
		}
	}
	saved, _ := store.Get(ctx, directStateKey)
	if !bytes.Equal(raw, saved) {
		t.Fatal("unreadable migration overwrote state")
	}
}

func TestPhoneFailedRemovalOnlySuppressesTargetAndReportsRestartUncertainty(t *testing.T) {
	ctx := context.Background()
	m, i, csr := directTestConfigured(t)
	first := directTestEnroll(t, m, i, csr)
	directTestAck(t, m, first)
	firstID := m.state.Phones[0].ID
	second, err := m.AddPhone(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(second.Bundle)
	var inv directInvitation
	_ = json.Unmarshal(raw, &inv)
	sibling := directTestEnroll(t, m, inv, csr)
	directTestAck(t, m, sibling)
	store := &directFailingStore{Store: m.repository.store}
	m.repository.store = store
	store.setFail(true)
	if m.RevokePhone(ctx, firstID) == nil {
		t.Fatal("failed removal reported durable success")
	}
	status, err := m.Phones(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Phones[0].Phase != "error" || !strings.Contains(status.Phones[0].Message, "restarting") || status.Phones[1].Phase == "error" {
		t.Fatal("missing scoped uncertainty", status)
	}
	ack := func(manager *Direct, identity directIdentityResponse) int {
		return directTestRequest(manager, "/v2/direct/ack", map[string]any{"clientId": identity.ClientID, "pairingId": identity.PairingID, "generation": identity.Generation}, &identity).Code
	}
	if ack(m, first) != 503 {
		t.Fatal("failed removal still admitted target")
	}
	store.setFail(false)
	if ack(m, sibling) != 200 {
		t.Fatal("failed removal disabled sibling")
	}
	restarted := NewDirect(m.repository, "windows", "amd64", "test")
	if ack(restarted, first) != 200 {
		t.Fatal("test must demonstrate failed persistence retained the old identity after restart")
	}
	if err := restarted.RevokePhone(ctx, firstID); err != nil {
		t.Fatal(err)
	}
	durable := NewDirect(m.repository, "windows", "amd64", "test")
	if ack(durable, first) != 401 || ack(durable, sibling) != 200 {
		t.Fatal("successful retry did not durably isolate revocation")
	}
}

func TestPhoneLabelsAndExpiredReservation(t *testing.T) {
	ctx := context.Background()
	m, _, _ := directTestConfigured(t)
	status, _ := m.Phones(ctx)
	id := status.PendingPhoneID
	for _, name := range []string{"", " \t ", "bad\nname", strings.Repeat("界", 65)} {
		if m.RenamePhone(ctx, id, name) == nil {
			t.Fatalf("invalid name accepted %q", name)
		}
	}
	if err := m.RenamePhone(ctx, id, "  "+strings.Repeat("界", 64)+"  "); err != nil {
		t.Fatal(err)
	}
	if err := m.RenamePhone(ctx, "unknown", "valid"); err == nil {
		t.Fatal("unknown ID renamed")
	}
	if _, err := m.PhoneProxy(ctx, id, "unknown"); err == nil {
		t.Fatal("unknown proxy format")
	}
	m.state.Phones[0].Invitation.ExpiresAt = time.Now().Add(-time.Minute)
	if err := m.saveLocked(ctx, m.state); err != nil {
		t.Fatal(err)
	}
	status, err := m.Phones(ctx)
	if err != nil || len(status.Phones) != 0 {
		t.Fatal("expired unredeemed slot retained", err)
	}
	fresh, err := m.AddPhone(ctx, "")
	if err != nil || fresh.PhoneID == id {
		t.Fatal("expired identity reused", err)
	}
}

func TestPhoneInvitationSurvivesEndpointChangesBeforeRedemption(t *testing.T) {
	ctx := context.Background()
	m, invitation, csr := directTestConfigured(t)
	id := m.state.Phones[0].ID
	if err := m.Configure(ctx, DirectConfiguration{Endpoint: "https://new.example", DisplayName: "Client"}); err != nil {
		t.Fatal(err)
	}
	resumed, err := m.AddPhone(ctx, "")
	if err != nil || resumed.PhoneID != id {
		t.Fatal("lost pending reservation", err)
	}
	identity := directTestEnroll(t, m, invitation, csr)
	if identity.Generation != 1 {
		t.Fatal("old invitation issued an unreachable generation", identity.Generation)
	}
	directTestAck(t, m, identity)
	if !m.Status().Paired || !m.Status().UpdatePending {
		t.Fatal("old endpoint ACK pretended update was reached")
	}
	bundle, err := m.ExportPhoneEndpointUpdate(ctx, id)
	if err != nil || directEndpointFromUpdate(t, bundle) != "https://new.example" {
		t.Fatal("missing recovery update", err)
	}
}

func TestPhoneRevokedV2MigrationRotatesCredentials(t *testing.T) {
	ctx := context.Background()
	m, i, csr := directTestConfigured(t)
	identity := directTestEnroll(t, m, i, csr)
	directTestAck(t, m, identity)
	old := m.cloneLocked()
	p := old.Phones[0]
	old.Version = 2
	old.Phones = nil
	old.Pairing = p.Pairing
	old.Pairing.Revoked = true
	old.Invitation = nil
	old.Username = p.Username
	old.Password = p.Password
	raw, _ := json.Marshal(old)
	store := securestore.NewMemoryStore()
	_ = store.Put(ctx, directStateKey, raw)
	restarted := NewDirect(NewRepository(store), "windows", "amd64", "test")
	add, err := restarted.AddPhone(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if phoneByID(restarted.state, add.PhoneID).Password == p.Password {
		t.Fatal("removed v2 credentials reused")
	}
}

func TestPhoneFailedExpirationStillListsHealthySibling(t *testing.T) {
	ctx := context.Background()
	m, i, csr := directTestConfigured(t)
	identity := directTestEnroll(t, m, i, csr)
	directTestAck(t, m, identity)
	added, err := m.AddPhone(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	next := m.cloneLocked()
	phoneByID(next, added.PhoneID).Invitation.ExpiresAt = time.Now().Add(-time.Minute)
	if m.saveLocked(ctx, next) != nil {
		t.Fatal("fixture save")
	}
	store := &directFailingStore{Store: m.repository.store}
	m.repository.store = store
	store.setFail(true)
	status, err := m.Phones(ctx)
	if err != nil || len(status.Phones) != 2 || !status.Phones[0].Paired || status.Phones[0].Phase == "error" || status.Phones[1].Phase != "error" {
		t.Fatal("failed expiration hid sibling or uncertainty", status, err)
	}
	if _, err := m.AddPhone(ctx, ""); err == nil {
		t.Fatal("failed expiration reused an uncommitted slot")
	}
}

func TestPhoneMixedGenerationRecoveryAndSerialRetirementAreIndependent(t *testing.T) {
	ctx := context.Background()
	m, i, csr := directTestConfigured(t)
	first := directTestEnroll(t, m, i, csr)
	directTestAck(t, m, first)
	firstID := m.state.Phones[0].ID
	added, err := m.AddPhone(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(added.Bundle)
	var inv directInvitation
	_ = json.Unmarshal(raw, &inv)
	second := directTestEnroll(t, m, inv, csr)
	directTestAck(t, m, second)
	if err := m.Configure(ctx, DirectConfiguration{Endpoint: "https://middle.example", DisplayName: "Client"}); err != nil {
		t.Fatal(err)
	}
	ack := map[string]any{"clientId": first.ClientID, "pairingId": first.PairingID, "generation": uint64(2)}
	if w := directAuthorityRequest(t, m, "POST", "/v2/direct/ack", "middle.example", ack, first); w.Code != 200 {
		t.Fatal(w.Code)
	}
	third, err := m.AddPhone(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Configure(ctx, DirectConfiguration{Endpoint: "https://latest.example", DisplayName: "Client"}); err != nil {
		t.Fatal(err)
	}
	restored := NewDirect(m.repository, "windows", "amd64", "test")
	if _, err := restored.Phones(ctx); err != nil {
		t.Fatal(err)
	}
	cert := restored.tlsConfig.Load().Certificates[0].Leaf
	for _, host := range []string{"client.example", "middle.example", "latest.example"} {
		if cert.VerifyHostname(host) != nil {
			t.Fatal("lost needed shared SAN", host)
		}
	}
	status, _ := restored.Phones(ctx)
	if !status.Phones[0].UpdatePending || !status.Phones[1].UpdatePending || status.PendingPhoneID != third.PhoneID {
		t.Fatal("mixed generations lost", status)
	}
	for _, p := range []struct{ id, pair string }{{firstID, first.PairingID}, {added.PhoneID, second.PairingID}} {
		bundle, err := restored.ExportPhoneEndpointUpdate(ctx, p.id)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := base64.RawURLEncoding.DecodeString(bundle)
		var wrapper struct{ Payload string }
		_ = json.Unmarshal(raw, &wrapper)
		payload, _ := base64.RawURLEncoding.DecodeString(wrapper.Payload)
		var update struct {
			PairingID string `json:"pairingId"`
		}
		_ = json.Unmarshal(payload, &update)
		if update.PairingID != p.pair {
			t.Fatal("signed update targets sibling")
		}
	}
	renew := map[string]any{"clientId": first.ClientID, "pairingId": first.PairingID, "csrPem": csr}
	w := directTestRequest(restored, "/v2/direct/renew", renew, &first)
	if w.Code != 201 {
		t.Fatal(w.Code)
	}
	var issued directIdentityResponse
	_ = json.Unmarshal(w.Body.Bytes(), &issued)
	if issued.Generation != 2 {
		t.Fatal("renewal used sibling generation")
	}
	if w := directAuthorityRequest(t, restored, "POST", "/v2/direct/ack", "middle.example", map[string]any{"clientId": first.ClientID, "pairingId": first.PairingID, "generation": 2}, issued); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := directAuthorityRequest(t, restored, "GET", "/v2/direct/config", "client.example:8443", nil, first); w.Code != 401 {
		t.Fatal("retired serial admitted", w.Code)
	}
	if w := directAuthorityRequest(t, restored, "GET", "/v2/direct/config", "client.example:8443", nil, second); w.Code != 200 {
		t.Fatal("sibling serial retired", w.Code)
	}
}
