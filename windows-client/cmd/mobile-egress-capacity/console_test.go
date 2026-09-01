//go:build capacityharness && (windows || (darwin && cgo && !bindings))

package main

import (
	"errors"
	"os"
	"testing"
)

func TestDisableConsoleEchoRejectsUnclassifiedQueryError(t *testing.T) {
	t.Parallel()
	stdin, err := os.CreateTemp(t.TempDir(), "capacity-console-fixture-")
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	operations := &consoleModeFixture{getErr: errors.New("console query failed")}
	if _, err := disableConsoleEcho(stdin, operations); err == nil {
		t.Fatal("disableConsoleEcho() accepted an unclassified console query error")
	}
	if operations.setCalls != 0 {
		t.Fatalf("Set mode calls = %d, want 0", operations.setCalls)
	}
}

func TestDisableConsoleEchoAllowsClassifiedNonTerminalQueryError(t *testing.T) {
	t.Parallel()
	stdin, err := os.CreateTemp(t.TempDir(), "capacity-pipe-fixture-")
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	operations := &consoleModeFixture{
		getErr:      errors.New("not a terminal fixture"),
		notTerminal: true,
	}
	restore, err := disableConsoleEcho(stdin, operations)
	if err != nil {
		t.Fatal(err)
	}
	restore()
	if operations.setCalls != 0 {
		t.Fatalf("Set mode calls = %d, want 0", operations.setCalls)
	}
}

type consoleModeFixture struct {
	getErr      error
	notTerminal bool
	setCalls    int
}

func (fixture *consoleModeFixture) Get(*os.File) (uint64, error) {
	return 0, fixture.getErr
}

func (fixture *consoleModeFixture) Set(*os.File, uint64) error {
	fixture.setCalls++
	return nil
}

func (fixture *consoleModeFixture) IsNotTerminal(error) bool {
	return fixture.notTerminal
}
