//go:build darwin && !bindings

package desktop

import (
	"context"

	"github.com/getlantern/systray"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

type wailsDesktopNative struct {
	tray existingApplicationLoopTray
}

func newDarwinDesktopNative() wailsDesktopNative {
	return wailsDesktopNative{
		tray: existingApplicationLoopTray{register: systray.Register},
	}
}

func (native wailsDesktopNative) StartTray(app *DesktopApp) {
	native.tray.Start(func() { app.trayReady() })
}

func (wailsDesktopNative) StopTray() { systray.Quit() }

func (wailsDesktopNative) HideWindow(ctx context.Context) { wailsruntime.WindowHide(ctx) }

func (wailsDesktopNative) ShowWindow(ctx context.Context) {
	wailsruntime.WindowUnminimise(ctx)
	wailsruntime.WindowShow(ctx)
}

func (wailsDesktopNative) Quit(ctx context.Context) { wailsruntime.Quit(ctx) }
