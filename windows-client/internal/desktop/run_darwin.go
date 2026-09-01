//go:build darwin

package desktop

import (
	"context"
	"errors"
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
	tailscaleController := tailscale.NewController(
		"/Applications/Tailscale.app/Contents/MacOS/Tailscale", tailscale.ExecRunner{},
	)
	return newDesktopApp(context.Background(), desktopControllerConfig{
		Platform:         platformMacOS,
		Store:            store,
		Gateway:          client.DefaultGateway{},
		Tailscale:        tailscaleController,
		TailscaleInstall: unsupportedDarwinTailscaleInstaller{},
		BrowserOpenURL:   runtime.BrowserOpenURL,
		RelayServiceState: func() relayServiceState {
			return relayServiceNotRegistered
		},
		Native: newDarwinDesktopNative(),
	})
}

// Task 2 replaces this fail-closed seam with the Security.framework Keychain store.
func newDarwinSecureStore() (securestore.Store, error) {
	return nil, errors.New("macOS Keychain secure storage is not yet supported")
}

type unsupportedDarwinTailscaleInstaller struct{}

func (unsupportedDarwinTailscaleInstaller) Install(context.Context) (tailscale.Release, error) {
	return tailscale.Release{}, errors.New("macOS Tailscale PKG installation is not yet supported")
}

func showFatal(err error) {
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
	}
}
