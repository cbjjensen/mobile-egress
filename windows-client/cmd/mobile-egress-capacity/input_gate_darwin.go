//go:build capacityharness && darwin && cgo && !bindings

package main

import (
	"context"
	"errors"
	"io"
	"os"

	"golang.org/x/sys/unix"
	"mobile-egress/windows-client/internal/capacityharness"
)

func waitForPlatformInputGate(ctx context.Context, reader io.Reader) error {
	stdin, ok := reader.(*os.File)
	if !ok || stdin.Fd() != uintptr(0) {
		return nil
	}
	gate := os.NewFile(uintptr(capacityharness.SignedInputGateFD), "capacity-input-gate")
	if gate == nil {
		return errors.New("signed capacity input gate is unavailable")
	}
	defer gate.Close()
	return waitForSignedInputGate(ctx, gate)
}

func waitForSignedInputGate(ctx context.Context, gate *os.File) error {
	if gate == nil {
		return errors.New("signed capacity input gate is unavailable")
	}
	info, err := gate.Stat()
	if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		return errors.New("signed capacity input gate is invalid")
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		poll := []unix.PollFd{{Fd: int32(gate.Fd()), Events: unix.POLLIN | unix.POLLHUP}}
		_, err := unix.Poll(poll, 100)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return errors.New("signed capacity input gate failed")
		}
		if poll[0].Revents&(unix.POLLERR|unix.POLLNVAL) != 0 {
			return errors.New("signed capacity input gate failed")
		}
		if poll[0].Revents&(unix.POLLIN|unix.POLLHUP) == 0 {
			continue
		}
		var signal [1]byte
		read, readErr := gate.Read(signal[:])
		if readErr != nil || read != len(signal) || signal[0] != capacityharness.SignedInputGateSignal {
			return errors.New("signed capacity input gate failed closed")
		}
		return nil
	}
}
