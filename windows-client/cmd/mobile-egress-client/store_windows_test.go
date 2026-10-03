//go:build windows

package main

import (
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"testing"
)

func TestInteractiveProcessCannotCreateServiceDPAPIState(t *testing.T) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	if user.User.Sid.IsWellKnown(windows.WinLocalSystemSid) {
		t.Skip("requires an interactive user account")
	}
	path := filepath.Join(t.TempDir(), "service-state")
	if _, err := openNodeRepository(path); err == nil {
		t.Fatal("interactive process opened service DPAPI")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("denied request created service state")
	}
}
