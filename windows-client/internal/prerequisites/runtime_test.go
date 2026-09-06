package prerequisites

import (
	"context"
	"errors"
	"testing"
)

func TestRuntimeSetupSkipsInstalledAndVerifiesInstallation(t *testing.T) {
	installs := 0
	installed := true
	check := func() error {
		if installed {
			return nil
		}
		return errors.New("missing")
	}
	install := func(context.Context) error { installs++; installed = true; return nil }
	if err := EnsureRuntime(context.Background(), check, install); err != nil || installs != 0 {
		t.Fatal("reinstalled existing runtime", err)
	}
	installed = false
	if err := EnsureRuntime(context.Background(), check, install); err != nil || installs != 1 {
		t.Fatal("did not install runtime", err)
	}
	installed = false
	if err := EnsureRuntime(context.Background(), check, func(context.Context) error { return nil }); err == nil {
		t.Fatal("accepted missing runtime after installer success")
	}
}

func TestRuntimeRetryResumesFailedPrerequisiteAndHonorsCancellation(t *testing.T) {
	attempts, prompts := 0, 0
	prepare := func(context.Context) error {
		attempts++
		if attempts == 1 {
			return errors.New("offline")
		}
		return nil
	}
	err := RetryRuntime(context.Background(), prepare, func() bool { prompts++; return true })
	if err != nil || attempts != 2 || prompts != 1 {
		t.Fatalf("attempts=%d prompts=%d err=%v", attempts, prompts, err)
	}
	attempts = 0
	if err := RetryRuntime(context.Background(), prepare, func() bool { return false }); err == nil || attempts != 1 {
		t.Fatal("ignored cancelled retry")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := RetryRuntime(ctx, prepare, func() bool { t.Fatal("prompted after context cancellation"); return true }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
