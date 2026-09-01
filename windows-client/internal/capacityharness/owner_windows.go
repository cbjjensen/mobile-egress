//go:build capacityharness && windows

package capacityharness

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"mobile-egress/windows-client/internal/client"
	"mobile-egress/windows-client/internal/relayclient"
	"mobile-egress/windows-client/internal/securestore"
)

// ProtectedOwnerLoader follows the same per-user DPAPI repository path as the
// production Windows controller. It accepts no caller-selected state path.
type ProtectedOwnerLoader struct{}

func (ProtectedOwnerLoader) LoadOwner(ctx context.Context) (relayclient.Identity, error) {
	configurationDirectory, err := os.UserConfigDir()
	if err != nil || configurationDirectory == "" {
		return relayclient.Identity{}, errors.New("protected Owner repository is unavailable")
	}
	store, err := securestore.NewDPAPIStore(filepath.Join(configurationDirectory, "MobileEgress", "secure"))
	if err != nil {
		return relayclient.Identity{}, errors.New("protected Owner repository is unavailable")
	}
	identity, _, err := client.NewRepository(store).LoadOwnerIdentity(ctx)
	if err != nil {
		return relayclient.Identity{}, errors.New("protected Owner identity is unavailable")
	}
	return identity, nil
}
