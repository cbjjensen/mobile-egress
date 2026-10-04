//go:build darwin

package main

import (
	"errors"
	"os"
)

func checkGUIPrivileges() error {
	return checkGUIUser(os.Geteuid())
}

func checkGUIUser(uid int) error {
	if uid == 0 {
		return errors.New("Open ZFNF Mobile Egress Client from Applications as the installation owner; the app cannot run as root.")
	}
	return nil
}
