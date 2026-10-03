package nodeservice

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"mobile-egress/internal/clientcontrol"
	"mobile-egress/windows-client/internal/relayclient"
	"mobile-egress/windows-client/internal/securestore"
)

type pendingPollEnrollment struct {
	invitation clientcontrol.Invitation
	bootstrap  clientcontrol.Bootstrap
	polled     bool
}

func (remote *pendingPollEnrollment) Submit(context.Context, clientcontrol.Invitation, clientcontrol.Bootstrap) (clientcontrol.Enrollment, error) {
	return clientcontrol.Enrollment{}, errors.New("expired recovery must poll its existing binding")
}
func (remote *pendingPollEnrollment) Poll(_ context.Context, invitation clientcontrol.Invitation, bootstrap clientcontrol.Bootstrap) (clientcontrol.Enrollment, error) {
	remote.invitation = invitation
	remote.bootstrap = bootstrap
	remote.polled = true
	return clientcontrol.Enrollment{ID: invitation.ID, NodeID: invitation.NodeID, State: "approved"}, nil
}
func (*pendingPollEnrollment) Report(context.Context, relayclient.Identity, string, uint64, string) error {
	return nil
}

func expiredInvitationForTest(t *testing.T, repository *Repository) clientcontrol.Invitation {
	t.Helper()
	bootstrap, err := repository.Bootstrap(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	configuration := signedNodeConfig(t, bootstrap.CSRPEM, "https://old.example.ts.net:8443", "user", "password")
	return clientcontrol.Invitation{Version: 1, Type: clientcontrol.InvitationType, ID: "enrollment", NodeID: "node", DisplayName: "My Client", RelayURL: configuration.RelayURL, CACertificatePEM: configuration.CACertificatePEM, Capability: base64.RawURLEncoding.EncodeToString(make([]byte, 32)), ExpiresAt: time.Now().Add(-time.Minute).UTC()}
}
func invitationBundleForTest(t *testing.T, value clientcontrol.Invitation) string {
	t.Helper()
	bundle, err := clientcontrol.EncodeInvitation(value)
	if err != nil {
		t.Fatal(err)
	}
	return bundle
}

func TestExpiredBoundInvitationResumesOnlyItsOriginAndPollsOriginalKeys(t *testing.T) {
	ctx := context.Background()
	repository := NewRepository(securestore.NewMemoryStore())
	invitation := expiredInvitationForTest(t, repository)
	if err := repository.BeginPairing(ctx, invitation); err != nil {
		t.Fatal(err)
	}
	bootstrap, err := repository.PairingBootstrap(ctx, "windows", "amd64", "1.0")
	if err != nil {
		t.Fatal(err)
	}
	remote := &pendingPollEnrollment{}
	manager := NewStandalone(repository, &fakeDialer{}, remote, "windows", "amd64", "1.1")
	moved := invitation
	moved.RelayURL = "https://new.example.ts.net:8443"
	if err := manager.Pair(ctx, invitationBundleForTest(t, moved)); err != nil {
		t.Fatalf("bound expired pairing could not resume its origin: %v", err)
	}
	saved, _ := repository.Pairing(ctx)
	if saved.Invitation.RelayURL != moved.RelayURL || !saved.Invitation.ExpiresAt.Equal(invitation.ExpiresAt) || *saved.Bootstrap != bootstrap {
		t.Fatal("resume changed authority, expiry, or bound keys")
	}
	manager.step(ctx)
	if !remote.polled || remote.invitation.RelayURL != moved.RelayURL || remote.bootstrap != bootstrap || manager.Status().Phase != "pairing" {
		t.Fatal("expired bound recovery did not poll the original binding at the new origin")
	}
	otherCA := expiredInvitationForTest(t, NewRepository(securestore.NewMemoryStore())).CACertificatePEM
	for _, change := range []func(*clientcontrol.Invitation){
		func(value *clientcontrol.Invitation) { value.ID = "other-enrollment" },
		func(value *clientcontrol.Invitation) { value.NodeID = "other-node" },
		func(value *clientcontrol.Invitation) {
			value.Capability = base64.RawURLEncoding.EncodeToString([]byte("12345678901234567890123456789012"))
		},
		func(value *clientcontrol.Invitation) { value.ExpiresAt = value.ExpiresAt.Add(time.Second) },
		func(value *clientcontrol.Invitation) { value.CACertificatePEM = otherCA },
	} {
		changed := moved
		change(&changed)
		if err := manager.Pair(ctx, invitationBundleForTest(t, changed)); err == nil {
			t.Fatal("expired recovery accepted a different invitation or authority")
		}
	}
}

func TestExpiredRecoveryRequiresThePersistedBootstrapToMatchLocalKeys(t *testing.T) {
	ctx := context.Background()
	repository := NewRepository(securestore.NewMemoryStore())
	invitation := expiredInvitationForTest(t, repository)
	if err := repository.BeginPairing(ctx, invitation); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.PairingBootstrap(ctx, "windows", "amd64", "1.0"); err != nil {
		t.Fatal(err)
	}
	other, _ := NewRepository(securestore.NewMemoryStore()).Bootstrap(ctx)
	state, err := repository.load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	state.Pairing.Bootstrap.ConfigurationPublicKey = other.ConfigurationPublicKey
	if err := repository.save(ctx, state); err != nil {
		t.Fatal(err)
	}
	remote := &pendingPollEnrollment{}
	manager := NewStandalone(repository, &fakeDialer{}, remote, "windows", "amd64", "1.0")
	if err := manager.Pair(ctx, invitationBundleForTest(t, invitation)); err == nil {
		t.Fatal("expired recovery accepted a bootstrap for different local keys")
	}
	manager.step(ctx)
	if remote.polled {
		t.Fatal("polled a bootstrap that does not match private local keys")
	}
}

func TestExpiredRecoveryCannotReplaceAConfiguredClient(t *testing.T) {
	ctx := context.Background()
	repository := NewRepository(securestore.NewMemoryStore())
	invitation := expiredInvitationForTest(t, repository)
	bootstrap, _ := repository.Bootstrap(ctx)
	configuration := signedNodeConfig(t, bootstrap.CSRPEM, invitation.RelayURL, "user", "password")
	invitation.CACertificatePEM = configuration.CACertificatePEM
	if err := repository.BeginPairing(ctx, invitation); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.PairingBootstrap(ctx, "windows", "amd64", "1.0"); err != nil {
		t.Fatal(err)
	}
	applyNodeConfig(t, repository, bootstrap.ConfigurationPublicKey, configuration)
	manager := NewStandalone(repository, &fakeDialer{}, &pendingPollEnrollment{}, "windows", "amd64", "1.0")
	if err := manager.Pair(ctx, invitationBundleForTest(t, invitation)); err == nil {
		t.Fatal("expired recovery accepted an already configured Client")
	}
}

func TestExpiredInvitationCannotStartFreshOrUnboundPairing(t *testing.T) {
	ctx := context.Background()
	fixture := NewRepository(securestore.NewMemoryStore())
	invitation := expiredInvitationForTest(t, fixture)
	bundle := invitationBundleForTest(t, invitation)
	for _, pending := range []bool{false, true} {
		store := securestore.NewMemoryStore()
		repository := NewRepository(store)
		if pending {
			if err := repository.BeginPairing(ctx, invitation); err != nil {
				t.Fatal(err)
			}
		}
		remote := &pendingPollEnrollment{}
		manager := NewStandalone(repository, &fakeDialer{}, remote, "windows", "amd64", "1.0")
		if err := manager.Pair(ctx, bundle); err == nil {
			t.Fatal("expired invitation started an unbound pairing")
		}
		if pending {
			manager.step(ctx)
			if remote.polled || manager.Status().Phase != "expired" {
				t.Fatal("unbound expired invitation was polled")
			}
		} else {
			if _, err := store.Get(ctx, stateKey); !errors.Is(err, securestore.ErrNotFound) {
				t.Fatal("fresh expired invitation created private state")
			}
		}
	}
}
