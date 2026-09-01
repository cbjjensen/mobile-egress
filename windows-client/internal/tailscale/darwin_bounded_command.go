//go:build darwin

package tailscale

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const (
	darwinBoundedCommandPipeDrainDelay = 2 * time.Second
	darwinBoundedCommandTreeExitDelay  = 2 * time.Second
	darwinBoundedCommandPollInterval   = 10 * time.Millisecond
)

var (
	errDarwinBoundedCommandOutputLimit = errors.New("Darwin command output limit exceeded")
	errDarwinBoundedCommandProcessTree = errors.New("Darwin command process cleanup failed")
)

type darwinBoundedCommandExitMonitor interface {
	Register(int) error
	Status(int) (darwinBoundedCommandLeaderStatus, error)
	Poll(time.Duration) (bool, error)
	Close() error
}

type darwinBoundedCommandLeaderStatus uint8

const (
	darwinBoundedCommandLeaderLive darwinBoundedCommandLeaderStatus = iota + 1
	darwinBoundedCommandLeaderZombie
)

const (
	darwinProcessStatusIdle     int8 = 1
	darwinProcessStatusRunning  int8 = 2
	darwinProcessStatusSleeping int8 = 3
	darwinProcessStatusStopped  int8 = 4
	darwinProcessStatusZombie   int8 = 5
)

type darwinBoundedCommandSystem interface {
	OpenProcessExitMonitor() (darwinBoundedCommandExitMonitor, error)
	SignalProcessGroup(int) error
}

type nativeDarwinBoundedCommandSystem struct{}

type nativeDarwinBoundedCommandExitMonitor struct {
	descriptor int
	pid        int
}

type darwinBoundedCommandProcess struct {
	pid  int
	wait func() error
	kill func() error
}

// runDarwinBoundedCommand retains the direct child as an unreaped process-group
// anchor until every negative-PGID signal is complete. Output pipes are owned
// by this supervisor so their drain result cannot be hidden by Cmd.Wait error
// precedence. A separate fixed duration bounds the explicit pipe join after
// reap; Cmd.WaitDelay stays zero because os/exec does not own these *os.File
// pipes and must not start watchCtx.
func runDarwinBoundedCommand(
	ctx context.Context,
	newCommand func(context.Context, string, ...string) *exec.Cmd,
	path string,
	args []string,
	environment []string,
	outputLimit int,
) ([]byte, error) {
	return runDarwinBoundedCommandWithSystem(
		ctx,
		newCommand,
		path,
		args,
		environment,
		outputLimit,
		nativeDarwinBoundedCommandSystem{},
	)
}

func runDarwinBoundedCommandWithSystem(
	ctx context.Context,
	newCommand func(context.Context, string, ...string) *exec.Cmd,
	path string,
	args []string,
	environment []string,
	outputLimit int,
	system darwinBoundedCommandSystem,
) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if newCommand == nil {
		newCommand = exec.CommandContext
	}
	if path == "" || outputLimit <= 0 || system == nil {
		return nil, errDarwinBoundedCommandProcessTree
	}

	monitor, err := system.OpenProcessExitMonitor()
	if err != nil || monitor == nil {
		if monitor != nil {
			_ = monitor.Close()
		}
		return nil, errDarwinBoundedCommandProcessTree
	}
	monitorOpen := true
	closeMonitor := func() error {
		if !monitorOpen {
			return nil
		}
		monitorOpen = false
		return monitor.Close()
	}

	// The factory receives a non-canceling context. All cancellation is owned by
	// superviseDarwinBoundedCommand, so os/exec watchCtx can never race a group
	// signal with Process.Wait.
	command := newCommand(context.Background(), path, append([]string(nil), args...)...)
	if command == nil {
		_ = closeMonitor()
		return nil, errDarwinBoundedCommandProcessTree
	}
	command.Cancel = nil
	command.WaitDelay = 0
	command.Env = append([]string(nil), environment...)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	pipes, err := newDarwinBoundedCommandPipes()
	if err != nil {
		_ = closeMonitor()
		return nil, errDarwinBoundedCommandProcessTree
	}
	command.Stdout = pipes.stdoutWrite
	command.Stderr = pipes.stderrWrite
	if err := command.Start(); err != nil {
		pipeErr := pipes.closeAllWithoutReaders()
		monitorErr := closeMonitor()
		if pipeErr != nil || monitorErr != nil {
			return nil, errDarwinBoundedCommandProcessTree
		}
		return nil, err
	}

	collector := newDarwinBoundedOutputCollector(outputLimit)
	parentWriteErr := pipes.startReaders(collector)
	process := darwinBoundedCommandProcess{
		pid:  command.Process.Pid,
		wait: command.Wait,
		kill: command.Process.Kill,
	}
	waitErr, lifecycleErr := superviseDarwinBoundedCommand(
		ctx,
		monitor,
		process,
		collector.overflowNotification(),
		system.SignalProcessGroup,
	)
	monitorErr := closeMonitor()
	drainErr := pipes.joinReaders(darwinBoundedCommandPipeDrainDelay)
	output, overflow := collector.result()

	if parentWriteErr != nil || lifecycleErr != nil || monitorErr != nil || drainErr != nil {
		return nil, errDarwinBoundedCommandProcessTree
	}
	if overflow {
		return nil, errDarwinBoundedCommandOutputLimit
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if waitErr != nil {
		return nil, waitErr
	}
	return output, nil
}

// superviseDarwinBoundedCommand is the only owner of group signaling and the
// direct wait. Every signal precedes process.wait. Registration or monitor
// uncertainty first kills the still-anchored group, then reaps, and returns a
// fixed lifecycle failure.
func superviseDarwinBoundedCommand(
	ctx context.Context,
	monitor darwinBoundedCommandExitMonitor,
	process darwinBoundedCommandProcess,
	overflow <-chan struct{},
	signalProcessGroup func(int) error,
) (waitErr error, lifecycleErr error) {
	return superviseDarwinBoundedCommandWithClock(
		ctx,
		monitor,
		process,
		overflow,
		signalProcessGroup,
		time.Now,
	)
}

func superviseDarwinBoundedCommandWithClock(
	ctx context.Context,
	monitor darwinBoundedCommandExitMonitor,
	process darwinBoundedCommandProcess,
	overflow <-chan struct{},
	signalProcessGroup func(int) error,
	now func() time.Time,
) (waitErr error, lifecycleErr error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if monitor == nil || process.pid <= 0 || process.wait == nil || process.kill == nil ||
		signalProcessGroup == nil || now == nil {
		return nil, errDarwinBoundedCommandProcessTree
	}

	cleanupFailure := false
	signalWhileAnchored := func() {
		if err := signalProcessGroup(process.pid); err != nil {
			cleanupFailure = true
			if killErr := process.kill(); killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
				cleanupFailure = true
			}
		}
	}
	if err := monitor.Register(process.pid); err != nil {
		cleanupFailure = true
		signalWhileAnchored()
		waitErr = process.wait()
		return waitErr, errDarwinBoundedCommandProcessTree
	}

	// XNU's EVFILT_PROC/NOTE_EXIT registration observes future exit edges; it
	// does not make an exit that preceded a successful attach level-triggered.
	// Inspect the exact still-unreaped leader immediately after attaching. A
	// zombie closes the late-attach race, while a live observation is safe
	// because any later exit is covered by the already-attached filter.
	status, err := monitor.Status(process.pid)
	if err != nil || (status != darwinBoundedCommandLeaderLive && status != darwinBoundedCommandLeaderZombie) {
		cleanupFailure = true
		signalWhileAnchored()
		waitErr = process.wait()
		return waitErr, errDarwinBoundedCommandProcessTree
	}
	if status == darwinBoundedCommandLeaderZombie {
		signalWhileAnchored()
		waitErr = process.wait()
		if cleanupFailure {
			return waitErr, errDarwinBoundedCommandProcessTree
		}
		return waitErr, nil
	}

	terminationRequested := false
	terminationDeadline := time.Time{}
	for {
		if !terminationRequested && (ctx.Err() != nil || darwinBoundedCommandNotified(overflow)) {
			terminationRequested = true
			terminationDeadline = now().Add(darwinBoundedCommandTreeExitDelay)
			signalWhileAnchored()
		}

		exited, err := monitor.Poll(darwinBoundedCommandPollInterval)
		if err != nil {
			cleanupFailure = true
			signalWhileAnchored()
			break
		}
		if exited {
			// The direct leader is now a zombie, not reaped. It still pins the
			// process-group identity while this final descendant cleanup runs.
			signalWhileAnchored()
			break
		}
		if terminationRequested && !now().Before(terminationDeadline) {
			cleanupFailure = true
			signalWhileAnchored()
			if err := process.kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				cleanupFailure = true
			}
			break
		}
	}

	waitErr = process.wait()
	if cleanupFailure {
		return waitErr, errDarwinBoundedCommandProcessTree
	}
	return waitErr, nil
}

func darwinBoundedCommandNotified(notification <-chan struct{}) bool {
	select {
	case <-notification:
		return true
	default:
		return false
	}
}

func (nativeDarwinBoundedCommandSystem) OpenProcessExitMonitor() (darwinBoundedCommandExitMonitor, error) {
	descriptor, err := unix.Kqueue()
	if err != nil {
		return nil, err
	}
	unix.CloseOnExec(descriptor)
	return &nativeDarwinBoundedCommandExitMonitor{descriptor: descriptor}, nil
}

func (nativeDarwinBoundedCommandSystem) SignalProcessGroup(pid int) error {
	if pid <= 0 {
		return errDarwinBoundedCommandProcessTree
	}
	err := unix.Kill(-pid, unix.SIGKILL)
	if errors.Is(err, unix.ESRCH) {
		return nil
	}
	return err
}

func newDarwinBoundedCommandExitRegistration(pid int) unix.Kevent_t {
	return unix.Kevent_t{
		Ident:  uint64(pid),
		Filter: unix.EVFILT_PROC,
		Flags:  unix.EV_ADD | unix.EV_ENABLE | unix.EV_ONESHOT,
		Fflags: unix.NOTE_EXIT,
	}
}

func (monitor *nativeDarwinBoundedCommandExitMonitor) Register(pid int) error {
	if monitor == nil || monitor.descriptor < 0 || pid <= 0 || monitor.pid != 0 {
		return errDarwinBoundedCommandProcessTree
	}
	registration := []unix.Kevent_t{newDarwinBoundedCommandExitRegistration(pid)}
	if _, err := unix.Kevent(monitor.descriptor, registration, nil, nil); err != nil {
		return err
	}
	monitor.pid = pid
	return nil
}

func (monitor *nativeDarwinBoundedCommandExitMonitor) Status(pid int) (darwinBoundedCommandLeaderStatus, error) {
	if monitor == nil || monitor.descriptor < 0 || pid <= 0 || monitor.pid != pid {
		return 0, errDarwinBoundedCommandProcessTree
	}
	return observeNativeDarwinBoundedCommandLeaderStatus(pid)
}

func observeNativeDarwinBoundedCommandLeaderStatus(pid int) (darwinBoundedCommandLeaderStatus, error) {
	if pid <= 0 {
		return 0, errDarwinBoundedCommandProcessTree
	}
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || info == nil {
		return 0, errDarwinBoundedCommandProcessTree
	}
	return classifyDarwinBoundedCommandLeaderStatus(pid, int(info.Proc.P_pid), info.Proc.P_stat)
}

func classifyDarwinBoundedCommandLeaderStatus(
	expectedPID int,
	observedPID int,
	processStatus int8,
) (darwinBoundedCommandLeaderStatus, error) {
	if expectedPID <= 0 || observedPID != expectedPID {
		return 0, errDarwinBoundedCommandProcessTree
	}
	switch processStatus {
	case darwinProcessStatusIdle, darwinProcessStatusRunning, darwinProcessStatusSleeping, darwinProcessStatusStopped:
		return darwinBoundedCommandLeaderLive, nil
	case darwinProcessStatusZombie:
		return darwinBoundedCommandLeaderZombie, nil
	default:
		return 0, errDarwinBoundedCommandProcessTree
	}
}

func (monitor *nativeDarwinBoundedCommandExitMonitor) Poll(timeout time.Duration) (bool, error) {
	if monitor == nil || monitor.descriptor < 0 || monitor.pid <= 0 || timeout <= 0 {
		return false, errDarwinBoundedCommandProcessTree
	}
	events := make([]unix.Kevent_t, 1)
	timespec := unix.NsecToTimespec(timeout.Nanoseconds())
	count, err := unix.Kevent(monitor.descriptor, nil, events, &timespec)
	if errors.Is(err, unix.EINTR) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if count == 0 {
		return false, nil
	}
	event := events[0]
	if event.Flags&unix.EV_ERROR != 0 {
		if event.Data == 0 {
			return false, errDarwinBoundedCommandProcessTree
		}
		return false, syscall.Errno(event.Data)
	}
	if event.Ident != uint64(monitor.pid) || event.Filter != unix.EVFILT_PROC || event.Fflags&unix.NOTE_EXIT == 0 {
		return false, errDarwinBoundedCommandProcessTree
	}
	return true, nil
}

func (monitor *nativeDarwinBoundedCommandExitMonitor) Close() error {
	if monitor == nil || monitor.descriptor < 0 {
		return nil
	}
	descriptor := monitor.descriptor
	monitor.descriptor = -1
	return unix.Close(descriptor)
}

type darwinBoundedCommandPipes struct {
	stdoutRead  *os.File
	stdoutWrite *os.File
	stderrRead  *os.File
	stderrWrite *os.File
	done        chan error
}

func newDarwinBoundedCommandPipes() (*darwinBoundedCommandPipes, error) {
	stdoutRead, stdoutWrite, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	stderrRead, stderrWrite, err := os.Pipe()
	if err != nil {
		_ = stdoutRead.Close()
		_ = stdoutWrite.Close()
		return nil, err
	}
	return &darwinBoundedCommandPipes{
		stdoutRead: stdoutRead, stdoutWrite: stdoutWrite,
		stderrRead: stderrRead, stderrWrite: stderrWrite,
		done: make(chan error, 2),
	}, nil
}

func (pipes *darwinBoundedCommandPipes) startReaders(collector io.Writer) error {
	stdoutWriteErr := pipes.stdoutWrite.Close()
	stderrWriteErr := pipes.stderrWrite.Close()
	go pipes.copy(pipes.stdoutRead, collector)
	go pipes.copy(pipes.stderrRead, collector)
	if stdoutWriteErr != nil || stderrWriteErr != nil {
		return errDarwinBoundedCommandProcessTree
	}
	return nil
}

func (pipes *darwinBoundedCommandPipes) copy(reader *os.File, collector io.Writer) {
	_, err := io.Copy(collector, reader)
	pipes.done <- err
}

func (pipes *darwinBoundedCommandPipes) joinReaders(timeout time.Duration) error {
	if pipes == nil || timeout <= 0 {
		return errDarwinBoundedCommandProcessTree
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	remaining := 2
	cleanupFailure := false
	for remaining > 0 {
		select {
		case err := <-pipes.done:
			remaining--
			if err != nil && !errors.Is(err, errDarwinBoundedCommandOutputLimit) {
				cleanupFailure = true
			}
		case <-timer.C:
			cleanupFailure = true
			_ = pipes.stdoutRead.Close()
			_ = pipes.stderrRead.Close()
			for remaining > 0 {
				<-pipes.done
				remaining--
			}
		}
	}
	if err := pipes.stdoutRead.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
		cleanupFailure = true
	}
	if err := pipes.stderrRead.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
		cleanupFailure = true
	}
	if cleanupFailure {
		return errDarwinBoundedCommandProcessTree
	}
	return nil
}

func (pipes *darwinBoundedCommandPipes) closeAllWithoutReaders() error {
	if pipes == nil {
		return nil
	}
	cleanupFailure := false
	for _, descriptor := range []*os.File{pipes.stdoutRead, pipes.stdoutWrite, pipes.stderrRead, pipes.stderrWrite} {
		if descriptor != nil {
			if err := descriptor.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
				cleanupFailure = true
			}
		}
	}
	if cleanupFailure {
		return errDarwinBoundedCommandProcessTree
	}
	return nil
}

type darwinBoundedOutputCollector struct {
	mu           sync.Mutex
	buffer       bytes.Buffer
	limit        int
	overflow     bool
	overflowOnce sync.Once
	overflowed   chan struct{}
}

func newDarwinBoundedOutputCollector(limit int) *darwinBoundedOutputCollector {
	return &darwinBoundedOutputCollector{limit: limit, overflowed: make(chan struct{})}
}

func (collector *darwinBoundedOutputCollector) Write(value []byte) (int, error) {
	collector.mu.Lock()
	if collector.overflow {
		collector.mu.Unlock()
		return 0, errDarwinBoundedCommandOutputLimit
	}
	remaining := collector.limit - collector.buffer.Len()
	if remaining < 0 {
		remaining = 0
	}
	if len(value) > remaining {
		if remaining > 0 {
			_, _ = collector.buffer.Write(value[:remaining])
		}
		collector.overflow = true
		collector.mu.Unlock()
		collector.overflowOnce.Do(func() { close(collector.overflowed) })
		return remaining, errDarwinBoundedCommandOutputLimit
	}
	_, _ = collector.buffer.Write(value)
	collector.mu.Unlock()
	return len(value), nil
}

func (collector *darwinBoundedOutputCollector) overflowNotification() <-chan struct{} {
	return collector.overflowed
}

func (collector *darwinBoundedOutputCollector) result() ([]byte, bool) {
	collector.mu.Lock()
	defer collector.mu.Unlock()
	return append([]byte(nil), collector.buffer.Bytes()...), collector.overflow
}
