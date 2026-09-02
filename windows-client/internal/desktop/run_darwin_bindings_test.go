//go:build darwin && bindings

package desktop

import (
	"context"
	"testing"
)

func TestDarwinBindingsCompositionUsesAnInertDesktopApp(t *testing.T) {
	application, err := newDarwinDesktopApp()
	if err != nil {
		t.Fatalf("newDarwinDesktopApp() error = %v, want an in-memory bindings composition", err)
	}
	t.Cleanup(application.shutdownApp)

	if application.core == nil || application.native == nil || application.browserOpenURL == nil {
		t.Fatal("bindings application is missing an in-memory dependency")
	}
	if application.tailscale != nil || application.tailscaleInstall != nil || application.relayService != nil || application.bridge != nil {
		t.Fatal("bindings application retained a native or external dependency")
	}

	application.startup(context.Background())
	view := application.GetBridgeStatus()
	if view.Platform != string(platformMacOS) || view.RelayServiceState != string(relayServiceUnavailable) {
		t.Fatalf("GetBridgeStatus() = %#v, want inert macOS bindings status", view)
	}

	appOptions := newWailsOptions(application, platformMacOS)
	if len(appOptions.Bind) != 1 {
		t.Fatalf("Wails binding count = %d, want the complete DesktopApp surface", len(appOptions.Bind))
	}
	bound, ok := appOptions.Bind[0].(*DesktopApp)
	if !ok || bound != application {
		t.Fatal("Wails binding is not the constructed *DesktopApp")
	}
}
