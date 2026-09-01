//go:build windows && !bindings

package desktop

import (
	"context"

	"github.com/getlantern/systray"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

type wailsDesktopNative struct{}

func (wailsDesktopNative) StartTray(app *DesktopApp) {
	go systray.Run(func() { app.trayReady() }, func() {})
}

func (wailsDesktopNative) StopTray() { systray.Quit() }

func (wailsDesktopNative) HideWindow(ctx context.Context) { wailsruntime.WindowHide(ctx) }

func (wailsDesktopNative) ShowWindow(ctx context.Context) {
	wailsruntime.WindowUnminimise(ctx)
	wailsruntime.WindowShow(ctx)
}

func (wailsDesktopNative) Quit(ctx context.Context) { wailsruntime.Quit(ctx) }
