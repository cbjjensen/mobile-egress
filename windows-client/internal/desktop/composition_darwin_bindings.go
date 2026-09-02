//go:build darwin && bindings

package desktop

import (
	"context"
	"errors"

	"mobile-egress/pairing"
	"mobile-egress/windows-client/internal/client"
	"mobile-egress/windows-client/internal/relayclient"
	"mobile-egress/windows-client/internal/securestore"
)

var errDarwinBindingsOperation = errors.New("desktop operations are unavailable during Wails bindings generation")

type darwinBindingsGateway struct{}

func (darwinBindingsGateway) Enroll(context.Context, pairing.Bundle) (relayclient.Identity, error) {
	return relayclient.Identity{}, errDarwinBindingsOperation
}

func (darwinBindingsGateway) DialSession(context.Context, relayclient.Identity) (client.Tunnel, error) {
	return nil, errDarwinBindingsOperation
}

func (darwinBindingsGateway) IssuePairing(context.Context, relayclient.Identity, string) (relayclient.PairingCode, error) {
	return relayclient.PairingCode{}, errDarwinBindingsOperation
}

func (darwinBindingsGateway) Revoke(context.Context, relayclient.Identity, string) error {
	return errDarwinBindingsOperation
}

func newDarwinDesktopApp() (*DesktopApp, error) {
	return newDesktopApp(context.Background(), desktopControllerConfig{
		Platform: platformMacOS,
		Store:    securestore.NewMemoryStore(),
		Gateway:  darwinBindingsGateway{},
		BrowserOpenURL: func(context.Context, string) {
		},
		RelayServiceState: func() relayServiceState {
			return relayServiceUnavailable
		},
		Native: newDarwinDesktopNative(),
	})
}
