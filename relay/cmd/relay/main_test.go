package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mobile-egress/pairing"
)

func TestRunInitCreatesStateAndPrintsOwnerCapabilityOnlyOnce(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "state")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	status := run([]string{"init", "--state-dir", stateDir, "--public-name", "127.0.0.1", "--public-url", "https://127.0.0.1:8443"}, &stdout, &stderr)
	if status != 0 {
		t.Fatalf("relay init status = %d, stderr = %s", status, stderr.String())
	}
	const prefix = "Owner pairing bundle: "
	if strings.Count(stdout.String(), prefix) != 1 {
		t.Fatalf("relay init output = %q, want one capability line", stdout.String())
	}
	encoded := strings.TrimSpace(strings.TrimPrefix(stdout.String(), prefix))
	bundle, err := pairing.Decode(encoded)
	if err != nil {
		t.Fatalf("relay init printed an invalid pairing bundle: %v", err)
	}
	if bundle.RelayURL != "https://127.0.0.1:8443" || bundle.Role != "owner" || len(bundle.Capability) < 32 || bundle.ExpiresAt.IsZero() {
		t.Fatalf("relay init bundle is incomplete: %#v", bundle)
	}

	for _, name := range []string{"ca.crt", "ca.key", "relay.crt", "relay.key", "state.db"} {
		contents, err := os.ReadFile(filepath.Join(stateDir, name))
		if err != nil {
			t.Fatalf("relay init did not create %s: %v", name, err)
		}
		if bytes.Contains(contents, []byte(bundle.Capability)) {
			t.Fatalf("relay init persisted the raw owner capability in %s", name)
		}
	}

	stdout.Reset()
	stderr.Reset()
	status = run([]string{"init", "--state-dir", stateDir, "--public-name", "127.0.0.1", "--public-url", "https://127.0.0.1:8443"}, &stdout, &stderr)
	if status == 0 {
		t.Fatal("relay init overwrote initialized state")
	}
	if stdout.Len() != 0 {
		t.Fatalf("failed relay init printed another capability: %q", stdout.String())
	}
}

func TestRunServeRefusesMissingState(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	status := run([]string{"serve", "--state-dir", filepath.Join(t.TempDir(), "missing"), "--listen", "127.0.0.1:0"}, &stdout, &stderr)
	if status == 0 {
		t.Fatal("relay serve accepted missing initialized state")
	}
	if stdout.Len() != 0 {
		t.Fatalf("relay serve wrote unexpected stdout: %q", stdout.String())
	}
}
