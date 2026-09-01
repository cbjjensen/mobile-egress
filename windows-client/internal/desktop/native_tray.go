package desktop

import (
	"sync"
	"sync/atomic"
	"time"
)

type menuBarConfig struct {
	Icon          []byte
	Tooltip       string
	InitialStatus string
	ShowTitle     string
	ShowTooltip   string
	QuitTitle     string
	QuitTooltip   string
}

type nativeMenuBar interface {
	Start(menuBarConfig, func(), func())
	SetStatus(string)
	Stop()
}

type desktopMenuBarController struct {
	mu           sync.Mutex
	native       nativeMenuBar
	config       menuBarConfig
	status       func() BridgeView
	show         func()
	quit         func()
	ticks        <-chan time.Time
	stopTicks    func()
	stop         chan struct{}
	pollingDone  chan struct{}
	shutdownDone chan struct{}
	started      bool
	stopped      bool
	active       atomic.Bool
	quitOnce     sync.Once
}

func newDesktopMenuBarController(
	native nativeMenuBar,
	config menuBarConfig,
	status func() BridgeView,
	show func(),
	quit func(),
	ticks <-chan time.Time,
	stopTicks func(),
) *desktopMenuBarController {
	return &desktopMenuBarController{
		native:       native,
		config:       config,
		status:       status,
		show:         show,
		quit:         quit,
		ticks:        ticks,
		stopTicks:    stopTicks,
		stop:         make(chan struct{}),
		pollingDone:  make(chan struct{}),
		shutdownDone: make(chan struct{}),
	}
}

func (controller *desktopMenuBarController) Start() {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if controller.started || controller.stopped {
		return
	}
	controller.started = true
	controller.active.Store(true)
	controller.native.Start(controller.config, controller.showMenu, controller.quitMenu)
	go controller.pollStatus()
}

func (controller *desktopMenuBarController) Stop() {
	controller.mu.Lock()
	if controller.stopped {
		done := controller.shutdownDone
		controller.mu.Unlock()
		<-done
		return
	}
	controller.stopped = true
	controller.active.Store(false)
	if !controller.started {
		close(controller.shutdownDone)
		controller.mu.Unlock()
		return
	}
	if controller.stopTicks != nil {
		controller.stopTicks()
	}
	close(controller.stop)
	controller.mu.Unlock()

	<-controller.pollingDone
	controller.native.Stop()
	close(controller.shutdownDone)
}

func (controller *desktopMenuBarController) pollStatus() {
	defer close(controller.pollingDone)
	for {
		select {
		case <-controller.stop:
			return
		case <-controller.ticks:
			status := controller.status()
			if controller.active.Load() {
				controller.native.SetStatus(menuBarStatusTitle(status))
			}
		}
	}
}

func (controller *desktopMenuBarController) showMenu() {
	if controller.active.Load() && controller.show != nil {
		controller.show()
	}
}

func (controller *desktopMenuBarController) quitMenu() {
	if controller.active.Load() && controller.quit != nil {
		controller.quitOnce.Do(controller.quit)
	}
}

func menuBarStatusTitle(status BridgeView) string {
	switch {
	case status.NeedsRotation:
		return "Funnel endpoint changed · rotation required"
	case status.Ready:
		return "Local relay and Funnel ready"
	case status.TailscaleOnline:
		return "Tailscale online · relay setup required"
	default:
		return "Bridge setup required"
	}
}
