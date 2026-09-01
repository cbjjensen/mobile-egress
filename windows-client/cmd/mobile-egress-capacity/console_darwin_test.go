//go:build capacityharness && darwin && cgo && !bindings

package main

import (
	"testing"

	"golang.org/x/sys/unix"
)

func TestDarwinConsoleModeClassifiesOnlyENOTTYAsNonTerminal(t *testing.T) {
	t.Parallel()
	operations := darwinConsoleModeOperations{}
	if !operations.IsNotTerminal(unix.ENOTTY) {
		t.Fatal("ENOTTY was not classified as non-terminal")
	}
	if operations.IsNotTerminal(unix.EACCES) {
		t.Fatal("EACCES was classified as non-terminal")
	}
}
