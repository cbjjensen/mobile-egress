//go:build darwin

package tailscale

import (
	"reflect"
	"testing"
)

func TestNewDarwinControllerUsesFixedResolverBackedConstruction(t *testing.T) {
	t.Parallel()

	runner := &resolverTestRunner{}
	controller := NewDarwinController(runner)
	if controller == nil || controller.resolver == nil {
		t.Fatal("NewDarwinController() did not configure signed app discovery")
	}
	if controller.executable != "" {
		t.Fatalf("NewDarwinController() configured caller path %q", controller.executable)
	}
	if controller.runner != runner {
		t.Fatal("NewDarwinController() did not retain the supplied command runner")
	}
}

func TestNewDarwinInstallerUsesFixedProductionDependencies(t *testing.T) {
	t.Parallel()

	installer := NewDarwinInstaller()
	if installer.VerifyPKG == nil || installer.LaunchInstaller == nil || installer.FindInstallation == nil || installer.Cleanup == nil {
		t.Fatal("NewDarwinInstaller() omitted a required production dependency")
	}
	if installer.PollInterval != defaultMacInstallPollInterval || installer.PollLimit != maximumMacInstallPollDuration {
		t.Fatalf("poll timing = %v/%v, want %v/%v", installer.PollInterval, installer.PollLimit, defaultMacInstallPollInterval, maximumMacInstallPollDuration)
	}
	if reflect.ValueOf(installer.VerifyPKG).Pointer() != reflect.ValueOf(verifyStagedMacPKGOnDarwin).Pointer() {
		t.Fatal("NewDarwinInstaller() did not select the fixed package verifier")
	}
	if reflect.ValueOf(installer.LaunchInstaller).Pointer() != reflect.ValueOf(launchDarwinInstaller).Pointer() {
		t.Fatal("NewDarwinInstaller() did not select the fixed Apple Installer launcher")
	}
	if reflect.ValueOf(installer.FindInstallation).Pointer() != reflect.ValueOf(resolveDarwinInstallation).Pointer() {
		t.Fatal("NewDarwinInstaller() did not select fixed signed app discovery")
	}
}
