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
