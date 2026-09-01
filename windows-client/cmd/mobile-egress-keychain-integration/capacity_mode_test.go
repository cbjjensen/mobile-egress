//go:build capacityharness

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"mobile-egress/windows-client/internal/mackeychainharness"
)

func TestCapacityModePassesOriginalStdinOnlyToSignedHost(t *testing.T) {
	t.Parallel()
	secretInput := bytes.NewBufferString("SECRET-CAPACITY-STDIN")
	var stdout, stderr bytes.Buffer
	capacityCalled := false
	keychainCalled := false
	exitCode := execute(context.Background(), []string{
		"-profile", "/tmp/controller.provisionprofile",
		"-identity", "Developer ID Application: Fixture Operator (A1B2C3D4E5)",
		"-capacity-run",
	}, secretInput, &stdout, &stderr, commandDependencies{
		runKeychain: func(context.Context, mackeychainharness.Runner, mackeychainharness.Config) error {
			keychainCalled = true
			return nil
		},
		runCapacity: func(_ context.Context, _ mackeychainharness.AttachedRunner, config mackeychainharness.SignedCapacityConfig) error {
			capacityCalled = true
			if config.PrepareInputHandoff == nil {
				t.Fatal("capacity mode omitted the protected-input handoff")
			}
			if config.Stdin != secretInput || config.Stdout != &stdout || config.Stderr != &stderr {
				t.Fatal("capacity mode did not preserve attached stdio")
			}
			if config.Signing.ProfilePath != "/tmp/controller.provisionprofile" ||
				config.Signing.Identity != "Developer ID Application: Fixture Operator (A1B2C3D4E5)" {
				t.Fatalf("signing config = %#v", config.Signing)
			}
			return nil
		},
	})
	if exitCode != 0 || !capacityCalled || keychainCalled {
		t.Fatalf("execute()=%d capacity=%t keychain=%t", exitCode, capacityCalled, keychainCalled)
	}
	if strings.Contains(stdout.String()+stderr.String(), "SECRET-CAPACITY-STDIN") {
		t.Fatal("unsigned launcher disclosed capacity stdin")
	}
}

func TestCapacityModeRejectsTrailingArgumentsBeforeSignedHost(t *testing.T) {
	t.Parallel()
	called := false
	var stdout, stderr bytes.Buffer
	exitCode := execute(context.Background(), []string{
		"-profile", "/tmp/controller.provisionprofile",
		"-identity", "Developer ID Application: Fixture Operator (A1B2C3D4E5)",
		"-capacity-run", "SECRET-TRAILING-ARGUMENT",
	}, strings.NewReader("SECRET-STDIN"), &stdout, &stderr, commandDependencies{
		runCapacity: func(context.Context, mackeychainharness.AttachedRunner, mackeychainharness.SignedCapacityConfig) error {
			called = true
			return nil
		},
	})
	if exitCode != 2 || called {
		t.Fatalf("execute()=%d called=%t", exitCode, called)
	}
	if strings.Contains(stdout.String()+stderr.String(), "SECRET-") {
		t.Fatal("capacity launcher disclosed rejected input")
	}
}

func TestCapacityModeDoesNotReflectRawHostErrors(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	exitCode := execute(context.Background(), []string{
		"-profile", "/tmp/controller.provisionprofile",
		"-identity", "Developer ID Application: Fixture Operator (A1B2C3D4E5)",
		"-capacity-run",
	}, strings.NewReader("SECRET-STDIN"), &stdout, &stderr, commandDependencies{
		runCapacity: func(context.Context, mackeychainharness.AttachedRunner, mackeychainharness.SignedCapacityConfig) error {
			return errors.New("SECRET-RAW-HOST-ERROR")
		},
	})
	if exitCode != 1 {
		t.Fatalf("execute()=%d, want 1", exitCode)
	}
	if strings.Contains(stdout.String()+stderr.String(), "SECRET-") {
		t.Fatal("capacity launcher reflected raw host error")
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("capacity launcher added non-event output: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestCapacityModeAbortsBeforeSigningOrReadingWhenInputCannotBeProtected(t *testing.T) {
	t.Parallel()
	secretInput := bytes.NewBufferString("SECRET-CAPACITY-STDIN")
	called := false
	var stdout, stderr bytes.Buffer
	exitCode := execute(context.Background(), []string{
		"-profile", "/tmp/controller.provisionprofile",
		"-identity", "Developer ID Application: Fixture Operator (A1B2C3D4E5)",
		"-capacity-run",
	}, secretInput, &stdout, &stderr, commandDependencies{
		protectInput: func(reader io.Reader) (capacityInputProtection, error) {
			if reader != secretInput {
				t.Fatal("capacity launcher did not protect the original stdin reader")
			}
			return nil, errors.New("SECRET-ECHO-PROTECTION-ERROR")
		},
		runCapacity: func(context.Context, mackeychainharness.AttachedRunner, mackeychainharness.SignedCapacityConfig) error {
			called = true
			return nil
		},
	})
	if exitCode != 1 || called {
		t.Fatalf("execute()=%d called=%t", exitCode, called)
	}
	if secretInput.Len() != len("SECRET-CAPACITY-STDIN") {
		t.Fatal("capacity launcher read stdin after echo protection failed")
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("capacity launcher disclosed echo-protection failure: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestCapacityModeFlushesQueuedInputWhenSigningFailsBeforeChildStart(t *testing.T) {
	t.Parallel()

	queued := bytes.NewBufferString("SECRET-EARLY-QUEUED-INPUT")
	protection := &fixtureCapacityInputProtection{queued: queued}
	var stdout, stderr bytes.Buffer
	exitCode := execute(context.Background(), []string{
		"-profile", "/tmp/controller.provisionprofile",
		"-identity", "Developer ID Application: Fixture Operator (A1B2C3D4E5)",
		"-capacity-run",
	}, queued, &stdout, &stderr, commandDependencies{
		protectInput: func(io.Reader) (capacityInputProtection, error) { return protection, nil },
		runCapacity: func(_ context.Context, _ mackeychainharness.AttachedRunner, config mackeychainharness.SignedCapacityConfig) error {
			if config.PrepareInputHandoff == nil {
				t.Fatal("capacity mode omitted the protected-input handoff")
			}
			return errors.New("fixture signing failed before child start")
		},
	})
	if exitCode != 1 {
		t.Fatalf("execute() = %d, want 1", exitCode)
	}
	if protection.prepareCalls != 0 {
		t.Fatalf("PrepareHandoff calls = %d, want 0 before child start", protection.prepareCalls)
	}
	if protection.restoreCalls != 1 || queued.Len() != 0 {
		t.Fatalf("Restore calls = %d, queued bytes = %d", protection.restoreCalls, queued.Len())
	}
	if strings.Contains(stdout.String()+stderr.String(), "SECRET-") {
		t.Fatal("capacity launcher disclosed queued input or signing failure")
	}
}

type fixtureCapacityInputProtection struct {
	queued       *bytes.Buffer
	prepareCalls int
	restoreCalls int
}

func (protection *fixtureCapacityInputProtection) PrepareHandoff() error {
	protection.prepareCalls++
	if protection.queued != nil {
		protection.queued.Reset()
	}
	return nil
}

func (protection *fixtureCapacityInputProtection) Restore() error {
	protection.restoreCalls++
	if protection.queued != nil {
		protection.queued.Reset()
	}
	return nil
}
