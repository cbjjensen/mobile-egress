//go:build windows

package setup

import (
	"errors"
	"runtime"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// NativeProgress is independent of WebView2 so missing runtime installation is
// visible. Its dedicated Windows message loop remains responsive during setup.
type NativeProgress struct {
	mu    sync.Mutex
	hwnd  uintptr
	label uintptr
	text  string
	done  chan struct{}
}

var progressWindows sync.Map
var registerProgressClass sync.Once
var progressClassErr error
var progressCallback = windows.NewCallback(progressWindowProc)

type progressWindowClass struct {
	Style                              uint32
	Proc                               uintptr
	ClassExtra, WindowExtra            int32
	Instance, Icon, Cursor, Background uintptr
	Menu, Name                         *uint16
}
type progressMessage struct {
	Window  uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	X, Y    int32
	Private uint32
}

func progressWindowProc(hwnd uintptr, message uint32, wparam, lparam uintptr) uintptr {
	if value, ok := progressWindows.Load(hwnd); ok {
		p := value.(*NativeProgress)
		switch message {
		case 0x8010:
			p.mu.Lock()
			label, text := p.label, p.text
			p.mu.Unlock()
			user32.NewProc("SetWindowTextW").Call(label, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(text))))
			return 0
		case 0x8011:
			user32.NewProc("DestroyWindow").Call(hwnd)
			return 0
		case 0x10: // Do not abandon a transactional installation via the close button.
			return 0
		case 2:
			user32.NewProc("PostQuitMessage").Call(0)
			return 0
		}
	}
	result, _, _ := user32.NewProc("DefWindowProcW").Call(hwnd, uintptr(message), wparam, lparam)
	return result
}

func NewNativeProgress() (*NativeProgress, error) {
	p := &NativeProgress{done: make(chan struct{})}
	ready := make(chan error, 1)
	go p.run(ready)
	if err := <-ready; err != nil {
		return nil, err
	}
	return p, nil
}

func (p *NativeProgress) run(ready chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(p.done)
	className := windows.StringToUTF16Ptr("MobileEgressSetupProgress")
	registerProgressClass.Do(func() {
		cursor, _, _ := user32.NewProc("LoadCursorW").Call(0, 32512)
		class := progressWindowClass{Proc: progressCallback, Cursor: cursor, Background: 6, Name: className}
		result, _, _ := user32.NewProc("RegisterClassW").Call(uintptr(unsafe.Pointer(&class)))
		if result == 0 {
			progressClassErr = errors.New("register setup progress window")
		}
	})
	if progressClassErr != nil {
		ready <- progressClassErr
		return
	}
	create := user32.NewProc("CreateWindowExW")
	// A fixed native window with no close control while the transaction is active.
	hwnd, _, _ := create.Call(0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("Mobile Egress Setup"))), 0x00C00000|0x10000000, 0x80000000, 0x80000000, 570, 180, 0, 0, 0, 0)
	if hwnd == 0 {
		ready <- errors.New("create setup progress window")
		return
	}
	defer progressWindows.Delete(hwnd)
	label, _, _ := create.Call(0, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("STATIC"))), uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("Preparing Mobile Egress setup…"))), 0x40000000|0x10000000, 24, 32, 510, 82, hwnd, 0, 0, 0)
	if label == 0 {
		user32.NewProc("DestroyWindow").Call(hwnd)
		ready <- errors.New("create setup progress text")
		return
	}
	font, _, _ := windows.NewLazySystemDLL("gdi32.dll").NewProc("GetStockObject").Call(17)
	user32.NewProc("SendMessageW").Call(label, 0x30, font, 1)
	p.mu.Lock()
	p.hwnd, p.label = hwnd, label
	p.mu.Unlock()
	progressWindows.Store(hwnd, p)
	ready <- nil
	var message progressMessage
	for {
		result, _, _ := user32.NewProc("GetMessageW").Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
		if int32(result) <= 0 {
			return
		}
		user32.NewProc("TranslateMessage").Call(uintptr(unsafe.Pointer(&message)))
		user32.NewProc("DispatchMessageW").Call(uintptr(unsafe.Pointer(&message)))
	}
}

func (p *NativeProgress) Report(message string) {
	p.mu.Lock()
	p.text = message
	hwnd := p.hwnd
	p.mu.Unlock()
	user32.NewProc("PostMessageW").Call(hwnd, 0x8010, 0, 0)
}

func (p *NativeProgress) Close() {
	p.mu.Lock()
	hwnd := p.hwnd
	p.mu.Unlock()
	user32.NewProc("PostMessageW").Call(hwnd, 0x8011, 0, 0)
	<-p.done
}

func (platform *WindowsPlatform) RetryRuntime() bool {
	result, err := showMessageBox("Mobile Egress is installed, but Microsoft WebView2 Runtime is not ready. Check your internet connection and choose Retry to finish the runtime step. Mobile Egress will not be reinstalled. Choose Cancel to finish later by opening Mobile Egress from the Start Menu.", "Finish Mobile Egress setup", 0x5|messageBoxIconWarning|messageBoxTopmost)
	return err == nil && result == 4
}
