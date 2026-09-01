//go:build capacityharness

package mackeychainharness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"mobile-egress/windows-client/internal/capacityharness"
)

const (
	capacityApplicationName   = "MobileEgressCapacity.app"
	capacityExecutableName    = "mobile-egress-capacity"
	capacityCleanupGrace      = 2*time.Minute + 5*time.Second
	maximumCapacityEventBytes = 512
	maximumCapacityEventLines = 1024
	maximumCapacityEventCount = 512
)

type SignedCapacityConfig struct {
	Signing             Config
	Stdin               io.Reader
	Stdout              io.Writer
	Stderr              io.Writer
	PrepareInputHandoff func() error
}

type AttachedCommand struct {
	Command
	Stdin       io.Reader
	Stdout      io.Writer
	Stderr      io.Writer
	GracePeriod time.Duration
	ExtraFiles  []*os.File
	OnStarted   func() error
}

type AttachedRunner interface {
	Runner
	RunAttached(context.Context, AttachedCommand) error
}

func RunSignedCapacity(ctx context.Context, runner AttachedRunner, config SignedCapacityConfig) error {
	if config.Stdin == nil || config.Stdout == nil || config.Stderr == nil || config.PrepareInputHandoff == nil {
		return errors.New("signed capacity streams are required")
	}
	signing, cleanup, err := prepareSigningContext(ctx, runner, config.Signing)
	if err != nil {
		return err
	}
	defer cleanup()

	executable, err := buildAndSignCapacityApplication(ctx, runner, signing)
	if err != nil {
		return err
	}
	gateReader, gateWriter, err := os.Pipe()
	if err != nil {
		return errors.New("create signed capacity input gate")
	}
	defer gateReader.Close()
	defer gateWriter.Close()
	var handoffOnce sync.Once
	var handoffCalled atomic.Bool
	var handoffErr error
	onStarted := func() error {
		handoffOnce.Do(func() {
			handoffCalled.Store(true)
			defer gateWriter.Close()
			if err := config.PrepareInputHandoff(); err != nil {
				handoffErr = errors.New("prepare signed capacity input handoff")
				return
			}
			if _, err := gateWriter.Write([]byte{capacityInputGateSignal}); err != nil {
				handoffErr = errors.New("signal signed capacity input handoff")
			}
		})
		return handoffErr
	}
	stdout := newCapacityEventStream(config.Stdout)
	stderr := newCapacityEventStream(config.Stderr)
	runErr := runner.RunAttached(ctx, AttachedCommand{
		Command: Command{Name: executable, Args: []string{"run"}, Dir: signing.repositoryRoot},
		Stdin:   config.Stdin, Stdout: stdout, Stderr: stderr, GracePeriod: capacityCleanupGrace,
		ExtraFiles: []*os.File{gateReader}, OnStarted: onStarted,
	})
	stdoutErr := stdout.Close()
	stderrErr := stderr.Close()
	if runErr != nil {
		return runErr
	}
	if !handoffCalled.Load() {
		return errors.New("signed capacity child did not acquire input ownership")
	}
	if stdoutErr != nil {
		return stdoutErr
	}
	return stderrErr
}

const capacityInputGateSignal = capacityharness.SignedInputGateSignal

func buildAndSignCapacityApplication(ctx context.Context, runner Runner, signing validatedSigningContext) (string, error) {
	applicationPath := filepath.Join(signing.workspace, capacityApplicationName)
	contentsPath := filepath.Join(applicationPath, "Contents")
	executablePath := filepath.Join(contentsPath, "MacOS", capacityExecutableName)
	if err := os.MkdirAll(filepath.Dir(executablePath), 0o700); err != nil {
		return "", fmt.Errorf("create signed capacity app bundle: %w", err)
	}
	if err := os.WriteFile(filepath.Join(contentsPath, "Info.plist"), []byte(capacityInfoPlist()), 0o600); err != nil {
		return "", fmt.Errorf("write signed capacity Info.plist: %w", err)
	}
	if err := copyFile(signing.profilePath, filepath.Join(contentsPath, "embedded.provisionprofile")); err != nil {
		return "", fmt.Errorf("embed signed capacity provisioning profile: %w", err)
	}
	if _, err := runCommand(ctx, runner, Command{
		Name: "go",
		Args: []string{
			"build", "-tags", "capacityharness", "-trimpath", "-o", executablePath,
			"./windows-client/cmd/mobile-egress-capacity",
		},
		Dir: signing.repositoryRoot,
		Env: []string{"CGO_ENABLED=1", "GOOS=darwin", "GOARCH=arm64"},
	}); err != nil {
		return "", fmt.Errorf("build signed capacity executable: %w", err)
	}
	if err := os.Chmod(executablePath, 0o700); err != nil {
		return "", fmt.Errorf("protect signed capacity executable: %w", err)
	}
	if _, err := runCommand(ctx, runner, Command{
		Name: "codesign",
		Args: []string{
			"--force", "--options", "runtime", "--timestamp=none",
			"--sign", signing.identity.sha1Fingerprint,
			"--entitlements", signing.entitlementsPath,
			applicationPath,
		},
	}); err != nil {
		return "", fmt.Errorf("sign capacity app bundle: %w", err)
	}
	if _, err := runCommand(ctx, runner, Command{
		Name: "codesign",
		Args: []string{"--verify", "--strict", "--verbose=2", applicationPath},
	}); err != nil {
		return "", fmt.Errorf("verify capacity app signature: %w", err)
	}

	signedEntitlements, err := runCommand(ctx, runner, Command{
		Name: "codesign",
		Args: []string{"--display", "--entitlements", ":-", executablePath},
	})
	if err != nil {
		return "", fmt.Errorf("read capacity signed entitlements: %w", err)
	}
	signedEntitlementsPath := filepath.Join(signing.workspace, "signed-capacity.entitlements.plist")
	if err := os.WriteFile(signedEntitlementsPath, []byte(signedEntitlements.Stdout), 0o600); err != nil {
		return "", fmt.Errorf("stage capacity signed entitlements: %w", err)
	}
	if err := verifySignedEntitlements(signedEntitlementsPath, signing.applicationIdentifier, signing.teamIdentifier); err != nil {
		return "", fmt.Errorf("verify capacity signed entitlements: %w", err)
	}
	metadata, err := runCommand(ctx, runner, Command{
		Name: "codesign",
		Args: []string{"--display", "--verbose=4", applicationPath},
	})
	if err != nil {
		return "", fmt.Errorf("read capacity signature metadata: %w", err)
	}
	metadataText := metadata.Stdout + metadata.Stderr
	for _, exactLine := range []string{
		"Identifier=" + controllerBundleIdentifier,
		"TeamIdentifier=" + signing.teamIdentifier,
	} {
		if !containsLine(metadataText, exactLine) {
			return "", fmt.Errorf("capacity signature metadata is missing %q", exactLine)
		}
	}
	certificateDirectory := filepath.Join(signing.workspace, "signed-capacity-certificates")
	if err := os.MkdirAll(certificateDirectory, 0o700); err != nil {
		return "", fmt.Errorf("create capacity certificate inspection directory: %w", err)
	}
	signedLeafPath := filepath.Join(certificateDirectory, "codesign0")
	if err := os.Remove(signedLeafPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("clear stale capacity signing certificate: %w", err)
	}
	if _, err := runCommand(ctx, runner, Command{
		Name: "codesign",
		Args: []string{"--display", "--extract-certificates", applicationPath},
		Dir:  certificateDirectory,
	}); err != nil {
		return "", fmt.Errorf("extract capacity signing certificate: %w", err)
	}
	signedLeafDER, err := os.ReadFile(signedLeafPath)
	if err != nil {
		return "", fmt.Errorf("read capacity signing certificate: %w", err)
	}
	if err := verifySignedLeafCertificate(signedLeafDER, signing.identity); err != nil {
		return "", fmt.Errorf("verify capacity signing certificate: %w", err)
	}
	return executablePath, nil
}

func capacityInfoPlist() string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleDevelopmentRegion</key>
  <string>en</string>
  <key>CFBundleExecutable</key>
  <string>` + capacityExecutableName + `</string>
  <key>CFBundleIdentifier</key>
  <string>` + controllerBundleIdentifier + `</string>
  <key>CFBundleName</key>
  <string>Mobile Egress Capacity Acceptance</string>
  <key>CFBundlePackageType</key>
  <string>APPL</string>
  <key>CFBundleShortVersionString</key>
  <string>0.0.0</string>
  <key>CFBundleVersion</key>
  <string>0</string>
</dict>
</plist>
`
}

type capacityOutputEvent struct {
	Phase     *string `json:"phase"`
	Attempted *int    `json:"attempted"`
	Open      *int    `json:"open"`
	Verified  *int    `json:"verified"`
	Closed    *int    `json:"closed"`
	Failure   *string `json:"failure"`
}

type capacityEventStream struct {
	mu          sync.Mutex
	destination io.Writer
	pending     []byte
	lines       int
	err         error
	closed      bool
}

func newCapacityEventStream(destination io.Writer) *capacityEventStream {
	return &capacityEventStream{destination: destination, pending: make([]byte, 0, maximumCapacityEventBytes)}
}

func (stream *capacityEventStream) Write(data []byte) (int, error) {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if stream.closed {
		return 0, errors.New("signed capacity event stream is closed")
	}
	if stream.err != nil {
		return 0, stream.err
	}
	stream.pending = append(stream.pending, data...)
	for {
		newline := bytes.IndexByte(stream.pending, '\n')
		if newline < 0 {
			if len(stream.pending) > maximumCapacityEventBytes {
				stream.fail(errors.New("signed capacity event exceeds limit"))
				return 0, stream.err
			}
			return len(data), nil
		}
		line := stream.pending[:newline]
		stream.pending = stream.pending[newline+1:]
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		if err := stream.forwardLine(line); err != nil {
			stream.fail(err)
			return 0, stream.err
		}
	}
}

func (stream *capacityEventStream) Close() error {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if stream.closed {
		return stream.err
	}
	stream.closed = true
	if stream.err == nil && len(stream.pending) != 0 {
		stream.fail(errors.New("signed capacity event stream ended with a partial event"))
	}
	return stream.err
}

func (stream *capacityEventStream) forwardLine(line []byte) error {
	if len(line) == 0 || len(line) > maximumCapacityEventBytes || stream.lines >= maximumCapacityEventLines {
		return errors.New("signed capacity event is outside output bounds")
	}
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.DisallowUnknownFields()
	var event capacityOutputEvent
	if err := decoder.Decode(&event); err != nil {
		return errors.New("signed capacity event is invalid")
	}
	if decoder.Decode(&struct{}{}) != io.EOF || !event.valid() {
		return errors.New("signed capacity event is invalid")
	}
	canonical, err := json.Marshal(struct {
		Phase     string `json:"phase"`
		Attempted int    `json:"attempted"`
		Open      int    `json:"open"`
		Verified  int    `json:"verified"`
		Closed    int    `json:"closed"`
		Failure   string `json:"failure"`
	}{*event.Phase, *event.Attempted, *event.Open, *event.Verified, *event.Closed, *event.Failure})
	if err != nil {
		return errors.New("signed capacity event could not be encoded")
	}
	canonical = append(canonical, '\n')
	if _, err := stream.destination.Write(canonical); err != nil {
		return errors.New("signed capacity event output failed")
	}
	stream.lines++
	return nil
}

func (event capacityOutputEvent) valid() bool {
	if event.Phase == nil || event.Attempted == nil || event.Open == nil || event.Verified == nil || event.Closed == nil || event.Failure == nil {
		return false
	}
	if !allowedCapacityPhase(*event.Phase) || !allowedCapacityFailure(*event.Failure) {
		return false
	}
	for _, count := range []int{*event.Attempted, *event.Open, *event.Verified, *event.Closed} {
		if count < 0 || count > maximumCapacityEventCount {
			return false
		}
	}
	return true
}

func allowedCapacityPhase(phase string) bool {
	switch phase {
	case "input", "preflight", "provision", "open", "verify", "limit", "hold", "replacement", "cleanup", "target", "complete":
		return true
	default:
		return false
	}
}

func allowedCapacityFailure(failure string) bool {
	switch failure {
	case "none", "input", "preflight", "provision", "session", "client_limit", "agent_limit", "tls", "authentication", "echo", "protocol", "canceled", "timeout", "cleanup", "internal":
		return true
	default:
		return false
	}
}

func (stream *capacityEventStream) fail(err error) {
	stream.err = err
	clear(stream.pending)
	stream.pending = nil
}

type attachedProcess interface {
	Signal(os.Signal) error
	Kill() error
	Wait() error
}

func waitForAttachedProcess(ctx context.Context, process attachedProcess, grace time.Duration) error {
	wait := make(chan error, 1)
	go func() { wait <- process.Wait() }()
	select {
	case err := <-wait:
		return err
	case <-ctx.Done():
	}
	_ = process.Signal(os.Interrupt)
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-wait:
		return ctx.Err()
	case <-timer.C:
		_ = process.Kill()
		<-wait
		return ctx.Err()
	}
}
