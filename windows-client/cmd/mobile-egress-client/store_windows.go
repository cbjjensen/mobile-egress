//go:build windows

package main

import (
	"errors"
	"golang.org/x/sys/windows"
	"mobile-egress/windows-client/internal/securestore"
	"path/filepath"
)

func openServiceStore(stateDir string) (securestore.Store, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || !user.User.Sid.IsWellKnown(windows.WinLocalSystemSid) {
		return nil, errors.New("Client service state must be opened by LocalSystem; use the installed Client app")
	}
	return securestore.NewDPAPIStore(filepath.Join(stateDir, "secure"))
}
