package tailscale

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const tailscaleExecutableEnvironment = "MOBILE_EGRESS_TEST_TAILSCALE_EXE"

func TestBackgroundCommandPolicySuppressesWindowsConsoleCreation(t *testing.T) {
	command := exec.Command("tailscale.exe", "status", "--json")
	configureBackgroundCommand(command)
	if command.SysProcAttr == nil {
		t.Fatal("background command has no Windows process attributes")
	}
	if !command.SysProcAttr.HideWindow {
		t.Fatal("background command does not hide its Windows child window")
	}
	if command.SysProcAttr.CreationFlags&0x08000000 == 0 {
		t.Fatal("background command does not use CREATE_NO_WINDOW")
	}
}

func TestExecRunnerStartsBackgroundCommandsWithoutAVisibleConsole(t *testing.T) {
	tailscaleExecutable := filepath.Join(os.Getenv("ProgramFiles"), "Tailscale", "tailscale.exe")
	if _, err := os.Stat(tailscaleExecutable); err != nil {
		t.Skip("installed Tailscale CLI is required for the visible-window regression")
	}
	guiRunner := filepath.Join(t.TempDir(), "tailscale-gui-runner.exe")
	build := exec.Command(
		"go",
		"build",
		"-ldflags=-H=windowsgui",
		"-o",
		guiRunner,
		"./testdata/gui_runner",
	)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build GUI runner: %v: %s", err, output)
	}
	baseline := visibleConsoleWindows()
	command := exec.Command(guiRunner)
	command.Env = append(os.Environ(), tailscaleExecutableEnvironment+"="+tailscaleExecutable)
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}

	newConsoleObserved := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for handle := range visibleConsoleWindows() {
			if _, existed := baseline[handle]; !existed {
				newConsoleObserved = true
				break
			}
		}
		if newConsoleObserved {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("detached parent failed: %v: %s", err, output.String())
	}
	if newConsoleObserved {
		t.Fatal("ExecRunner displayed a console window for a background command")
	}
}

func visibleConsoleWindows() map[uintptr]struct{} {
	user32 := windows.NewLazySystemDLL("user32.dll")
	enumWindows := user32.NewProc("EnumWindows")
	isWindowVisible := user32.NewProc("IsWindowVisible")
	getClassName := user32.NewProc("GetClassNameW")
	result := make(map[uintptr]struct{})
	callback := syscall.NewCallback(func(window uintptr, _ uintptr) uintptr {
		visible, _, _ := isWindowVisible.Call(window)
		if visible == 0 {
			return 1
		}
		var className [64]uint16
		length, _, _ := getClassName.Call(
			window,
			uintptr(unsafe.Pointer(&className[0])),
			uintptr(len(className)),
		)
		if length > 0 && windows.UTF16ToString(className[:length]) == "ConsoleWindowClass" {
			result[window] = struct{}{}
		}
		return 1
	})
	enumWindows.Call(callback, 0)
	return result
}
