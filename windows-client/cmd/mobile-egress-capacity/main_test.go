//go:build capacityharness && (windows || (darwin && cgo && !bindings))

package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mobile-egress/windows-client/internal/capacityharness"
	"mobile-egress/windows-client/internal/relayclient"
)

func TestCLIAbortsBeforeReadingConsoleWhenEchoCannotBeDisabled(t *testing.T) {
	t.Parallel()

	token := base64.RawURLEncoding.EncodeToString([]byte("abcdefghijklmnopqrstuvwxyz012345"))
	document := `{"token":"` + token + `","targetHost":"echo.example.com","targetPort":443}`
	stdin, err := os.CreateTemp(t.TempDir(), "capacity-secret-input-")
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	if _, err := stdin.WriteString(document); err != nil {
		t.Fatal(err)
	}
	if _, err := stdin.Seek(0, 0); err != nil {
		t.Fatal(err)
	}

	consoleModes := &fakeConsoleModeOperations{
		mode:   1,
		setErr: errors.New("SECRET-CONSOLE-MODE-ERROR"),
	}
	called := false
	var stdout, stderr bytes.Buffer
	exitCode := execute(
		context.Background(), []string{"run"}, stdin, &stdout, &stderr,
		commandDependencies{
			consoleModes: consoleModes,
			run: func(context.Context, capacityharness.RunConfig) (capacityharness.Result, *capacityharness.RunError) {
				called = true
				return capacityharness.Result{}, nil
			},
		}, nil,
	)
	if exitCode != 2 || called {
		t.Fatalf("execute() = %d, run called %t", exitCode, called)
	}
	position, err := stdin.Seek(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if position != 0 {
		t.Fatalf("stdin position = %d, want 0 before secret read", position)
	}
	if consoleModes.setCalls != 1 {
		t.Fatalf("Set mode calls = %d, want 1", consoleModes.setCalls)
	}
	if strings.Contains(stdout.String()+stderr.String(), "SECRET-CONSOLE-MODE-ERROR") {
		t.Fatal("CLI disclosed the console-mode error")
	}
	assertOnlyEventSchema(t, stderr.Bytes())
}

func TestCLIAbortsBeforeReadingConsoleWhenConsoleModeCannotBeQueried(t *testing.T) {
	t.Parallel()

	stdin, err := os.CreateTemp(t.TempDir(), "capacity-secret-input-")
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	if _, err := stdin.WriteString(`{"token":"SECRET-CONSOLE-QUERY"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := stdin.Seek(0, 0); err != nil {
		t.Fatal(err)
	}

	consoleModes := &fakeConsoleModeOperations{getErr: errors.New("SECRET-CONSOLE-QUERY-ERROR")}
	called := false
	var stdout, stderr bytes.Buffer
	exitCode := execute(
		context.Background(), []string{"run"}, stdin, &stdout, &stderr,
		commandDependencies{
			consoleModes: consoleModes,
			run: func(context.Context, capacityharness.RunConfig) (capacityharness.Result, *capacityharness.RunError) {
				called = true
				return capacityharness.Result{}, nil
			},
		}, nil,
	)
	if exitCode != 2 || called {
		t.Fatalf("execute() = %d, run called %t", exitCode, called)
	}
	position, err := stdin.Seek(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if position != 0 {
		t.Fatalf("stdin position = %d, want 0 before secret read", position)
	}
	if consoleModes.setCalls != 0 {
		t.Fatalf("Set mode calls = %d, want 0", consoleModes.setCalls)
	}
	if strings.Contains(stdout.String()+stderr.String(), "SECRET-") {
		t.Fatal("CLI disclosed the console query error")
	}
	assertOnlyEventSchema(t, stderr.Bytes())
}

type fakeConsoleModeOperations struct {
	mode     uint64
	getErr   error
	setErr   error
	setCalls int
}

func (operations *fakeConsoleModeOperations) Get(*os.File) (uint64, error) {
	return operations.mode, operations.getErr
}

func (operations *fakeConsoleModeOperations) Set(*os.File, uint64) error {
	operations.setCalls++
	return operations.setErr
}

func (operations *fakeConsoleModeOperations) IsNotTerminal(error) bool {
	return false
}

func TestCLIRejectsSecretFlagsAndEnvironmentWithoutDisclosingValues(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		args    []string
		environ []string
	}{
		{name: "token flag", args: []string{"run", "--token=SECRET-FLAG-TOKEN"}},
		{name: "destination flag", args: []string{"run", "--destination=SECRET-FLAG-DESTINATION"}},
		{name: "relay flag", args: []string{"run", "--relay-url=SECRET-FLAG-RELAY"}},
		{name: "identity flag", args: []string{"run", "--identity=SECRET-FLAG-IDENTITY"}},
		{name: "certificate flag", args: []string{"target", "--certificate-file=SECRET-FLAG-CERT"}},
		{name: "private key flag", args: []string{"target", "--private-key-file=SECRET-FLAG-KEY"}},
		{name: "token environment", args: []string{"run"}, environ: []string{"MOBILE_EGRESS_CAPACITY_TOKEN=SECRET-ENV-TOKEN"}},
		{name: "destination environment", args: []string{"run"}, environ: []string{"MOBILE_EGRESS_CAPACITY_TARGET=SECRET-ENV-DESTINATION"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			exitCode := execute(context.Background(), test.args, strings.NewReader("{}"), &stdout, &stderr, commandDependencies{}, test.environ)
			if exitCode != 2 {
				t.Fatalf("execute() = %d, want 2", exitCode)
			}
			combined := stdout.String() + stderr.String()
			if strings.Contains(combined, "SECRET-") {
				t.Fatalf("CLI disclosed secret in %q", combined)
			}
			assertOnlyEventSchema(t, stderr.Bytes())
		})
	}
}

func TestRunCLIReadsStrictSecretsOnlyFromStdinAndAcceptsOnlyBoundedOperationalFlags(t *testing.T) {
	t.Parallel()

	tokenMaterial := []byte("0123456789abcdefghijklmnopqrstuv")
	secretDocument := `{"token":"` + base64.RawURLEncoding.EncodeToString(tokenMaterial) + `","targetHost":"echo.example.com","targetPort":443}`
	called := false
	dependencies := commandDependencies{
		run: func(_ context.Context, config capacityharness.RunConfig) (capacityharness.Result, *capacityharness.RunError) {
			called = true
			if config.HoldDuration != 15*time.Minute || config.PhaseTimeout != 45*time.Second || config.CleanupTimeout != 40*time.Second {
				t.Fatalf("run durations = %v/%v/%v", config.HoldDuration, config.PhaseTimeout, config.CleanupTimeout)
			}
			if config.Secrets.TargetHost != "echo.example.com" || config.Secrets.TargetPort != 443 || !bytes.Equal(config.Secrets.Token, tokenMaterial) {
				t.Fatal("run secrets were not passed from strict stdin")
			}
			result := capacityharness.Result{Attempted: 266, Open: 257, Verified: 257, Closed: 257}
			_ = config.Emitter.Emit(capacityharness.Event{
				Phase: capacityharness.PhaseComplete, Attempted: result.Attempted, Open: result.Open,
				Verified: result.Verified, Closed: result.Closed, Failure: capacityharness.FailureNone,
			})
			return result, nil
		},
	}
	var stdout, stderr bytes.Buffer
	exitCode := execute(context.Background(), []string{
		"run", "--duration=15m", "--phase-timeout=45s", "--cleanup-timeout=40s",
	}, strings.NewReader(secretDocument), &stdout, &stderr, dependencies, nil)
	if exitCode != 0 || !called {
		t.Fatalf("execute() = %d, called %t, stderr %q", exitCode, called, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
	assertOnlyEventSchema(t, stdout.Bytes())
}

func TestRunCLIEmitsFixedInputReadinessBeforeReadingStdin(t *testing.T) {
	t.Parallel()

	token := base64.RawURLEncoding.EncodeToString([]byte("abcdefghijklmnopqrstuvwxyz012345"))
	reader := &observedSecretReader{reader: strings.NewReader(
		`{"token":"` + token + `","targetHost":"echo.example.com","targetPort":443}`,
	)}
	stdout := &readinessOrderWriter{reader: reader}
	var stderr bytes.Buffer
	exitCode := execute(context.Background(), []string{"run"}, reader, stdout, &stderr, commandDependencies{
		run: func(context.Context, capacityharness.RunConfig) (capacityharness.Result, *capacityharness.RunError) {
			return capacityharness.Result{}, nil
		},
	}, nil)
	if exitCode != 0 {
		t.Fatalf("execute() = %d, stderr %q", exitCode, stderr.String())
	}
	if stdout.writes != 1 || stdout.readHadStarted {
		t.Fatalf("readiness writes = %d, stdin read before readiness = %t", stdout.writes, stdout.readHadStarted)
	}
	const readiness = `{"phase":"input","attempted":0,"open":0,"verified":0,"closed":0,"failure":"none"}` + "\n"
	if stdout.String() != readiness {
		t.Fatalf("readiness output = %q, want %q", stdout.String(), readiness)
	}
}

func TestTargetCLIUsesProtectedFileDocumentAndFixedLoopbackPortFlag(t *testing.T) {
	t.Parallel()

	token := base64.RawURLEncoding.EncodeToString([]byte("abcdefghijklmnopqrstuvwxyz012345"))
	secretDocument, certificateFile, privateKeyFile := targetSecretDocumentForCLI(t, token)
	loaded := false
	served := false
	dependencies := commandDependencies{
		loadTargetTLS: func(secrets capacityharness.TargetSecrets) (*tls.Config, error) {
			loaded = secrets.CertificateFile == certificateFile && secrets.PrivateKeyFile == privateKeyFile
			return &tls.Config{Certificates: []tls.Certificate{{}}, MinVersion: tls.VersionTLS13, MaxVersion: tls.VersionTLS13}, nil
		},
		serveTarget: func(_ context.Context, config capacityharness.TargetConfig) error {
			served = config.ListenPort == 9443 && config.ConnectionTimeout == 30*time.Second && config.CleanupTimeout == 45*time.Second
			return nil
		},
	}
	var stdout, stderr bytes.Buffer
	if exitCode := execute(context.Background(), []string{
		"target", "--listen-port=9443", "--connection-timeout=30s", "--cleanup-timeout=45s",
	}, strings.NewReader(secretDocument), &stdout, &stderr, dependencies, nil); exitCode != 0 {
		t.Fatalf("execute() = %d, stderr %q", exitCode, stderr.String())
	}
	if !loaded || !served || stderr.Len() != 0 {
		t.Fatalf("target calls = loaded %t/served %t, stderr %q", loaded, served, stderr.String())
	}
}

func TestTargetCLIEmitsFixedInputReadinessBeforeReadingStdin(t *testing.T) {
	t.Parallel()

	token := base64.RawURLEncoding.EncodeToString([]byte("abcdefghijklmnopqrstuvwxyz012345"))
	document, _, _ := targetSecretDocumentForCLI(t, token)
	reader := &observedSecretReader{reader: strings.NewReader(document)}
	stdout := &readinessOrderWriter{reader: reader}
	var stderr bytes.Buffer
	exitCode := execute(context.Background(), []string{"target"}, reader, stdout, &stderr, commandDependencies{
		loadTargetTLS: func(capacityharness.TargetSecrets) (*tls.Config, error) {
			return &tls.Config{Certificates: []tls.Certificate{{}}, MinVersion: tls.VersionTLS13, MaxVersion: tls.VersionTLS13}, nil
		},
		serveTarget: func(context.Context, capacityharness.TargetConfig) error { return nil },
	}, nil)
	if exitCode != 0 {
		t.Fatalf("execute() = %d, stderr %q", exitCode, stderr.String())
	}
	if stdout.writes != 1 || stdout.readHadStarted {
		t.Fatalf("readiness writes = %d, stdin read before readiness = %t", stdout.writes, stdout.readHadStarted)
	}
	const readiness = `{"phase":"input","attempted":0,"open":0,"verified":0,"closed":0,"failure":"none"}` + "\n"
	if stdout.String() != readiness {
		t.Fatalf("readiness output = %q, want %q", stdout.String(), readiness)
	}
}

func TestCLIRedactsRawDependencyErrorsAcrossStdoutAndStderr(t *testing.T) {
	t.Parallel()

	token := base64.RawURLEncoding.EncodeToString([]byte("abcdefghijklmnopqrstuvwxyz012345"))
	document, _, _ := targetSecretDocumentForCLI(t, token)
	dependencies := commandDependencies{
		loadTargetTLS: func(capacityharness.TargetSecrets) (*tls.Config, error) {
			return nil, errors.New("SECRET-RAW-TLS-LOADER-ERROR")
		},
	}
	var stdout, stderr bytes.Buffer
	if exitCode := execute(context.Background(), []string{"target"}, strings.NewReader(document), &stdout, &stderr, dependencies, nil); exitCode != 1 {
		t.Fatalf("execute() = %d, want 1", exitCode)
	}
	combined := stdout.String() + stderr.String()
	if strings.Contains(combined, "SECRET-RAW-TLS-LOADER-ERROR") {
		t.Fatal("CLI disclosed raw dependency error")
	}
	assertOnlyEventSchema(t, stderr.Bytes())
}

func TestCLIMapsInvalidCategorizedDependencyErrorsToFixedFallback(t *testing.T) {
	t.Parallel()

	token := base64.RawURLEncoding.EncodeToString([]byte("abcdefghijklmnopqrstuvwxyz012345"))
	document, _, _ := targetSecretDocumentForCLI(t, token)
	dependencies := commandDependencies{
		loadTargetTLS: func(capacityharness.TargetSecrets) (*tls.Config, error) {
			return nil, capacityharness.CategorizedError{Category: capacityharness.FailureCategory("SECRET-INVALID-CATEGORY")}
		},
	}
	var stdout, stderr bytes.Buffer
	if exitCode := execute(context.Background(), []string{"target"}, strings.NewReader(document), &stdout, &stderr, dependencies, nil); exitCode != 1 {
		t.Fatalf("execute() = %d, want 1", exitCode)
	}
	if strings.Contains(stdout.String()+stderr.String(), "SECRET-INVALID-CATEGORY") {
		t.Fatal("CLI disclosed an invalid dependency category")
	}
	assertOnlyEventSchema(t, stderr.Bytes())
}

func TestRunCLIRedactsProtectedRepositoryErrorsAcrossStdoutAndStderr(t *testing.T) {
	t.Parallel()

	token := base64.RawURLEncoding.EncodeToString([]byte("abcdefghijklmnopqrstuvwxyz012345"))
	document := `{"token":"` + token + `","targetHost":"echo.example.com","targetPort":443}`
	var stdout, stderr bytes.Buffer
	exitCode := execute(
		context.Background(), []string{"run"}, strings.NewReader(document), &stdout, &stderr,
		commandDependencies{owner: failingOwnerLoader{}}, nil,
	)
	if exitCode != 1 {
		t.Fatalf("execute() = %d, want 1", exitCode)
	}
	combined := stdout.String() + stderr.String()
	if strings.Contains(combined, "SECRET-PROTECTED-REPOSITORY-ERROR") {
		t.Fatal("CLI disclosed protected repository error")
	}
	assertOnlyEventSchema(t, stderr.Bytes())
}

type failingOwnerLoader struct{}

func (failingOwnerLoader) LoadOwner(context.Context) (relayclient.Identity, error) {
	return relayclient.Identity{}, errors.New("SECRET-PROTECTED-REPOSITORY-ERROR")
}

func targetSecretDocumentForCLI(t *testing.T, token string) (string, string, string) {
	t.Helper()
	directory := t.TempDir()
	certificateFile := filepath.Join(directory, "fullchain.pem")
	privateKeyFile := filepath.Join(directory, "privkey.pem")
	document, err := json.Marshal(struct {
		Token           string `json:"token"`
		Hostname        string `json:"hostname"`
		CertificateFile string `json:"certificateFile"`
		PrivateKeyFile  string `json:"privateKeyFile"`
	}{
		Token: token, Hostname: "echo.example.com",
		CertificateFile: certificateFile, PrivateKeyFile: privateKeyFile,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(document), certificateFile, privateKeyFile
}

type observedSecretReader struct {
	reader  io.Reader
	started bool
}

func (reader *observedSecretReader) Read(destination []byte) (int, error) {
	reader.started = true
	return reader.reader.Read(destination)
}

type readinessOrderWriter struct {
	bytes.Buffer
	reader         *observedSecretReader
	writes         int
	readHadStarted bool
}

func (writer *readinessOrderWriter) Write(value []byte) (int, error) {
	writer.writes++
	if writer.reader.started {
		writer.readHadStarted = true
	}
	return writer.Buffer.Write(value)
}

func assertOnlyEventSchema(t *testing.T, raw []byte) {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoded := false
	for decoder.More() {
		var event map[string]any
		if err := decoder.Decode(&event); err != nil {
			t.Fatal(err)
		}
		decoded = true
		if len(event) != 6 {
			t.Fatalf("event fields = %#v", event)
		}
		for _, key := range []string{"phase", "attempted", "open", "verified", "closed", "failure"} {
			if _, exists := event[key]; !exists {
				t.Fatalf("event missing %q: %#v", key, event)
			}
		}
	}
	if !decoded {
		t.Fatal("output contained no event")
	}
}
