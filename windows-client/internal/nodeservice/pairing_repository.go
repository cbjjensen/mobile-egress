package nodeservice

import (
	"context"
	"errors"

	"mobile-egress/internal/clientcontrol"
	"mobile-egress/pairing"
)

// PairingState is service-private. Only safe progress is exposed through IPC.
type PairingState struct {
	Bootstrap              *clientcontrol.Bootstrap  `json:"bootstrap,omitempty"`
	Revoked                bool                      `json:"revoked,omitempty"`
	NodeID                 string                    `json:"nodeId"`
	EnrollmentID           string                    `json:"enrollmentId"`
	Invitation             *clientcontrol.Invitation `json:"invitation,omitempty"`
	AcknowledgedGeneration uint64                    `json:"acknowledgedGeneration"`
}

// PairingBootstrap retains the exact public bootstrap bound by the relay, even
// when a signed service upgrade occurs during an interrupted enrollment.
func (repository *Repository) PairingBootstrap(ctx context.Context, platform, architecture, version string) (clientcontrol.Bootstrap, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	state, err := repository.load(ctx)
	if err != nil {
		return clientcontrol.Bootstrap{}, err
	}
	if state.Pairing == nil {
		return clientcontrol.Bootstrap{}, errors.New("Client is not pairing")
	}
	if state.Pairing.Bootstrap != nil {
		return *state.Pairing.Bootstrap, nil
	}
	bootstrap := clientcontrol.Bootstrap{CSRPEM: state.CSRPEM, ConfigurationPublicKey: state.ConfigurationPublicKey, Platform: platform, Architecture: architecture, ServiceVersion: version}
	if err := bootstrap.Validate(); err != nil {
		return clientcontrol.Bootstrap{}, err
	}
	state.Pairing.Bootstrap = &bootstrap
	if err := repository.save(ctx, state); err != nil {
		return clientcontrol.Bootstrap{}, err
	}
	return bootstrap, nil
}

func (repository *Repository) Revoke(ctx context.Context) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	state, err := repository.load(ctx)
	if err != nil {
		return err
	}
	if state.Pairing == nil {
		return errors.New("Client is not paired")
	}
	state.Pairing.Revoked = true
	return repository.save(ctx, state)
}

func (repository *Repository) BeginPairing(ctx context.Context, invitation clientcontrol.Invitation) error {
	// Bootstrap must commit both private keys before any public network request.
	if _, err := repository.Bootstrap(ctx); err != nil {
		return err
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	state, err := repository.load(ctx)
	if err != nil {
		return err
	}
	if state.Configuration != nil {
		return errors.New("Client is already configured")
	}
	if state.Pairing != nil && state.Pairing.EnrollmentID == invitation.ID && state.Pairing.Invitation != nil {
		previous := *state.Pairing.Invitation
		candidate := invitation
		previous.ExpiresAt = previous.ExpiresAt.UTC()
		candidate.ExpiresAt = candidate.ExpiresAt.UTC()
		previous.RelayURL = candidate.RelayURL
		if previous != candidate {
			return errors.New("invitation changed during pairing")
		}
		if state.Pairing.Invitation.RelayURL != invitation.RelayURL {
			if _, err := pairing.RelayOrigin(invitation.RelayURL); err != nil {
				return errors.New("invitation relay origin is invalid")
			}
			state.Pairing.Invitation = &invitation
			return repository.save(ctx, state)
		}
		return nil
	}
	state.Pairing = &PairingState{NodeID: invitation.NodeID, EnrollmentID: invitation.ID, Invitation: &invitation}
	return repository.save(ctx, state)
}

func (repository *Repository) Pairing(ctx context.Context) (PairingState, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	state, err := repository.load(ctx)
	if err != nil {
		return PairingState{}, err
	}
	if state.Pairing == nil {
		return PairingState{}, errors.New("Client is not paired")
	}
	return *state.Pairing, nil
}

func (repository *Repository) Acknowledged(ctx context.Context, generation uint64) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	state, err := repository.load(ctx)
	if err != nil {
		return err
	}
	if state.Pairing == nil || state.Configuration == nil || generation != state.Configuration.Generation {
		return errors.New("acknowledgement does not match configuration")
	}
	state.Pairing.AcknowledgedGeneration = generation
	state.Pairing.Invitation = nil
	return repository.save(ctx, state)
}
