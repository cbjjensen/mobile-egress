package tailscale

import (
	"bytes"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
)

func TestTailscaleCommandOutputBudgetCountsStdoutAndStderrTogether(t *testing.T) {
	t.Parallel()

	var cancelled atomic.Int32
	budget := newTailscaleCommandOutputBudget(5, func() {
		cancelled.Add(1)
	})
	var stdout bytes.Buffer
	stdoutWriter := budget.Writer(&stdout, nil)
	stderrWriter := budget.Writer(io.Discard, nil)

	if count, err := stdoutWriter.Write([]byte("abc")); err != nil || count != 3 {
		t.Fatalf("stdout write = %d/%v, want 3/nil", count, err)
	}
	if count, err := stderrWriter.Write([]byte("de")); err != nil || count != 2 {
		t.Fatalf("stderr write = %d/%v, want 2/nil", count, err)
	}
	if count, err := stdoutWriter.Write([]byte("f")); !errors.Is(err, errTailscaleCommandOutputLimit) || count != 0 {
		t.Fatalf("overflow write = %d/%v, want 0/%v", count, err, errTailscaleCommandOutputLimit)
	}
	if got := stdout.String(); got != "abc" {
		t.Fatalf("retained stdout = %q, want %q", got, "abc")
	}
	if !budget.Exceeded() {
		t.Fatal("budget did not record overflow")
	}
	if got := cancelled.Load(); got != 1 {
		t.Fatalf("cancel calls = %d, want 1", got)
	}
}

func TestTailscaleCommandOutputBudgetRetainsOnlyTheExactLimit(t *testing.T) {
	t.Parallel()

	var cancelled atomic.Int32
	budget := newTailscaleCommandOutputBudget(4, func() {
		cancelled.Add(1)
	})
	var output bytes.Buffer
	writer := budget.Writer(&output, nil)

	count, err := writer.Write([]byte("abcdef"))
	if !errors.Is(err, errTailscaleCommandOutputLimit) || count != 4 {
		t.Fatalf("Write() = %d/%v, want 4/%v", count, err, errTailscaleCommandOutputLimit)
	}
	if got := output.String(); got != "abcd" {
		t.Fatalf("retained output = %q, want %q", got, "abcd")
	}
	if count, err = writer.Write([]byte("z")); !errors.Is(err, errTailscaleCommandOutputLimit) || count != 0 {
		t.Fatalf("second overflow Write() = %d/%v, want 0/%v", count, err, errTailscaleCommandOutputLimit)
	}
	if got := cancelled.Load(); got != 1 {
		t.Fatalf("cancel calls = %d, want 1", got)
	}
}

func TestTailscaleCommandOutputBudgetKeepsStreamingObservationInBufferOrder(t *testing.T) {
	t.Parallel()

	budget := newTailscaleCommandOutputBudget(2_000, nil)
	var retained bytes.Buffer
	var observed bytes.Buffer
	observe := func(chunk []byte) {
		observed.Write(chunk)
		if len(chunk) > 0 {
			chunk[0] = 'x'
		}
	}
	left := budget.Writer(&retained, observe)
	right := budget.Writer(&retained, observe)

	var writers sync.WaitGroup
	for _, writer := range []io.Writer{left, right} {
		writer := writer
		writers.Add(1)
		go func() {
			defer writers.Done()
			for index := 0; index < 500; index++ {
				if _, err := writer.Write([]byte("ab")); err != nil {
					t.Errorf("Write() error = %v", err)
					return
				}
			}
		}()
	}
	writers.Wait()

	if !bytes.Equal(retained.Bytes(), observed.Bytes()) {
		t.Fatal("stream observer order differs from retained output order")
	}
	if bytes.Contains(retained.Bytes(), []byte("xb")) {
		t.Fatal("observer mutation changed retained output")
	}
}
