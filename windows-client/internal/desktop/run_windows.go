//go:build windows

// Package desktop hosts the thin Wails and tray shell around the testable core.
package desktop

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/getlantern/systray"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	"github.com/wailsapp/wails/v2/pkg/runtime"
	windowssys "golang.org/x/sys/windows"
	"mobile-egress/pairing"
	"mobile-egress/windows-client/internal/assets"
	"mobile-egress/windows-client/internal/client"
	"mobile-egress/windows-client/internal/prerequisites"
	"mobile-egress/windows-client/internal/securestore"
)

type DesktopApp struct {
	core *client.Core

	mu       sync.RWMutex
	ctx      context.Context
	quitting atomic.Bool
	shutdown sync.Once
}

type PairingView struct {
	Bundle    string `json:"bundle"`
	Role      string `json:"role"`
	ExpiresAt string `json:"expiresAt"`
}

func Run() error {
	if err := prerequisites.CheckWebView2Installed(); err != nil {
		showFatal(err)
		return err
	}
	configDirectory, err := os.UserConfigDir()
	if err != nil {
		return fmt.Errorf("locate Windows configuration directory: %w", err)
	}
	store, err := securestore.NewDPAPIStore(filepath.Join(configDirectory, "MobileEgress", "secure"))
	if err != nil {
		return err
	}
	core, err := client.NewCore(context.Background(), store, client.DefaultGateway{})
	if err != nil {
		return err
	}
	application := &DesktopApp{core: core}
	err = wails.Run(&options.App{
		Title: "Mobile Egress", Width: 880, Height: 660, MinWidth: 720, MinHeight: 540,
		BackgroundColour: options.NewRGB(15, 20, 28),
		AssetServer:      &assetserver.Options{Assets: assets.Files()},
		OnStartup:        application.startup, OnShutdown: application.onShutdown,
		OnBeforeClose: application.beforeClose, Bind: []interface{}{application},
		Windows:                          &windows.Options{WebviewIsTransparent: false, WindowIsTranslucent: false},
		EnableDefaultContextMenu:         false,
		EnableFraudulentWebsiteDetection: false,
	})
	if err != nil {
		application.shutdownApp()
		showFatal(err)
	}
	return err
}

func (app *DesktopApp) startup(ctx context.Context) {
	app.mu.Lock()
	app.ctx = ctx
	app.mu.Unlock()
	go systray.Run(app.trayReady, func() {})
}

func (app *DesktopApp) onShutdown(context.Context) { app.shutdownApp() }

func (app *DesktopApp) beforeClose(ctx context.Context) bool {
	if app.quitting.Load() {
		return false
	}
	runtime.WindowHide(ctx)
	return true
}

func (app *DesktopApp) GetStatus() client.Status { return app.core.Status() }

func (app *DesktopApp) Pair(encodedBundle string) error {
	bundle, err := pairing.Decode(encodedBundle)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	return app.core.Pair(ctx, bundle)
}

func (app *DesktopApp) StartProxy(port uint16) error { return app.core.StartProxy(port) }

func (app *DesktopApp) StopProxy() error { return app.core.StopProxy() }

func (app *DesktopApp) ProxyLine() (string, error) { return app.core.ProxyLine() }

func (app *DesktopApp) IssuePairing(role string) (PairingView, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	result, err := app.core.IssuePairing(ctx, role)
	if err != nil {
		return PairingView{}, err
	}
	encoded, err := pairing.Encode(result)
	if err != nil {
		return PairingView{}, err
	}
	return PairingView{Bundle: encoded, Role: result.Role, ExpiresAt: result.ExpiresAt.UTC().Format(time.RFC3339)}, nil
}

func (app *DesktopApp) Revoke(serial string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return app.core.Revoke(ctx, serial)
}

func (app *DesktopApp) Quit() {
	app.quitting.Store(true)
	app.shutdownApp()
	if ctx := app.runtimeContext(); ctx != nil {
		runtime.Quit(ctx)
	}
}

func (app *DesktopApp) trayReady() {
	systray.SetIcon(trayIcon())
	systray.SetTooltip("Mobile Egress")
	statusItem := systray.AddMenuItem("Relay offline", "Aggregate relay health")
	statusItem.Disable()
	showItem := systray.AddMenuItem("Show Mobile Egress", "Open the client window")
	toggleItem := systray.AddMenuItem("Start proxy", "Start or stop the loopback SOCKS proxy")
	systray.AddSeparator()
	quitItem := systray.AddMenuItem("Quit", "Stop the proxy and quit")

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-showItem.ClickedCh:
			if ctx := app.runtimeContext(); ctx != nil {
				runtime.WindowShow(ctx)
				runtime.WindowUnminimise(ctx)
			}
		case <-toggleItem.ClickedCh:
			status := app.core.Status()
			if status.Running {
				_ = app.core.StopProxy()
			} else if status.Paired && status.Role == "client" {
				_ = app.core.StartProxy(status.Port)
			}
		case <-quitItem.ClickedCh:
			app.Quit()
			return
		case <-ticker.C:
			status := app.core.Status()
			if status.Relay == "connected" && status.AgentAvailable {
				statusItem.SetTitle("Relay connected · agent ready")
			} else if status.Relay == "connected" {
				statusItem.SetTitle("Relay connected · agent offline")
			} else {
				statusItem.SetTitle("Relay offline")
			}
			if status.Running {
				toggleItem.SetTitle("Stop proxy")
			} else {
				toggleItem.SetTitle("Start proxy")
			}
		}
	}
}

func (app *DesktopApp) runtimeContext() context.Context {
	app.mu.RLock()
	defer app.mu.RUnlock()
	return app.ctx
}

func (app *DesktopApp) shutdownApp() {
	app.shutdown.Do(func() {
		_ = app.core.Close()
		systray.Quit()
	})
}

func trayIcon() []byte {
	const (
		width      = 16
		height     = 16
		pixelBytes = width * height * 4
		maskBytes  = height * 4
		imageBytes = 40 + pixelBytes + maskBytes
	)
	icon := make([]byte, 6+16+imageBytes)
	binary.LittleEndian.PutUint16(icon[2:4], 1)
	binary.LittleEndian.PutUint16(icon[4:6], 1)
	icon[6], icon[7] = width, height
	binary.LittleEndian.PutUint16(icon[10:12], 1)
	binary.LittleEndian.PutUint16(icon[12:14], 32)
	binary.LittleEndian.PutUint32(icon[14:18], imageBytes)
	binary.LittleEndian.PutUint32(icon[18:22], 22)
	dib := icon[22:]
	binary.LittleEndian.PutUint32(dib[0:4], 40)
	binary.LittleEndian.PutUint32(dib[4:8], width)
	binary.LittleEndian.PutUint32(dib[8:12], height*2)
	binary.LittleEndian.PutUint16(dib[12:14], 1)
	binary.LittleEndian.PutUint16(dib[14:16], 32)
	binary.LittleEndian.PutUint32(dib[20:24], pixelBytes)
	for index := 0; index < width*height; index++ {
		offset := 40 + index*4
		dib[offset], dib[offset+1], dib[offset+2], dib[offset+3] = 215, 139, 61, 255
	}
	return icon
}

func showFatal(err error) {
	if err == nil {
		return
	}
	message, messageErr := windowssys.UTF16PtrFromString(err.Error())
	caption, captionErr := windowssys.UTF16PtrFromString("Mobile Egress")
	if messageErr == nil && captionErr == nil {
		_, _ = windowssys.MessageBox(0, message, caption, windowssys.MB_OK|windowssys.MB_ICONERROR)
	}
	_, _ = fmt.Fprintln(os.Stderr, err)
}
