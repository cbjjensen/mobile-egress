//go:build darwin && cgo && !bindings

package desktop

/*
#cgo CFLAGS: -fblocks
#cgo LDFLAGS: -framework Cocoa
#include <stdlib.h>
#include "menu_bar_darwin.h"
*/
import "C"

import (
	"runtime/cgo"
	"sync"
	"unsafe"
)

type cocoaMenuBarCallbacks struct {
	show func()
	quit func()
}

type cocoaMenuBar struct {
	mu     sync.Mutex
	handle uintptr
}

func newCocoaMenuBar() *cocoaMenuBar { return &cocoaMenuBar{} }

func (bar *cocoaMenuBar) Start(config menuBarConfig, show, quit func()) {
	bar.mu.Lock()
	defer bar.mu.Unlock()
	if bar.handle != 0 {
		return
	}

	handle := cgo.NewHandle(cocoaMenuBarCallbacks{show: show, quit: quit})
	bar.handle = uintptr(handle)
	icon := unsafe.Pointer(nil)
	if len(config.Icon) > 0 {
		icon = C.CBytes(config.Icon)
		defer C.free(icon)
	}
	tooltip := C.CString(config.Tooltip)
	defer C.free(unsafe.Pointer(tooltip))
	initialStatus := C.CString(config.InitialStatus)
	defer C.free(unsafe.Pointer(initialStatus))
	showTitle := C.CString(config.ShowTitle)
	defer C.free(unsafe.Pointer(showTitle))
	showTooltip := C.CString(config.ShowTooltip)
	defer C.free(unsafe.Pointer(showTooltip))
	quitTitle := C.CString(config.QuitTitle)
	defer C.free(unsafe.Pointer(quitTitle))
	quitTooltip := C.CString(config.QuitTooltip)
	defer C.free(unsafe.Pointer(quitTooltip))

	C.mobile_egress_menu_bar_start(
		icon,
		C.size_t(len(config.Icon)),
		tooltip,
		initialStatus,
		showTitle,
		showTooltip,
		quitTitle,
		quitTooltip,
		C.uintptr_t(handle),
	)
}

func (bar *cocoaMenuBar) SetStatus(status string) {
	bar.mu.Lock()
	defer bar.mu.Unlock()
	if bar.handle == 0 {
		return
	}
	value := C.CString(status)
	defer C.free(unsafe.Pointer(value))
	C.mobile_egress_menu_bar_set_status(C.uintptr_t(bar.handle), value)
}

func (bar *cocoaMenuBar) Stop() {
	bar.mu.Lock()
	if bar.handle == 0 {
		bar.mu.Unlock()
		return
	}
	handle := bar.handle
	bar.handle = 0
	bar.mu.Unlock()
	C.mobile_egress_menu_bar_stop(C.uintptr_t(handle))
}

// Cocoa serializes menu actions and removal on the main thread. It calls the
// stopped callback only after removing the action target, so each handle stays
// valid for every callback that can still reach Go.
//
//export mobileEgressMenuBarShow
func mobileEgressMenuBarShow(handle C.uintptr_t) {
	callbacks := cgo.Handle(handle).Value().(cocoaMenuBarCallbacks)
	if callbacks.show != nil {
		callbacks.show()
	}
}

//export mobileEgressMenuBarQuit
func mobileEgressMenuBarQuit(handle C.uintptr_t) {
	callbacks := cgo.Handle(handle).Value().(cocoaMenuBarCallbacks)
	if callbacks.quit != nil {
		callbacks.quit()
	}
}

//export mobileEgressMenuBarStopped
func mobileEgressMenuBarStopped(handle C.uintptr_t) {
	cgo.Handle(handle).Delete()
}
