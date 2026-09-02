//go:build darwin && !bindings

package desktop

import (
	"context"

	"github.com/wailsapp/wails/v2/pkg/runtime"
	"mobile-egress/windows-client/internal/client"
	"mobile-egress/windows-client/internal/localbridge"
	"mobile-egress/windows-client/internal/relayservice"
	"mobile-egress/windows-client/internal/securestore"
	"mobile-egress/windows-client/internal/tailscale"
)

func newDarwinDesktopApp() (*DesktopApp, error) {
	store, err := newDarwinSecureStore()
	if err != nil {
		return nil, err
	}
	tailscaleController := tailscale.NewDarwinController(tailscale.ExecRunner{})
	relayService, relayAdmin, err := relayservice.NewDarwin(controllerVersion)
	if err != nil {
		return nil, err
	}
	relayHelper, err := localbridge.NewRelayAdminHelper(relayAdmin)
	if err != nil {
		return nil, err
	}
	return newDesktopApp(context.Background(), desktopControllerConfig{
		Platform:         platformMacOS,
		Store:            store,
		Gateway:          client.DefaultGateway{},
		Tailscale:        tailscaleController,
		TailscaleInstall: tailscale.NewDarwinInstaller(),
		NewBridge: func(controller *tailscale.Controller, owners localbridge.OwnerSink) *localbridge.Manager {
			return localbridge.NewManager(controller, relayHelper, owners)
		},
		BrowserOpenURL: runtime.BrowserOpenURL,
		RelayService:   relayService,
		Native:         newDarwinDesktopNative(),
	})
}

func newDarwinSecureStore() (securestore.Store, error) {
	return securestore.NewKeychainStore()
}
