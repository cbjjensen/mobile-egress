//go:build darwin

package tailscale

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strconv"
	"testing"
	"time"
)

const (
	darwinOutputHelper       = "MOBILE_EGRESS_TEST_DARWIN_OUTPUT_HELPER"
	darwinOutputHelperStdout = "MOBILE_EGRESS_TEST_DARWIN_OUTPUT_STDOUT"
	darwinOutputHelperStderr = "MOBILE_EGRESS_TEST_DARWIN_OUTPUT_STDERR"
)

func TestDarwinCommandOutputHelper(t *testing.T) {
	if os.Getenv(darwinOutputHelper) == "" {
		return
	}
	stdoutBytes, _ := strconv.Atoi(os.Getenv(darwinOutputHelperStdout))
	stderrBytes, _ := strconv.Atoi(os.Getenv(darwinOutputHelperStderr))
	writeDarwinOutputBytes(os.Stdout, 'o', stdoutBytes)
	writeDarwinOutputBytes(os.Stderr, 'e', stderrBytes)
	if os.Getenv(darwinOutputHelper) == "forever" {
		for {
			writeDarwinOutputBytes(os.Stdout, 'x', 64<<10)
		}
	}
	os.Exit(0)
}

func TestDarwinExecRunnerReturnsStdoutAndCountsStderr(t *testing.T) {
	t.Setenv(darwinOutputHelper, "bounded")
	t.Setenv(darwinOutputHelperStdout, strconv.Itoa(3<<20))
	t.Setenv(darwinOutputHelperStderr, strconv.Itoa(1<<20))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	output, err := (ExecRunner{}).Run(ctx, os.Args[0], "-test.run=^TestDarwinCommandOutputHelper$")
	if err != nil {
		t.Fatal(err)
	}
	if len(output) != 3<<20 || !bytes.Equal(output, bytes.Repeat([]byte{'o'}, 3<<20)) {
		t.Fatalf("ordinary stdout length = %d, want %d stdout-only bytes", len(output), 3<<20)
	}

	t.Setenv(darwinOutputHelperStderr, strconv.Itoa((1<<20)+1))
	output, err = (ExecRunner{}).Run(ctx, os.Args[0], "-test.run=^TestDarwinCommandOutputHelper$")
	if !errors.Is(err, errTailscaleCommandOutputLimit) || output != nil {
		t.Fatalf("over-limit Run() = %d bytes/%v, want nil/%v", len(output), err, errTailscaleCommandOutputLimit)
	}
}

func TestDarwinExecRunnerBoundsStreamingOutputAndReapsEndlessWriter(t *testing.T) {
	t.Setenv(darwinOutputHelper, "bounded")
	t.Setenv(darwinOutputHelperStdout, strconv.Itoa(3<<20))
	t.Setenv(darwinOutputHelperStderr, strconv.Itoa(1<<20))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var observed bytes.Buffer
	output, err := (ExecRunner{}).RunStreaming(ctx, os.Args[0], func(chunk []byte) {
		observed.Write(chunk)
	}, "-test.run=^TestDarwinCommandOutputHelper$")
	if err != nil || len(output) != maximumDarwinCommandOutput || !bytes.Equal(output, observed.Bytes()) {
		t.Fatalf("exact-limit RunStreaming() = %d bytes/%v, observed %d", len(output), err, observed.Len())
	}

	t.Setenv(darwinOutputHelper, "forever")
	t.Setenv(darwinOutputHelperStdout, "0")
	t.Setenv(darwinOutputHelperStderr, "0")
	observed.Reset()
	started := time.Now()
	output, err = (ExecRunner{}).RunStreaming(ctx, os.Args[0], func(chunk []byte) {
		observed.Write(chunk)
	}, "-test.run=^TestDarwinCommandOutputHelper$")
	if !errors.Is(err, errTailscaleCommandOutputLimit) {
		t.Fatalf("endless RunStreaming() error = %v, want %v", err, errTailscaleCommandOutputLimit)
	}
	if len(output) > maximumDarwinCommandOutput || observed.Len() > maximumDarwinCommandOutput || !bytes.Equal(output, observed.Bytes()) {
		t.Fatalf("bounded output/observer = %d/%d bytes", len(output), observed.Len())
	}
	if elapsed := time.Since(started); elapsed >= 5*time.Second {
		t.Fatalf("overflow cancellation took %v", elapsed)
	}
}

func writeDarwinOutputBytes(writer io.Writer, value byte, count int) {
	chunk := bytes.Repeat([]byte{value}, 64<<10)
	for count > 0 {
		write := len(chunk)
		if write > count {
			write = count
		}
		if _, err := writer.Write(chunk[:write]); err != nil {
			return
		}
		count -= write
	}
}
