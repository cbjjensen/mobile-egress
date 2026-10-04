//go:build windows || darwin

package main

import (
	"context"
	"mobile-egress/windows-client/internal/clientapp"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

func runApp() error {
	assets, err := clientapp.Assets()
	if err != nil {
		return err
	}
	var appContext context.Context
	app := clientapp.NewWithBrowser(clientapp.LocalClient{}, func(value string) error { return runtime.ClipboardSetText(appContext, value) }, func(raw string) error { runtime.BrowserOpenURL(appContext, raw); return nil })
	return wails.Run(&options.App{Title: "Mobile Egress Client", Width: 940, Height: 760, MinWidth: 620, MinHeight: 650, AssetServer: &assetserver.Options{Assets: assets}, OnStartup: func(ctx context.Context) { appContext = ctx }, Bind: []interface{}{app}})
}
