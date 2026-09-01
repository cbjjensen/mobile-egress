//go:build darwin

package tailscale

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"testing"
	"time"
)

const darwinCLIEnvironmentHelper = "MOBILE_EGRESS_TEST_DARWIN_CLI_ENV_HELPER"

func TestDarwinCommandEnvironmentPolicyUsesExplicitEnvironment(t *testing.T) {
	command := exec.Command("unused")
	command.Env = []string{"FIRST=1", "TAILSCALE_BE_CLI=0", "LAST=2"}
	configureTailscaleCommand(command)
	want := []string{"FIRST=1", "LAST=2", "TAILSCALE_BE_CLI=1"}
	if !reflect.DeepEqual(command.Env, want) {
		t.Fatalf("command environment = %#v, want %#v", command.Env, want)
	}
}

func TestDarwinExecRunnerForcesCLIEnvironmentForOrdinaryAndStreamingCommands(t *testing.T) {
	if os.Getenv(darwinCLIEnvironmentHelper) == "1" {
		_, _ = fmt.Fprint(os.Stdout, os.Getenv("TAILSCALE_BE_CLI"))
		os.Exit(0)
	}

	t.Setenv(darwinCLIEnvironmentHelper, "1")
	t.Setenv("TAILSCALE_BE_CLI", "disabled")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	arguments := []string{"-test.run=^TestDarwinExecRunnerForcesCLIEnvironmentForOrdinaryAndStreamingCommands$"}

	ordinary, err := (ExecRunner{}).Run(ctx, os.Args[0], arguments...)
	if err != nil || string(ordinary) != "1" {
		t.Fatalf("Run() = %q/%v, want %q/nil", ordinary, err, "1")
	}
	var observed bytes.Buffer
	streamed, err := (ExecRunner{}).RunStreaming(ctx, os.Args[0], func(chunk []byte) {
		observed.Write(chunk)
	}, arguments...)
	if err != nil || string(streamed) != "1" || observed.String() != "1" {
		t.Fatalf("RunStreaming() = %q/%v, observed %q; want %q/nil", streamed, err, observed.String(), "1")
	}
}
