//go:build darwin

package desktop

import (
	"github.com/wailsapp/wails/v2"
)

func Run() error {
	application, err := newDarwinDesktopApp()
	if err != nil {
		showDarwinFatal(fatalStartup)
		return err
	}
	err = wails.Run(newWailsOptions(application, platformMacOS))
	if err != nil {
		application.shutdownApp()
		showDarwinFatal(fatalRuntime)
	}
	return err
}
