package desktop

type existingApplicationLoopTray struct {
	register func(onReady, onExit func())
}

func (tray existingApplicationLoopTray) Start(onReady func()) {
	tray.register(onReady, func() {})
}
