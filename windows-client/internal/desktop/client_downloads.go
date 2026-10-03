package desktop

// OpenClientDownloads uses the native browser so the installer link works in
// both embedded WebViews. Callers cannot supply an arbitrary destination.
func (app *DesktopApp) OpenClientDownloads() error {
	return app.openBrowserURL("https://github.com/cbjjensen/mobile-egress/releases", "Client downloads page")
}
