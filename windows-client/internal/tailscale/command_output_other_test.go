//go:build !darwin

package tailscale

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

const otherOutputHelper = "MOBILE_EGRESS_TEST_OTHER_OUTPUT_HELPER"

func TestNonDarwinCommandOutputKeepsExistingOrdinaryAndStreamingBehavior(t *testing.T) {
	if os.Getenv(otherOutputHelper) == "1" {
		_, _ = fmt.Fprint(os.Stdout, "stdout")
		_, _ = fmt.Fprint(os.Stderr, "stderr")
		os.Exit(0)
	}

	t.Setenv(otherOutputHelper, "1")
	arguments := []string{"-test.run=^TestNonDarwinCommandOutputKeepsExistingOrdinaryAndStreamingBehavior$"}
	ordinary, err := runTailscaleCommandOutput(exec.Command(os.Args[0], arguments...))
	if err != nil || string(ordinary) != "stdout" {
		t.Fatalf("ordinary output = %q/%v, want %q/nil", ordinary, err, "stdout")
	}

	var observed bytes.Buffer
	streamed, err := runTailscaleStreamingCommandOutput(exec.Command(os.Args[0], arguments...), func(chunk []byte) {
		observed.Write(chunk)
	})
	if err != nil {
		t.Fatalf("streaming error = %v", err)
	}
	for _, fragment := range []string{"stdout", "stderr"} {
		if !strings.Contains(string(streamed), fragment) || !strings.Contains(observed.String(), fragment) {
			t.Fatalf("streaming output/observer = %q/%q, missing %q", streamed, observed.String(), fragment)
		}
	}
}
