//go:build capacityharness && windows

package main

import (
	"context"
	"io"
)

func waitForPlatformInputGate(context.Context, io.Reader) error { return nil }
