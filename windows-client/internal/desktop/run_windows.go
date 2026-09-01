//go:build windows

package desktop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/runtime"
	windowssys "golang.org/x/sys/windows"
	"mobile-egress/windows-client/internal/client"
	"mobile-egress/windows-client/internal/localbridge"
	"mobile-egress/windows-client/internal/prerequisites"
	"mobile-egress/windows-client/internal/securestore"
	"mobile-egress/windows-client/internal/tailscale"
)

func Run() error {
	if err := prerequisites.CheckWebView2Installed(); err != nil {
		showFatal(err)
		return err
	}
	application, err := newWindowsDesktopApp()
	if err != nil {
		return err
	}
	err = wails.Run(newWailsOptions(application, platformWindows))
	if err != nil {
		application.shutdownApp()
		showFatal(err)
	}
	return err
}

func newWindowsDesktopApp() (*DesktopApp, error) {
	configDirectory, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("locate Windows configuration directory: %w", err)
	}
	store, err := securestore.NewDPAPIStore(filepath.Join(configDirectory, "MobileEgress", "secure"))
	if err != nil {
		return nil, err
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locate Mobile Egress executable: %w", err)
	}
	binDirectory := filepath.Dir(executable)
	tailscaleController := tailscale.NewController(`C:\Program Files\Tailscale\tailscale.exe`, tailscale.ExecRunner{})
	return newDesktopApp(context.Background(), desktopControllerConfig{
		Platform:         platformWindows,
		Store:            store,
		Gateway:          client.DefaultGateway{},
		Tailscale:        tailscaleController,
		TailscaleInstall: tailscale.DefaultInstaller(),
		NewBridge: func(controller *tailscale.Controller, owners localbridge.OwnerSink) *localbridge.Manager {
			return localbridge.NewManager(controller, localbridge.UACHelper{
				AdminExecutable: filepath.Join(binDirectory, "mobile-egress-admin.exe"),
				RelayExecutable: filepath.Join(binDirectory, "mobile-egress-relay.exe"),
			}, owners)
		},
		BrowserOpenURL: runtime.BrowserOpenURL,
		RelayServiceState: func() relayServiceState {
			return relayServiceNotRequired
		},
		Native: wailsDesktopNative{},
	})
}

func showFatal(err error) {
	if err == nil {
		return
	}
	message, messageErr := windowssys.UTF16PtrFromString(err.Error())
	caption, captionErr := windowssys.UTF16PtrFromString(desktopDisplayName)
	if messageErr == nil && captionErr == nil {
		_, _ = windowssys.MessageBox(0, message, caption, windowssys.MB_OK|windowssys.MB_ICONERROR)
	}
	_, _ = fmt.Fprintln(os.Stderr, err)
}
