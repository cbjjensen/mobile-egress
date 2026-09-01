//go:build capacityharness && darwin && cgo && !bindings

package main

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

const consoleEchoInputFlag = uint64(unix.ECHO)

type darwinConsoleModeOperations struct{}

func platformConsoleModeOperations() consoleModeOperations {
	return darwinConsoleModeOperations{}
}

func (darwinConsoleModeOperations) Get(file *os.File) (uint64, error) {
	state, err := unix.IoctlGetTermios(int(file.Fd()), unix.TIOCGETA)
	if err != nil {
		return 0, err
	}
	return state.Lflag, nil
}

func (darwinConsoleModeOperations) Set(file *os.File, mode uint64) error {
	state, err := unix.IoctlGetTermios(int(file.Fd()), unix.TIOCGETA)
	if err != nil {
		return err
	}
	state.Lflag = mode
	return unix.IoctlSetTermios(int(file.Fd()), unix.TIOCSETA, state)
}

func (darwinConsoleModeOperations) IsNotTerminal(err error) bool {
	return errors.Is(err, unix.ENOTTY)
}
