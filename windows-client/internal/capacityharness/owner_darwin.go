//go:build capacityharness && darwin && cgo && !bindings

package capacityharness

import (
	"context"
	"errors"

	"mobile-egress/windows-client/internal/client"
	"mobile-egress/windows-client/internal/relayclient"
	"mobile-egress/windows-client/internal/securestore"
)

// ProtectedOwnerLoader reads the production controller Owner directly from
// its private data-protection Keychain group. The entire capacity run must be
// hosted by the validated signed app; Owner material is never exported to an
// unsigned launcher.
type ProtectedOwnerLoader struct{}

func (ProtectedOwnerLoader) LoadOwner(ctx context.Context) (relayclient.Identity, error) {
	store, err := securestore.NewKeychainStore()
	if err != nil {
		return relayclient.Identity{}, errors.New("protected Owner repository is unavailable")
	}
	identity, _, err := client.NewRepository(store).LoadOwnerIdentity(ctx)
	if err != nil {
		return relayclient.Identity{}, errors.New("protected Owner identity is unavailable")
	}
	return identity, nil
}
