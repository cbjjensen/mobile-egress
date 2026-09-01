//go:build capacityharness && darwin

package main

import (
	"errors"
	"io"
	"os"
	"sync"

	"golang.org/x/sys/unix"
)

type darwinTermiosOperations interface {
	Get(*os.File, uint) (*unix.Termios, error)
	Set(*os.File, uint, *unix.Termios) error
}

type systemDarwinTermiosOperations struct{}

func (systemDarwinTermiosOperations) Get(file *os.File, request uint) (*unix.Termios, error) {
	return unix.IoctlGetTermios(int(file.Fd()), request)
}

func (systemDarwinTermiosOperations) Set(file *os.File, request uint, state *unix.Termios) error {
	return unix.IoctlSetTermios(int(file.Fd()), request, state)
}

type darwinCapacityInputProtection struct {
	mu         sync.Mutex
	file       *os.File
	operations darwinTermiosOperations
	original   unix.Termios
	protected  unix.Termios
	restored   bool
	restoreErr error
}

func protectCapacityInput(reader io.Reader) (capacityInputProtection, error) {
	return protectCapacityInputWithOperations(reader, systemDarwinTermiosOperations{})
}

func protectCapacityInputWithOperations(reader io.Reader, operations darwinTermiosOperations) (capacityInputProtection, error) {
	file, ok := reader.(*os.File)
	if !ok {
		return noopCapacityInputProtection{}, nil
	}
	original, err := operations.Get(file, unix.TIOCGETA)
	if errors.Is(err, unix.ENOTTY) {
		return noopCapacityInputProtection{}, nil
	}
	if err != nil {
		return nil, err
	}
	protected := *original
	protected.Lflag &^= unix.ECHO
	if err := operations.Set(file, unix.TIOCSETAF, &protected); err != nil {
		return nil, err
	}
	return &darwinCapacityInputProtection{
		file: file, operations: operations, original: *original, protected: protected,
	}, nil
}

func (protection *darwinCapacityInputProtection) PrepareHandoff() error {
	protection.mu.Lock()
	defer protection.mu.Unlock()
	if protection.restored {
		return errors.New("capacity input protection is already restored")
	}
	return protection.operations.Set(protection.file, unix.TIOCSETAF, &protection.protected)
}

func (protection *darwinCapacityInputProtection) Restore() error {
	protection.mu.Lock()
	defer protection.mu.Unlock()
	if !protection.restored {
		protection.restored = true
		protection.restoreErr = protection.operations.Set(protection.file, unix.TIOCSETAF, &protection.original)
	}
	return protection.restoreErr
}
