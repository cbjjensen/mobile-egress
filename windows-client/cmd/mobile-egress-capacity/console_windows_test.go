//go:build capacityharness && windows

package main

import (
	"testing"

	"golang.org/x/sys/windows"
)

func TestConsoleModeClassifiesOnlyInvalidHandleAsNonConsole(t *testing.T) {
	t.Parallel()
	operations := windowsConsoleModeOperations{}
	if !operations.IsNotTerminal(windows.ERROR_INVALID_HANDLE) {
		t.Fatal("ERROR_INVALID_HANDLE was not classified as non-console")
	}
	if operations.IsNotTerminal(windows.ERROR_ACCESS_DENIED) {
		t.Fatal("ERROR_ACCESS_DENIED was classified as non-console")
	}
}
