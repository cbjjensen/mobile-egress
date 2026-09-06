//go:build windows

package setup

import (
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestNativeProgressDisplaysReportedStageAndCloses(t *testing.T) {
	progress, err := NewNativeProgress()
	if err != nil {
		t.Fatal(err)
	}
	defer progress.Close()
	want := "Verifying signed application files..."
	progress.Report(want)
	deadline := time.Now().Add(2 * time.Second)
	for {
		progress.mu.Lock()
		label := progress.label
		progress.mu.Unlock()
		var buffer [256]uint16
		user32.NewProc("GetWindowTextW").Call(label, uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
		if windows.UTF16ToString(buffer[:]) == want {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("progress stage did not render: %q", windows.UTF16ToString(buffer[:]))
		}
		time.Sleep(10 * time.Millisecond)
	}
}
