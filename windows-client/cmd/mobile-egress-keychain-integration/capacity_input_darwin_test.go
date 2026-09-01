//go:build capacityharness && darwin

package main

import (
	"bytes"
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

func TestCapacityInputAllowsNonTerminalAndRejectsOtherQueryErrors(t *testing.T) {
	t.Parallel()
	nonTerminal, err := os.CreateTemp(t.TempDir(), "capacity-input-fixture-")
	if err != nil {
		t.Fatal(err)
	}
	restore, err := protectCapacityInput(nonTerminal)
	if err != nil {
		nonTerminal.Close()
		t.Fatal(err)
	}
	restore()
	if err := nonTerminal.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := protectCapacityInput(nonTerminal); err == nil {
		t.Fatal("protectCapacityInput() accepted a closed terminal descriptor")
	}
}

func TestCapacityInputAtomicallyFlushesAtProtectionHandoffAndRestore(t *testing.T) {
	t.Parallel()
	terminal, err := os.CreateTemp(t.TempDir(), "capacity-terminal-fixture-")
	if err != nil {
		t.Fatal(err)
	}
	defer terminal.Close()

	operations := &fixtureDarwinTermiosOperations{
		state:  unix.Termios{Lflag: unix.ECHO | unix.ICANON},
		queued: bytes.NewBufferString("SECRET-BEFORE-PROTECTION"),
	}
	protection, err := protectCapacityInputWithOperations(terminal, operations)
	if err != nil {
		t.Fatal(err)
	}
	if operations.queued.Len() != 0 {
		t.Fatal("initial echo protection did not flush preexisting terminal input")
	}
	operations.queued.WriteString("SECRET-BEFORE-CHILD-READY")
	if err := protection.PrepareHandoff(); err != nil {
		t.Fatal(err)
	}
	if operations.queued.Len() != 0 {
		t.Fatal("child handoff did not flush early queued terminal input")
	}
	operations.queued.WriteString("SECRET-BEFORE-FAILURE-RETURN")
	if err := protection.Restore(); err != nil {
		t.Fatal(err)
	}
	if err := protection.Restore(); err != nil {
		t.Fatal(err)
	}
	if operations.queued.Len() != 0 {
		t.Fatal("echo restoration did not flush queued terminal input")
	}
	if len(operations.requests) != 3 {
		t.Fatalf("termios set calls = %d, want 3 once-only flush transitions", len(operations.requests))
	}
	for index, request := range operations.requests {
		if request != unix.TIOCSETAF {
			t.Fatalf("termios request %d = %#x, want TIOCSETAF %#x", index, request, uint(unix.TIOCSETAF))
		}
	}
	if operations.states[0].Lflag&unix.ECHO != 0 || operations.states[1].Lflag&unix.ECHO != 0 {
		t.Fatal("capacity terminal echo remained enabled before child ownership")
	}
	if operations.states[2].Lflag&unix.ECHO == 0 {
		t.Fatal("capacity terminal echo was not restored")
	}
}

type fixtureDarwinTermiosOperations struct {
	state    unix.Termios
	queued   *bytes.Buffer
	requests []uint
	states   []unix.Termios
}

func (operations *fixtureDarwinTermiosOperations) Get(*os.File, uint) (*unix.Termios, error) {
	state := operations.state
	return &state, nil
}

func (operations *fixtureDarwinTermiosOperations) Set(_ *os.File, request uint, state *unix.Termios) error {
	operations.requests = append(operations.requests, request)
	operations.states = append(operations.states, *state)
	operations.state = *state
	if request == unix.TIOCSETAF {
		operations.queued.Reset()
	}
	return nil
}
