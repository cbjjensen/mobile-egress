//go:build bindings

package desktop

import (
	"context"
	_ "embed"
)

// Wails bindings generation cross-compiles without native window or tray code.
// Production builds never use this adapter.
type wailsDesktopNative struct{}

func newDarwinDesktopNative() wailsDesktopNative { return wailsDesktopNative{} }

func (wailsDesktopNative) StartTray(*DesktopApp)      {}
func (wailsDesktopNative) StopTray()                  {}
func (wailsDesktopNative) HideWindow(context.Context) {}
func (wailsDesktopNative) ShowWindow(context.Context) {}
func (wailsDesktopNative) Quit(context.Context)       {}

//go:embed zfnf-logo.ico
var bindingsTrayIcon []byte

func trayIcon() []byte { return append([]byte(nil), bindingsTrayIcon...) }
