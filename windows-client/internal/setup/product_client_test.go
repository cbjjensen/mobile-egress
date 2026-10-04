package setup

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClientProductPayloadCannotInstallControllerOrRelay(t *testing.T) {
	if SetupExecutableName != "MobileEgressClientSetup.exe" || ControllerExecutableName != "mobile-egress-client-app.exe" || InstallRoot == `C:\Program Files\MobileEgress\Controller` {
		t.Fatal("Client setup is not isolated from controller installation")
	}
	if len(installedExecutableNames) != 2 || installedExecutableNames[0] != "mobile-egress-client-app.exe" || installedExecutableNames[1] != "mobile-egress-client.exe" {
		t.Fatal("Client install contains unexpected binaries")
	}
	for _, prohibited := range []string{"mobile-egress-windows.exe", RelayExecutableName, AdminExecutableName, "tailscale.exe"} {
		dir := t.TempDir()
		names := append([]string{}, payloadNames...)
		names[0] = prohibited
		if err := extractPayload(payloadFixture(t, names), dir); err == nil {
			t.Fatalf("accepted %s in Client payload", prohibited)
		}
		entries, _ := os.ReadDir(dir)
		if len(entries) != 0 {
			t.Fatal("unsafe payload changed installation staging")
		}
	}
	dir := t.TempDir()
	if err := extractPayload(payloadFixture(t, payloadNames), dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "mobile-egress-client-app.exe")); err != nil {
		t.Fatal(err)
	}
}
