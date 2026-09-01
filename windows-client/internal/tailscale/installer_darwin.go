//go:build darwin

package tailscale

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

const fixedAppleInstallerPath = "/System/Library/CoreServices/Installer.app"

type installerDarwinPathMetadata struct {
	mode uint32
	uid  uint32
}

type installerDarwinPathInspector interface {
	ReadDirectory(string) ([]string, error)
	Lstat(string) (installerDarwinPathMetadata, error)
}

type nativeInstallerDarwinPathInspector struct{}

type installerDarwinOpenFactory func(string, ...string) *exec.Cmd

func launchDarwinInstaller(ctx context.Context, stage *stagedMacPKG) (installerSession, error) {
	return launchDarwinInstallerWithDependencies(
		ctx,
		stage,
		nativeInstallerDarwinPathInspector{},
		identityDarwinTrustRunner{newCommand: exec.CommandContext},
		exec.Command,
	)
}

func launchDarwinInstallerWithDependencies(
	ctx context.Context,
	stage stagedPathGuard,
	inspector installerDarwinPathInspector,
	runner identityTrustCommandRunner,
	newCommand installerDarwinOpenFactory,
) (installerSession, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil || stage == nil || inspector == nil || runner == nil || newCommand == nil ||
		validateAppleInstallerPathWithInspector(fixedAppleInstallerPath, inspector) != nil {
		return nil, installerNoDispatchError{}
	}
	if _, err := runner.Run(
		ctx,
		"/usr/bin/codesign",
		[]string{"--verify", "--deep", "--strict", "--verbose=4", "-R", appleInstallerRequirement, fixedAppleInstallerPath},
		newIdentityTrustEnvironment(),
		maximumIdentityTrustOutput,
	); err != nil {
		return nil, installerNoDispatchError{}
	}
	if stage.Revalidate(ctx) != nil {
		return nil, installerNoDispatchError{}
	}
	stagePath := stage.Path()
	if stagePath == "" || !filepath.IsAbs(stagePath) || stage.Revalidate(ctx) != nil || stage.Path() != stagePath || ctx.Err() != nil {
		return nil, installerNoDispatchError{}
	}

	command := newCommand("/usr/bin/open", "-W", "-a", fixedAppleInstallerPath, stagePath)
	if command == nil {
		return nil, installerNoDispatchError{}
	}
	command.Env = newIdentityTrustEnvironment()
	if err := command.Start(); err != nil {
		return nil, installerNoDispatchError{}
	}
	session := newDarwinInstallerSession(command)
	if stage.Revalidate(context.Background()) != nil || stage.Path() != stagePath {
		return session, errDarwinInstallerFailed
	}
	return session, nil
}

func validateAppleInstallerPathWithInspector(path string, inspector installerDarwinPathInspector) error {
	if path != fixedAppleInstallerPath || inspector == nil || filepath.Clean(path) != path || !filepath.IsAbs(path) {
		return errDarwinInstallerFailed
	}
	current := string(filepath.Separator)
	if !validAppleInstallerPathMetadata(inspector, current) {
		return errDarwinInstallerFailed
	}
	components := strings.Split(strings.TrimPrefix(path, string(filepath.Separator)), string(filepath.Separator))
	for _, component := range components {
		if component == "" || component == "." || component == ".." {
			return errDarwinInstallerFailed
		}
		entries, err := inspector.ReadDirectory(current)
		if err != nil || !containsExactInstallerPathEntry(entries, component) {
			return errDarwinInstallerFailed
		}
		current = filepath.Join(current, component)
		if !validAppleInstallerPathMetadata(inspector, current) {
			return errDarwinInstallerFailed
		}
	}
	return nil
}

func validAppleInstallerPathMetadata(inspector installerDarwinPathInspector, path string) bool {
	metadata, err := inspector.Lstat(path)
	return err == nil && metadata.uid == 0 && metadata.mode&unix.S_IFMT == unix.S_IFDIR && metadata.mode&0o022 == 0
}

func containsExactInstallerPathEntry(entries []string, wanted string) bool {
	for _, entry := range entries {
		if entry == wanted {
			return true
		}
	}
	return false
}

func (nativeInstallerDarwinPathInspector) ReadDirectory(path string) ([]string, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(entries))
	for index, entry := range entries {
		names[index] = entry.Name()
	}
	return names, nil
}

func (nativeInstallerDarwinPathInspector) Lstat(path string) (installerDarwinPathMetadata, error) {
	var status unix.Stat_t
	if err := unix.Lstat(path, &status); err != nil {
		return installerDarwinPathMetadata{}, err
	}
	return installerDarwinPathMetadata{mode: uint32(status.Mode), uid: status.Uid}, nil
}

type darwinInstallerSession struct {
	done  chan installerWaitResult
	ready chan struct{}

	mu     sync.RWMutex
	result installerWaitResult
}

func newDarwinInstallerSession(command *exec.Cmd) *darwinInstallerSession {
	session := &darwinInstallerSession{
		done:  make(chan installerWaitResult, 1),
		ready: make(chan struct{}),
	}
	go func() {
		result := classifyDarwinInstallerWait(command.Wait())
		session.mu.Lock()
		session.result = result
		session.mu.Unlock()
		close(session.ready)
		session.done <- result
		close(session.done)
	}()
	return session
}

func (session *darwinInstallerSession) Done() <-chan installerWaitResult {
	if session == nil {
		return nil
	}
	return session.done
}

func (session *darwinInstallerSession) Stop(ctx context.Context) (installerStopResult, error) {
	if session == nil {
		return installerStopResult{}, errMacCleanupPending
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return installerStopResult{}, ctx.Err()
	case <-session.ready:
		session.mu.RLock()
		result := session.result
		session.mu.RUnlock()
		return stopResultForWait(result), nil
	}
}

func classifyDarwinInstallerWait(err error) installerWaitResult {
	if err == nil {
		return installerWaitResult{Reason: installerTerminalNaturalZero, ExitCode: 0}
	}
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) || exitError.ProcessState == nil {
		return installerWaitResult{Reason: installerTerminalMalformed, ExitCode: -1}
	}
	status, ok := exitError.ProcessState.Sys().(syscall.WaitStatus)
	if !ok {
		return installerWaitResult{Reason: installerTerminalMalformed, ExitCode: -1}
	}
	if status.Signaled() {
		return installerWaitResult{Reason: installerTerminalExternalSignal, ExitCode: -1}
	}
	if status.Exited() && status.ExitStatus() > 0 {
		return installerWaitResult{Reason: installerTerminalNaturalNonzero, ExitCode: status.ExitStatus()}
	}
	return installerWaitResult{Reason: installerTerminalMalformed, ExitCode: -1}
}

var _ installerDarwinPathInspector = nativeInstallerDarwinPathInspector{}
var _ installerSession = (*darwinInstallerSession)(nil)
