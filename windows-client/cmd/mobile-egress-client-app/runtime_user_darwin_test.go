//go:build darwin && client_usermode

package main

import (
	"os"
	"os/user"
	"strconv"
	"testing"
)

func TestUserRuntimeHomeCannotBeSplitByHOMEOverride(t *testing.T) {
	account, err := user.LookupId(strconv.Itoa(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	home, err := userHomeDirectory()
	if err != nil || home != account.HomeDir || home == os.Getenv("HOME") {
		t.Fatalf("runtime lock trusted HOME override: %q %v", home, err)
	}
}
