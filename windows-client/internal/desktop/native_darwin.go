//go:build darwin && cgo && !bindings

package desktop

import (
	"context"
	_ "embed"
	"sync"
	"time"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed zfnf-menu-bar.png
var darwinMenuBarIcon []byte

type wailsDesktopNative struct {
	mu   sync.Mutex
	menu *desktopMenuBarController
}

func newDarwinDesktopNative() *wailsDesktopNative { return &wailsDesktopNative{} }

func (native *wailsDesktopNative) StartTray(app *DesktopApp) {
	native.mu.Lock()
	if native.menu != nil {
		native.mu.Unlock()
		return
	}
	ticker := time.NewTicker(2 * time.Second)
	menu := newDesktopMenuBarController(
		newCocoaMenuBar(),
		menuBarConfig{
			Icon:          append([]byte(nil), darwinMenuBarIcon...),
			Tooltip:       desktopDisplayName,
			InitialStatus: "Bridge status unavailable",
			ShowTitle:     "Show " + desktopDisplayName,
			ShowTooltip:   "Open the controller window",
			QuitTitle:     "Quit controller",
			QuitTooltip:   "Close the controller; the background relay keeps running",
		},
		app.GetBridgeStatus,
		func() {
			if ctx := app.runtimeContext(); ctx != nil {
				app.showWindow(ctx)
			}
		},
		app.Quit,
		ticker.C,
		ticker.Stop,
	)
	native.menu = menu
	native.mu.Unlock()
	menu.Start()
}

func (native *wailsDesktopNative) StopTray() {
	native.mu.Lock()
	menu := native.menu
	native.mu.Unlock()
	if menu != nil {
		menu.Stop()
	}
}

func (*wailsDesktopNative) HideWindow(ctx context.Context) { wailsruntime.WindowHide(ctx) }

func (*wailsDesktopNative) ShowWindow(ctx context.Context) {
	wailsruntime.WindowUnminimise(ctx)
	wailsruntime.WindowShow(ctx)
}

func (*wailsDesktopNative) Quit(ctx context.Context) { wailsruntime.Quit(ctx) }
