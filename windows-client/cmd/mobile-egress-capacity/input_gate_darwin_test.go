//go:build capacityharness && darwin && cgo && !bindings

package main

import (
	"context"
	"os"
	"testing"

	"mobile-egress/windows-client/internal/capacityharness"
)

func TestSignedInputGateAcceptsOnlyFixedPipeSignal(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		signal []byte
		ok     bool
	}{
		{name: "fixed signal", signal: []byte{capacityharness.SignedInputGateSignal}, ok: true},
		{name: "wrong signal", signal: []byte{capacityharness.SignedInputGateSignal ^ 0xff}},
		{name: "closed without signal"},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			if len(test.signal) != 0 {
				if _, err := writer.Write(test.signal); err != nil {
					writer.Close()
					t.Fatal(err)
				}
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			err = waitForSignedInputGate(context.Background(), reader)
			if test.ok && err != nil {
				t.Fatalf("waitForSignedInputGate() = %v", err)
			}
			if !test.ok && err == nil {
				t.Fatal("waitForSignedInputGate() accepted invalid signal")
			}
		})
	}
}

func TestSignedInputGateHonorsCancellation(t *testing.T) {
	t.Parallel()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitForSignedInputGate(ctx, reader); err == nil {
		t.Fatal("waitForSignedInputGate() ignored cancellation")
	}
}
