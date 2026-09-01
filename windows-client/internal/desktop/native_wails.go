//go:build windows && !bindings

package desktop

import (
	_ "embed"
	"time"

	"github.com/getlantern/systray"
)

//go:embed zfnf-logo.ico
var zfnfTrayIcon []byte

func (app *DesktopApp) trayReady() {
	systray.SetIcon(trayIcon())
	systray.SetTooltip(desktopDisplayName)
	statusItem := systray.AddMenuItem("Bridge status unavailable", "Local relay and Funnel status")
	statusItem.Disable()
	showItem := systray.AddMenuItem("Show "+desktopDisplayName, "Open the controller window")
	systray.AddSeparator()
	quitTooltip := "Close the controller; Windows services keep running"
	quitItem := systray.AddMenuItem("Quit controller", quitTooltip)

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-showItem.ClickedCh:
			if ctx := app.runtimeContext(); ctx != nil {
				app.showWindow(ctx)
			}
		case <-quitItem.ClickedCh:
			app.Quit()
			return
		case <-ticker.C:
			statusItem.SetTitle(menuBarStatusTitle(app.GetBridgeStatus()))
		}
	}
}

func trayIcon() []byte { return append([]byte(nil), zfnfTrayIcon...) }
