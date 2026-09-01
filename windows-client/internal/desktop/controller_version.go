package desktop

// ControllerVersion returns the release value injected by the desktop
// packaging pipeline. It also gives the packaged Darwin executable a simple
// post-build identity check without starting Wails.
func ControllerVersion() string { return controllerVersion }
