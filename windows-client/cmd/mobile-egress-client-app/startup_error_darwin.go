//go:build darwin

package main

import "os/exec"

// A static AppleScript consumes the message as argv. Errors are never inserted
// into script source, shell commands, or AppleScript expressions.
func showStartupError(err error) {
	_ = exec.Command("/usr/bin/osascript", "-e", "on run argv", "-e", `display alert "Inevitable Mobile Relay could not start" message (item 1 of argv) as critical buttons {"OK"} default button "OK"`, "-e", "end run", err.Error()).Run()
}
