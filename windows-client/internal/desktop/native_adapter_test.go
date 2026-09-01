package desktop

import (
	"bytes"
	"sync"
	"testing"
	"time"
)

func TestDesktopMenuBarControllerOwnsCallbacksStatusAndIdempotentShutdown(t *testing.T) {
	t.Parallel()

	native := newRecordingMenuBar()
	ticks := make(chan time.Time, 1)
	views := make(chan BridgeView, 1)
	shown := make(chan struct{}, 1)
	quit := make(chan struct{}, 2)
	tickerStops := 0
	controller := newDesktopMenuBarController(
		native,
		menuBarConfig{
			Icon:          []byte{0x01, 0x02, 0x03},
			Tooltip:       "ZFNF Mobile Egress",
			InitialStatus: "Bridge status unavailable",
			ShowTitle:     "Show ZFNF Mobile Egress",
			ShowTooltip:   "Open the controller window",
			QuitTitle:     "Quit controller",
			QuitTooltip:   "Close the controller; the background relay keeps running",
		},
		func() BridgeView { return <-views },
		func() { shown <- struct{}{} },
		func() { quit <- struct{}{} },
		ticks,
		func() { tickerStops++ },
	)

	controller.Start()
	started := native.snapshot()
	if !started.running {
		t.Fatal("menu bar running = false, want true after Start")
	}
	if !bytes.Equal(started.config.Icon, []byte{0x01, 0x02, 0x03}) ||
		started.config.Tooltip != "ZFNF Mobile Egress" ||
		started.config.InitialStatus != "Bridge status unavailable" ||
		started.config.ShowTitle != "Show ZFNF Mobile Egress" ||
		started.config.ShowTooltip != "Open the controller window" ||
		started.config.QuitTitle != "Quit controller" ||
		started.config.QuitTooltip != "Close the controller; the background relay keeps running" {
		t.Fatalf("menu bar config = %#v", started.config)
	}

	if !native.clickShow() {
		t.Fatal("Show item could not be clicked while menu bar was running")
	}
	select {
	case <-shown:
	default:
		t.Fatal("Show item did not invoke the controller callback")
	}

	if !native.clickQuit() || !native.clickQuit() {
		t.Fatal("Quit item could not be clicked while menu bar was running")
	}
	if got := len(quit); got != 1 {
		t.Fatalf("Quit callback count = %d, want 1", got)
	}

	statusTests := []struct {
		view BridgeView
		want string
	}{
		{view: BridgeView{NeedsRotation: true}, want: "Funnel endpoint changed · rotation required"},
		{view: BridgeView{Ready: true}, want: "Local relay and Funnel ready"},
		{view: BridgeView{TailscaleOnline: true}, want: "Tailscale online · relay setup required"},
		{view: BridgeView{}, want: "Bridge setup required"},
	}
	for _, test := range statusTests {
		views <- test.view
		ticks <- time.Unix(0, 0)
		select {
		case got := <-native.statusChanges:
			if got != test.want {
				t.Fatalf("bridge status title = %q, want %q", got, test.want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for bridge status title %q", test.want)
		}
	}

	controller.Stop()
	controller.Stop()
	stopped := native.snapshot()
	if stopped.running {
		t.Fatal("menu bar running = true, want false after Stop")
	}
	if stopped.stopCalls != 1 {
		t.Fatalf("native Stop calls = %d, want 1", stopped.stopCalls)
	}
	if tickerStops != 1 {
		t.Fatalf("ticker Stop calls = %d, want 1", tickerStops)
	}
	if native.clickShow() || native.clickQuit() {
		t.Fatal("menu item remained clickable after Stop")
	}
	views <- BridgeView{Ready: true}
	ticks <- time.Unix(1, 0)
	select {
	case status := <-native.statusChanges:
		t.Fatalf("status changed to %q after Stop", status)
	case <-time.After(100 * time.Millisecond):
	}
}

type recordingMenuBar struct {
	mu            sync.Mutex
	running       bool
	config        menuBarConfig
	show          func()
	quit          func()
	stopCalls     int
	statusChanges chan string
}

func newRecordingMenuBar() *recordingMenuBar {
	return &recordingMenuBar{statusChanges: make(chan string, 8)}
}

func (bar *recordingMenuBar) Start(config menuBarConfig, show, quit func()) {
	bar.mu.Lock()
	defer bar.mu.Unlock()
	bar.running = true
	bar.config = config
	bar.show = show
	bar.quit = quit
}

func (bar *recordingMenuBar) SetStatus(status string) {
	bar.mu.Lock()
	defer bar.mu.Unlock()
	if !bar.running {
		return
	}
	bar.statusChanges <- status
}

func (bar *recordingMenuBar) Stop() {
	bar.mu.Lock()
	defer bar.mu.Unlock()
	bar.stopCalls++
	bar.running = false
	bar.show = nil
	bar.quit = nil
}

func (bar *recordingMenuBar) clickShow() bool {
	bar.mu.Lock()
	show := bar.show
	bar.mu.Unlock()
	if show == nil {
		return false
	}
	show()
	return true
}

func (bar *recordingMenuBar) clickQuit() bool {
	bar.mu.Lock()
	quit := bar.quit
	bar.mu.Unlock()
	if quit == nil {
		return false
	}
	quit()
	return true
}

func (bar *recordingMenuBar) snapshot() recordingMenuBar {
	bar.mu.Lock()
	defer bar.mu.Unlock()
	return recordingMenuBar{
		running:   bar.running,
		config:    bar.config,
		stopCalls: bar.stopCalls,
	}
}
