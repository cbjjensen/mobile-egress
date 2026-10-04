package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mobile-egress/windows-client/internal/nodeservice"
	"mobile-egress/windows-client/internal/securestore"
)

func TestRuntimeStartFailureWaitsForRecoveryWithoutEndingManagement(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	attempts := make(chan int, 2)
	done := make(chan error, 1)
	go func() {
		count := 0
		done <- runRecoverableRuntime(ctx, func(ctx context.Context) error {
			count++
			attempts <- count
			if count == 1 {
				return errors.New("listener occupied")
			}
			<-ctx.Done()
			return nil
		}, time.Millisecond)
	}()
	for want := 1; want <= 2; want++ {
		select {
		case got := <-attempts:
			if got != want {
				t.Fatal(got)
			}
		case err := <-done:
			t.Fatalf("management ended on recoverable failure: %v", err)
		case <-time.After(time.Second):
			t.Fatal("runtime did not retry")
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("stop did not finish")
	}
}

func TestLegacyBootstrapRequiresDirectMigrationWithoutOpeningSecrets(t *testing.T) {
	t.Parallel()

	repository := nodeservice.NewRepository(securestore.NewMemoryStore())
	opened := false
	open := func(string) (*nodeservice.Repository, error) { opened = true; return repository, nil }
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	status := run([]string{"bootstrap", "--state-dir", "ignored"}, &stdout, &stderr, open)
	if status == 0 || opened || stdout.Len() != 0 || !strings.Contains(stderr.String(), "direct") {
		t.Fatalf("legacy bootstrap accepted: status=%d opened=%v stdout=%q stderr=%q", status, opened, stdout.String(), stderr.String())
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
