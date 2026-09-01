//go:build darwin

package desktop

import (
	"context"
	"fmt"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/runtime"
	"mobile-egress/windows-client/internal/client"
	"mobile-egress/windows-client/internal/securestore"
	"mobile-egress/windows-client/internal/tailscale"
)

func Run() error {
	application, err := newDarwinDesktopApp()
	if err != nil {
		showFatal(err)
		return err
	}
	err = wails.Run(newWailsOptions(application, platformMacOS))
	if err != nil {
		application.shutdownApp()
		showFatal(err)
	}
	return err
}

func newDarwinDesktopApp() (*DesktopApp, error) {
	store, err := newDarwinSecureStore()
	if err != nil {
		return nil, err
	}
	tailscaleController := tailscale.NewDarwinController(tailscale.ExecRunner{})
	return newDesktopApp(context.Background(), desktopControllerConfig{
		Platform:         platformMacOS,
		Store:            store,
		Gateway:          client.DefaultGateway{},
		Tailscale:        tailscaleController,
		TailscaleInstall: tailscale.NewDarwinInstaller(),
		BrowserOpenURL:   runtime.BrowserOpenURL,
		RelayServiceState: func() relayServiceState {
			return relayServiceNotRegistered
		},
		Native: newDarwinDesktopNative(),
	})
}

func newDarwinSecureStore() (securestore.Store, error) {
	return securestore.NewKeychainStore()
}

func showFatal(err error) {
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
	}
}
