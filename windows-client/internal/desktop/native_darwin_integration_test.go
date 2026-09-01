//go:build darwin && cgo && macintegration

package desktop

import (
	"os"
	"runtime"
	"testing"
	"time"
)

func init() { runtime.LockOSThread() }

func TestMain(m *testing.M) {
	result := make(chan int, 1)
	go func() {
		result <- m.Run()
		cocoaMenuBarContractStopApplicationLoop()
	}()
	cocoaMenuBarContractRunApplicationLoop()
	os.Exit(<-result)
}

func TestCocoaMenuBarUsesMainThreadWithoutReplacingApplicationDelegate(t *testing.T) {
	cocoaMenuBarContractInstallSentinelDelegate()
	cocoaMenuBarContractResetMainThreadChecks()

	show := make(chan struct{}, 1)
	quit := make(chan struct{}, 1)
	bar := newCocoaMenuBar()
	bar.Start(menuBarConfig{
		Icon:          []byte{},
		Tooltip:       "ZFNF Mobile Egress",
		InitialStatus: "Bridge status unavailable",
		ShowTitle:     "Show ZFNF Mobile Egress",
		ShowTooltip:   "Open the controller window",
		QuitTitle:     "Quit controller",
		QuitTooltip:   "Close the controller; the background relay keeps running",
	}, func() {
		show <- struct{}{}
	}, func() {
		quit <- struct{}{}
	})
	t.Cleanup(func() {
		bar.Stop()
		cocoaMenuBarContractRemoveSentinelDelegate()
	})

	waitForCocoaContract(t, "menu-bar creation", cocoaMenuBarContractMenuPresent)
	if !cocoaMenuBarContractDelegatePreserved() {
		t.Fatal("NSApp.delegate changed while creating the menu bar")
	}

	bar.SetStatus("Local relay and Funnel ready")
	waitForCocoaContract(t, "status update", func() bool {
		return cocoaMenuBarContractStatus() == "Local relay and Funnel ready"
	})
	if !cocoaMenuBarContractClickShow() {
		t.Fatal("native Show menu item was unavailable")
	}
	select {
	case <-show:
	case <-time.After(2 * time.Second):
		t.Fatal("native Show menu item did not reach Go")
	}
	if !cocoaMenuBarContractClickQuit() {
		t.Fatal("native Quit menu item was unavailable")
	}
	select {
	case <-quit:
	case <-time.After(2 * time.Second):
		t.Fatal("native Quit menu item did not reach Go")
	}

	bar.Stop()
	bar.Stop()
	waitForCocoaContract(t, "menu-bar removal", func() bool {
		return !cocoaMenuBarContractMenuPresent()
	})
	if cocoaMenuBarContractClickShow() || cocoaMenuBarContractClickQuit() {
		t.Fatal("native menu callback remained reachable after removal")
	}
	if !cocoaMenuBarContractAllOperationsOnMainThread() {
		t.Fatal("an NSStatusItem operation or menu callback ran off the AppKit main thread")
	}
	if !cocoaMenuBarContractDelegatePreserved() {
		t.Fatal("NSApp.delegate changed during menu-bar lifecycle")
	}
}

func waitForCocoaContract(t *testing.T, operation string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", operation)
}
