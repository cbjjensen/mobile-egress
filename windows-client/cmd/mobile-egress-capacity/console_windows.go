//go:build capacityharness && windows

package main

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

const consoleEchoInputFlag = uint64(windows.ENABLE_ECHO_INPUT)

type windowsConsoleModeOperations struct{}

func platformConsoleModeOperations() consoleModeOperations {
	return windowsConsoleModeOperations{}
}

func (windowsConsoleModeOperations) Get(file *os.File) (uint64, error) {
	var mode uint32
	err := windows.GetConsoleMode(windows.Handle(file.Fd()), &mode)
	return uint64(mode), err
}

func (windowsConsoleModeOperations) Set(file *os.File, mode uint64) error {
	return windows.SetConsoleMode(windows.Handle(file.Fd()), uint32(mode))
}

func (windowsConsoleModeOperations) IsNotTerminal(err error) bool {
	return errors.Is(err, windows.ERROR_INVALID_HANDLE)
}
