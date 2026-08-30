package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mobile-egress/windows-client/internal/nodeservice"
	"mobile-egress/windows-client/internal/securestore"
)

func TestRunBootstrapPrintsOnlyPublicNodeMaterial(t *testing.T) {
	t.Parallel()

	repository := nodeservice.NewRepository(securestore.NewMemoryStore())
	open := func(string) (*nodeservice.Repository, error) { return repository, nil }
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	status := run([]string{"bootstrap", "--state-dir", "ignored"}, &stdout, &stderr, open)
	if status != 0 {
		t.Fatalf("bootstrap status = %d, stderr = %q", status, stderr.String())
	}
	var response nodeservice.BootstrapResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("bootstrap output is not JSON: %v", err)
	}
	if response.CSRPEM == "" || response.ConfigurationPublicKey == "" {
		t.Fatalf("bootstrap response is incomplete: %#v", response)
	}
	for _, forbidden := range []string{"private", "password", "username", "credential"} {
		if strings.Contains(strings.ToLower(stdout.String()), forbidden) {
			t.Fatalf("bootstrap output exposed %q: %s", forbidden, stdout.String())
		}
	}
}

func TestRunApplyFailureDoesNotEchoSealedInput(t *testing.T) {
	t.Parallel()

	repository := nodeservice.NewRepository(securestore.NewMemoryStore())
	if _, err := repository.Bootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	open := func(string) (*nodeservice.Repository, error) { return repository, nil }
	marker := "ssm-must-not-log-this-marker"
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"ciphertext":"`+marker+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	status := run([]string{"apply-config", "--state-dir", "ignored", "--envelope-file", path}, &stdout, &stderr, open)
	if status == 0 {
		t.Fatal("apply-config accepted malformed input")
	}
	if stdout.Len() != 0 || strings.Contains(stderr.String(), marker) {
		t.Fatalf("apply-config leaked sealed input: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestRunVersion(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	status := run([]string{"--version"}, &stdout, &stderr, nil)
	if status != 0 || strings.TrimSpace(stdout.String()) == "" || stderr.Len() != 0 {
		t.Fatalf("--version = %d/%q/%q", status, stdout.String(), stderr.String())
	}
}
