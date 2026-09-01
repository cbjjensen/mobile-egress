//go:build (windows || darwin) && !bindings

package desktop

import (
	_ "embed"
	goruntime "runtime"
	"time"

	"github.com/getlantern/systray"
)

//go:embed zfnf-logo.ico
var zfnfTrayIcon []byte

func (app *DesktopApp) trayReady() {
	if goruntime.GOOS == "darwin" {
		systray.SetTemplateIcon(trayIcon(), trayIcon())
	} else {
		systray.SetIcon(trayIcon())
	}
	systray.SetTooltip(desktopDisplayName)
	statusItem := systray.AddMenuItem("Bridge status unavailable", "Local relay and Funnel status")
	statusItem.Disable()
	showItem := systray.AddMenuItem("Show "+desktopDisplayName, "Open the controller window")
	systray.AddSeparator()
	quitTooltip := "Close the controller; Windows services keep running"
	if app.desktopPlatform() == platformMacOS {
		quitTooltip = "Close the controller; the background relay keeps running"
	}
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
			status := app.GetBridgeStatus()
			switch {
			case status.NeedsRotation:
				statusItem.SetTitle("Funnel endpoint changed · rotation required")
			case status.Ready:
				statusItem.SetTitle("Local relay and Funnel ready")
			case status.TailscaleOnline:
				statusItem.SetTitle("Tailscale online · relay setup required")
			default:
				statusItem.SetTitle("Bridge setup required")
			}
		}
	}
}

func trayIcon() []byte { return append([]byte(nil), zfnfTrayIcon...) }
